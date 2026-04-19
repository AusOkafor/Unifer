package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	shopifysvc "merger/backend/internal/services/shopify"
	"merger/backend/pkg/shopifyauth"
)

type JobDispatcher interface {
	Dispatch(ctx context.Context, jobType string, merchantID uuid.UUID, payload interface{}) (uuid.UUID, error)
}

// IdempotencyStore deduplicates repeated Shopify webhook deliveries.
// Shopify retries failed webhooks up to 19 times over 48 hours; without
// deduplication the same event would trigger multiple detection jobs or
// cache mutations.
type IdempotencyStore interface {
	// IsProcessed returns true if this webhook ID has already been handled.
	// It atomically marks unseen IDs as processed on the first call.
	IsProcessed(ctx context.Context, webhookID string) (bool, error)
}

type WebhookHandler struct {
	shopifySecret     string
	merchantRepo      repository.MerchantRepository
	customerCacheRepo repository.CustomerCacheRepository
	settingsRepo      repository.SettingsRepository
	jobDispatcher     JobDispatcher
	idempotency       IdempotencyStore // may be nil — dedup skipped if not wired
	log               zerolog.Logger
}

func NewWebhookHandler(
	shopifySecret string,
	merchantRepo repository.MerchantRepository,
	customerCacheRepo repository.CustomerCacheRepository,
	settingsRepo repository.SettingsRepository,
	jobDispatcher JobDispatcher,
	idempotency IdempotencyStore,
	log zerolog.Logger,
) *WebhookHandler {
	return &WebhookHandler{
		shopifySecret:     shopifySecret,
		merchantRepo:      merchantRepo,
		customerCacheRepo: customerCacheRepo,
		settingsRepo:      settingsRepo,
		jobDispatcher:     jobDispatcher,
		idempotency:       idempotency,
		log:               log,
	}
}

