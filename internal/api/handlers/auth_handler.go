package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	shopifysvc "merger/backend/internal/services/shopify"
	"merger/backend/internal/utils"
	"merger/backend/pkg/shopifyauth"
)

type AuthHandler struct {
	oauthCfg     *shopifyauth.OAuthConfig
	merchantRepo  repository.MerchantRepository
	encryptor     *utils.Encryptor
	apiKey        string
	log           zerolog.Logger
}

func NewAuthHandler(
	oauthCfg *shopifyauth.OAuthConfig,
	merchantRepo repository.MerchantRepository,
	encryptor *utils.Encryptor,
	apiKey string,
	log zerolog.Logger,
) *AuthHandler {
	return &AuthHandler{
		oauthCfg:    oauthCfg,
		merchantRepo: merchantRepo,
		encryptor:   encryptor,
		apiKey:      apiKey,
		log:         log,
	}
}

// HandleInstall redirects the merchant to Shopify's OAuth install page.
func (h *AuthHandler) HandleInstall(c *gin.Context) {
	shop := c.Query("shop")
	if shop == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "shop parameter required"})
		return
	}

	state := generateState()
	// SameSite=None + Secure required for cross-origin cookie to survive the
	// Shopify redirect back to the callback URL.
	c.SetSameSite(http.SameSiteNoneMode)
	c.SetCookie("oauth_state", state, 300, "/", "", true, true)

	installURL := h.oauthCfg.GenerateInstallURL(shop, state)
	c.Redirect(http.StatusTemporaryRedirect, installURL)
}

// HandleCallback handles the OAuth callback from Shopify.
// After persisting the access token it redirects the merchant into the
// Shopify admin so the app loads embedded — App Bridge then provides
// session tokens for all subsequent API calls.
func (h *AuthHandler) HandleCallback(c *gin.Context) {
	// Verify HMAC
	if !shopifyauth.ValidateHMAC(c.Request.URL.Query(), h.oauthCfg.APISecret) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid HMAC"})
		return
	}

	// Verify state
	state := c.Query("state")
	cookieState, err := c.Cookie("oauth_state")
	if err != nil || state == "" || state != cookieState {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid state"})
		return
	}
	c.SetCookie("oauth_state", "", -1, "/", "", false, true) // clear

	shop := c.Query("shop")
	code := c.Query("code")

	// Exchange code for access token
	token, err := h.oauthCfg.ExchangeCode(c.Request.Context(), shop, code)
	if err != nil {
		h.log.Error().Err(err).Str("shop", shop).Msg("token exchange failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token exchange failed"})
		return
	}

	// Encrypt and persist
	encToken, err := h.encryptor.Encrypt(token)
	if err != nil {
		h.log.Error().Err(err).Msg("token encryption failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	merchant := &models.Merchant{
		ShopDomain:     shop,
		AccessTokenEnc: encToken,
	}
	if err := h.merchantRepo.Create(c.Request.Context(), merchant); err != nil {
		h.log.Error().Err(err).Str("shop", shop).Msg("merchant upsert failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// Register Shopify webhooks for this merchant (best-effort, non-blocking).
	go func() {
		client := shopifysvc.NewClient(shop, token, h.log)
		whSvc := shopifysvc.NewWebhookService(client)
		if err := whSvc.RegisterAll(context.Background(), h.oauthCfg.AppURL); err != nil {
			h.log.Warn().Err(err).Str("shop", shop).Msg("webhook registration failed")
		} else {
			h.log.Info().Str("shop", shop).Msg("shopify webhooks registered")
		}
	}()

	// Redirect into the Shopify admin so the app loads embedded.
	// App Bridge will supply session tokens automatically from here.
	adminURL := fmt.Sprintf("https://%s/admin/apps/%s", shop, h.apiKey)
	c.Redirect(http.StatusTemporaryRedirect, adminURL)
}

func generateState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
