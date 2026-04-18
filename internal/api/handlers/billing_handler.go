package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"merger/backend/internal/middleware"
	"merger/backend/internal/repository"
	billingpkg "merger/backend/internal/services/billing"
	shopifysvc "merger/backend/internal/services/shopify"
	"merger/backend/internal/utils"
)

type BillingHandler struct {
	settingsRepo repository.SettingsRepository
	merchantRepo repository.MerchantRepository
	encryptor    *utils.Encryptor
	appURL       string // e.g. https://identity-manager-4ew0.onrender.com
	apiKey       string // Shopify API key — used to redirect back into the admin
	isTest       bool   // when true, subscriptions are created in test mode
	log          zerolog.Logger
}

func NewBillingHandler(
	settingsRepo repository.SettingsRepository,
	merchantRepo repository.MerchantRepository,
	encryptor *utils.Encryptor,
	appURL string,
	apiKey string,
	isTest bool,
	log zerolog.Logger,
) *BillingHandler {
	return &BillingHandler{
		settingsRepo: settingsRepo,
		merchantRepo: merchantRepo,
		encryptor:    encryptor,
		appURL:       appURL,
		apiKey:       apiKey,
		isTest:       isTest,
		log:          log,
	}
}

// Subscribe creates an appSubscription and returns the Shopify confirmation URL.
// The frontend must redirect the merchant to that URL (top-frame navigation).
//
// POST /api/billing/subscribe
// Body: {"plan": "basic"} or {"plan": "pro"}
func (h *BillingHandler) Subscribe(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	var req struct {
		Plan string `json:"plan" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plan is required"})
		return
	}

	plans := billingpkg.Plans()
	info, ok := plans[req.Plan]
	if !ok || req.Plan == billingpkg.PlanFree {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid plan — choose basic or pro"})
		return
	}

	// Decrypt the merchant's access token to call the Billing API.
	token, err := h.encryptor.Decrypt(merchant.AccessTokenEnc)
	if err != nil {
		h.log.Error().Err(err).Str("shop", merchant.ShopDomain).Msg("billing: decrypt token failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// returnUrl: Shopify redirects here after the merchant approves/declines.
	returnURL := fmt.Sprintf("%s/api/billing/callback?plan=%s&shop=%s",
		h.appURL, req.Plan, merchant.ShopDomain)

	client := shopifysvc.NewClient(merchant.ShopDomain, token, h.log)
	billingSvc := shopifysvc.NewBillingService(client)

	result, err := billingSvc.CreateSubscription(
		c.Request.Context(),
		info.Name, // "Basic" or "Pro"
		info.PriceUSD,
		returnURL,
		h.isTest,
	)
	if err != nil {
		h.log.Error().Err(err).Str("shop", merchant.ShopDomain).Str("plan", req.Plan).Msg("billing: create subscription failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create subscription"})
		return
	}

	h.log.Info().
		Str("shop", merchant.ShopDomain).
		Str("plan", req.Plan).
		Str("subscription_id", result.SubscriptionID).
		Msg("billing: subscription created — awaiting merchant approval")

	c.JSON(http.StatusOK, gin.H{
		"confirmation_url": result.ConfirmationURL,
		"subscription_id":  result.SubscriptionID,
	})
}

// Callback handles the redirect from Shopify after the merchant approves or
// declines the subscription.
//
// GET /api/billing/callback?plan=basic&shop=test.myshopify.com&charge_id=gid://...
func (h *BillingHandler) Callback(c *gin.Context) {
	plan := c.Query("plan")
	shop := c.Query("shop")
	chargeID := c.Query("charge_id") // Shopify appends this on approval

	if plan == "" || shop == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing plan or shop"})
		return
	}

	merchant, err := h.merchantRepo.FindByDomain(c.Request.Context(), shop)
	if err != nil {
		h.log.Warn().Str("shop", shop).Msg("billing callback: merchant not found")
		c.JSON(http.StatusNotFound, gin.H{"error": "merchant not found"})
		return
	}

	// If no charge_id — merchant declined. Redirect back to app without changing plan.
	if chargeID == "" {
		h.log.Info().Str("shop", shop).Str("plan", plan).Msg("billing: merchant declined subscription")
		c.Redirect(http.StatusTemporaryRedirect,
			fmt.Sprintf("https://%s/admin/apps/%s", shop, h.apiKey))
		return
	}

	// Verify the subscription is actually ACTIVE before activating the plan.
	token, err := h.encryptor.Decrypt(merchant.AccessTokenEnc)
	if err != nil {
		h.log.Error().Err(err).Str("shop", shop).Msg("billing callback: decrypt token failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	client := shopifysvc.NewClient(shop, token, h.log)
	billingSvc := shopifysvc.NewBillingService(client)

	status, err := billingSvc.GetSubscriptionStatus(c.Request.Context(), chargeID)
	if err != nil {
		h.log.Warn().Err(err).Str("charge_id", chargeID).Msg("billing callback: status check failed")
		// Non-fatal — proceed optimistically on status check failure.
	}

	if status != "" && status != "ACTIVE" && status != "PENDING" {
		h.log.Warn().Str("shop", shop).Str("status", status).Msg("billing callback: subscription not active")
		c.Redirect(http.StatusTemporaryRedirect,
			fmt.Sprintf("https://%s/admin/apps/%s", shop, h.apiKey))
		return
	}

	// Activate the plan.
	if err := h.settingsRepo.UpdatePlan(c.Request.Context(), merchant.ID, plan, &chargeID); err != nil {
		h.log.Error().Err(err).Str("shop", shop).Str("plan", plan).Msg("billing callback: update plan failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to activate plan"})
		return
	}

	h.log.Info().Str("shop", shop).Str("plan", plan).Str("charge_id", chargeID).Msg("billing: plan activated")

	// Redirect the merchant back into the embedded app.
	c.Redirect(http.StatusTemporaryRedirect,
		fmt.Sprintf("https://%s/admin/apps/%s", shop, h.apiKey))
}

// Plans returns the available plans and their details (unauthenticated — used
// by the pricing page before the merchant has installed the app).
//
// GET /api/billing/plans
func (h *BillingHandler) Plans(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"plans": billingpkg.Plans()})
}

// CurrentPlan returns the authenticated merchant's current plan + usage.
//
// GET /api/billing/current
func (h *BillingHandler) CurrentPlan(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	s, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"plan":              billingpkg.PlanFree,
			"merges_this_month": 0,
			"merge_limit":       billingpkg.MergeLimitFree,
			"customer_limit":    billingpkg.CustomerLimitFree,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"plan":              s.Plan,
		"merges_this_month": s.MergesThisMonth,
		"merge_limit":       billingpkg.MergeLimit(s.Plan),
		"customer_limit":    billingpkg.CustomerLimit(s.Plan),
		"features": gin.H{
			billingpkg.FeatureOrderIntelligence: billingpkg.IsFeatureEnabled(s.Plan, billingpkg.FeatureOrderIntelligence),
			billingpkg.FeatureMergeHistory:      billingpkg.IsFeatureEnabled(s.Plan, billingpkg.FeatureMergeHistory),
			billingpkg.FeatureSnapshots:         billingpkg.IsFeatureEnabled(s.Plan, billingpkg.FeatureSnapshots),
			billingpkg.FeatureAutoDetect:        billingpkg.IsFeatureEnabled(s.Plan, billingpkg.FeatureAutoDetect),
			billingpkg.FeatureBulkMerge:         billingpkg.IsFeatureEnabled(s.Plan, billingpkg.FeatureBulkMerge),
			billingpkg.FeatureRestore:           billingpkg.IsFeatureEnabled(s.Plan, billingpkg.FeatureRestore),
		},
	})
}