// Handle processes incoming Shopify webhook events.
// Body is read before any parsing so HMAC validation uses the raw bytes.
func (h *WebhookHandler) Handle(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	sig := c.GetHeader("X-Shopify-Hmac-SHA256")
	if !shopifyauth.ValidateWebhookHMAC(body, sig, h.shopifySecret) {
		h.log.Warn().Str("ip", c.ClientIP()).Msg("webhook HMAC validation failed")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}

	// Idempotency check — Shopify may retry a webhook up to 19 times over 48 hours.
	// Drop duplicates before doing any DB or queue work.
	if h.idempotency != nil {
		webhookID := c.GetHeader("X-Shopify-Webhook-Id")
		if webhookID != "" {
			already, err := h.idempotency.IsProcessed(c.Request.Context(), webhookID)
			if err != nil {
				// Redis error: log but continue — better to process twice than lose events.
				h.log.Warn().Err(err).Str("webhook_id", webhookID).Msg("idempotency check failed, processing anyway")
			} else if already {
				h.log.Debug().Str("webhook_id", webhookID).Msg("duplicate webhook delivery — skipped")
				c.JSON(http.StatusOK, gin.H{})
				return
			}
		}
	}

	shop := c.GetHeader("X-Shopify-Shop-Domain")
	topic := c.GetHeader("X-Shopify-Topic")

	// GDPR and app/uninstalled topics must always return 200 — even if the
	// merchant is no longer in our database (shop/redact arrives 48 days after
	// uninstall). Handle these before the merchant lookup.
	switch topic {
	case "customers/data_request":
		h.handleDataRequest(c, body, shop)
		c.JSON(http.StatusOK, gin.H{})
		return
	case "customers/redact":
		h.handleCustomerRedact(c, body, shop)
		c.JSON(http.StatusOK, gin.H{})
		return
	case "shop/redact":
		h.handleShopRedact(c, body, shop)
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	merchant, err := h.merchantRepo.FindByDomain(c.Request.Context(), shop)
	if err != nil {
		h.log.Warn().Str("shop", shop).Msg("webhook from unknown merchant")
		c.JSON(http.StatusOK, gin.H{}) // always 200 to Shopify
		return
	}

	switch topic {
	case "customers/create", "customers/update":
		h.handleCustomerUpsert(c, body, merchant, topic)
	case "customers/delete":
		h.handleCustomerDelete(c, body, merchant)
	case "orders/create", "orders/updated":
		h.handleOrderUpsert(c, body, merchant, topic)
	case "app/uninstalled":
		h.handleAppUninstalled(c, merchant)
	default:
		h.log.Debug().Str("topic", topic).Msg("unhandled webhook topic")
	}

	c.JSON(http.StatusOK, gin.H{})
}

func (h *WebhookHandler) handleCustomerUpsert(c *gin.Context, body []byte, merchant *models.Merchant, topic string) {
	payload, err := shopifysvc.ParseCustomerPayload(body)
	if err != nil {
		h.log.Error().Err(err).Str("topic", topic).Msg("parse customer webhook payload")
		return
	}

	name := strings.TrimSpace(payload.FirstName + " " + payload.LastName)
	// Build address JSON in flat map format (matches sync_service format)
	// so scorer.extractAddress can parse it correctly.
	addrJSON := buildWebhookAddressJSON(payload.Addresses)
	tags := strings.Split(payload.Tags, ",")
	cleanTags := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			cleanTags = append(cleanTags, t)
		}
	}

	var shopifyCreatedAt *time.Time
	if payload.CreatedAt != "" {
		if t, err := time.Parse(time.RFC3339, payload.CreatedAt); err == nil {
			shopifyCreatedAt = &t
		}
	}

	customer := &models.CustomerCache{
		MerchantID:        merchant.ID,
		ShopifyCustomerID: payload.ID,
		Email:             payload.Email,
		Name:              name,
		Phone:             payload.Phone,
		AddressJSON:       addrJSON,
		Tags:              cleanTags,
		OrdersCount:       payload.OrdersCount,
		TotalSpent:        payload.TotalSpent,
		Note:              payload.Note,
		State:             payload.State,
		VerifiedEmail:     payload.VerifiedEmail,
		ShopifyCreatedAt:  shopifyCreatedAt,
	}

	if err := h.customerCacheRepo.Upsert(c.Request.Context(), customer); err != nil {
		h.log.Error().Err(err).Int64("shopify_id", payload.ID).Msg("customer cache upsert")
		return
	}

	// Dispatch a duplicate detection job only when:
	//   1. auto_detect is enabled (master switch)
	//   2. scan_frequency is "webhook" (not "daily" or "manual")
	autoDetect := true
	scanFrequency := "webhook"
	if h.settingsRepo != nil {
		if s, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID); err == nil {
			autoDetect = s.AutoDetect
			if s.ScanFrequency != "" {
				scanFrequency = s.ScanFrequency
			}
		}
	}
	if autoDetect && scanFrequency == "webhook" && h.jobDispatcher != nil {
		if _, err := h.jobDispatcher.Dispatch(
			c.Request.Context(),
			models.JobTypeDetectDuplicates,
			merchant.ID,
			map[string]interface{}{"merchant_id": merchant.ID.String()},
		); err != nil {
			h.log.Warn().Err(err).Msg("dispatch detect job after webhook")
		}
	}

	h.log.Debug().Int64("shopify_id", payload.ID).Str("shop", merchant.ShopDomain).Msg("customer cache updated")
}

// buildWebhookAddressJSON extracts the first address from the Shopify webhook
// payload and serializes it as a flat map[string]string, matching the format
// produced by the sync service so that scorer.extractAddress can parse it.
func buildWebhookAddressJSON(addresses []shopifysvc.Address) []byte {
	if len(addresses) == 0 {
		return []byte("{}")
	}
	a := addresses[0]
	m := map[string]string{
		"address1": a.Address1,
		"city":     a.City,
		"province": a.Province,
		"zip":      a.Zip,
		"country":  a.Country,
	}
	b, _ := json.Marshal(m)
	return b
}

// handleOrderUpsert handles orders/create and orders/updated webhooks.
// It patches the customer cache with the latest order stats (order count and
// total spent) that Shopify includes in the order payload, so the cache stays
// accurate without a full customer sync. If the customer isn't cached yet the
// UPDATE is a no-op — the next full sync will backfill them.
func (h *WebhookHandler) handleOrderUpsert(c *gin.Context, body []byte, merchant *models.Merchant, topic string) {
	payload, err := shopifysvc.ParseOrderPayload(body)
	if err != nil {
		h.log.Error().Err(err).Str("topic", topic).Msg("parse order webhook payload")
		return
	}

	if payload.Customer.ID == 0 {
		// Draft orders and orders without an associated customer have no ID.
		h.log.Debug().Int64("order_id", payload.ID).Msg("order webhook: no customer — skipping cache update")
		return
	}

	if err := h.customerCacheRepo.UpdateOrderStats(
		c.Request.Context(),
		merchant.ID,
		payload.Customer.ID,
		payload.Customer.OrdersCount,
		payload.Customer.TotalSpent,
	); err != nil {
		h.log.Error().Err(err).
			Int64("shopify_customer_id", payload.Customer.ID).
			Str("topic", topic).
			Msg("order webhook: customer cache update failed")
		return
	}

	h.log.Debug().
		Int64("order_id", payload.ID).
		Int64("customer_id", payload.Customer.ID).
		Str("shop", merchant.ShopDomain).
		Msg("order webhook: customer order stats updated")
}

func (h *WebhookHandler) handleCustomerDelete(c *gin.Context, body []byte, merchant *models.Merchant) {
	var payload struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		h.log.Error().Err(err).Msg("parse customer delete payload")
		return
	}

	if err := h.customerCacheRepo.DeleteByShopifyID(c.Request.Context(), merchant.ID, payload.ID); err != nil {
		h.log.Error().Err(err).Int64("shopify_id", payload.ID).Msg("customer cache delete")
	}
}

// handleDataRequest handles the GDPR customers/data_request webhook.
// Shopify requires a response within 30 days. Our app only caches operational
// data sourced from Shopify itself (email, name, phone, address) — we
// acknowledge the request and log it. No additional export is needed because
// the authoritative copy of all PII remains in Shopify.
func (h *WebhookHandler) handleDataRequest(c *gin.Context, body []byte, shop string) {
	var payload struct {
		Customer struct {
			ID    int64  `json:"id"`
			Email string `json:"email"`
		} `json:"customer"`
		DataRequest struct {
			ID int64 `json:"id"`
		} `json:"data_request"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		h.log.Error().Err(err).Str("shop", shop).Msg("gdpr: parse data_request payload")
		return
	}
	h.log.Info().
		Str("shop", shop).
		Int64("customer_id", payload.Customer.ID).
		Int64("request_id", payload.DataRequest.ID).
		Msg("gdpr: data_request received — app holds only Shopify-sourced operational cache")
}

// handleCustomerRedact handles the GDPR customers/redact webhook.
// Deletes the customer's cached data from our database within the required
// 30-day window.
func (h *WebhookHandler) handleCustomerRedact(c *gin.Context, body []byte, shop string) {
	var payload struct {
		Customer struct {
			ID int64 `json:"id"`
		} `json:"customer"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		h.log.Error().Err(err).Str("shop", shop).Msg("gdpr: parse customers/redact payload")
		return
	}

	merchant, err := h.merchantRepo.FindByDomain(c.Request.Context(), shop)
	if err != nil {
		// Merchant already removed — nothing to delete.
		h.log.Debug().Str("shop", shop).Msg("gdpr: customers/redact — merchant not found, skipping")
		return
	}

	if err := h.customerCacheRepo.DeleteByShopifyID(c.Request.Context(), merchant.ID, payload.Customer.ID); err != nil {
		h.log.Error().Err(err).Int64("shopify_id", payload.Customer.ID).Msg("gdpr: customer redact delete failed")
		return
	}
	h.log.Info().Str("shop", shop).Int64("shopify_id", payload.Customer.ID).Msg("gdpr: customer data redacted")
}

// handleShopRedact handles the GDPR shop/redact webhook.
// Shopify sends this 48 days after app uninstall. Deletes all remaining
// merchant data (cascades to customer_cache, duplicate_groups, etc.).
func (h *WebhookHandler) handleShopRedact(c *gin.Context, body []byte, shop string) {
	merchant, err := h.merchantRepo.FindByDomain(c.Request.Context(), shop)
	if err != nil {
		// Merchant already deleted (e.g., by app/uninstalled handler). Nothing to do.
		h.log.Debug().Str("shop", shop).Msg("gdpr: shop/redact — merchant not found, already clean")
		return
	}

	if err := h.merchantRepo.Delete(c.Request.Context(), merchant.ID); err != nil {
		h.log.Error().Err(err).Str("shop", shop).Msg("gdpr: shop/redact delete failed")
		return
	}
	h.log.Info().Str("shop", shop).Msg("gdpr: all merchant data redacted")
}

// handleAppUninstalled handles the app/uninstalled webhook.
// Deletes the merchant and all their data immediately upon uninstall.
// shop/redact will arrive 48 days later as a final cleanup sweep.
func (h *WebhookHandler) handleAppUninstalled(c *gin.Context, merchant *models.Merchant) {
	if err := h.merchantRepo.Delete(c.Request.Context(), merchant.ID); err != nil {
		h.log.Error().Err(err).Str("shop", merchant.ShopDomain).Msg("app/uninstalled: merchant delete failed")
		return
	}
	h.log.Info().Str("shop", merchant.ShopDomain).Msg("app/uninstalled: merchant data deleted")
}
