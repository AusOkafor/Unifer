package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
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
	jwtSecret     []byte
	frontendURL   string
	log           zerolog.Logger
}

func NewAuthHandler(
	oauthCfg *shopifyauth.OAuthConfig,
	merchantRepo repository.MerchantRepository,
	encryptor *utils.Encryptor,
	jwtSecret string,
	frontendURL string,
	log zerolog.Logger,
) *AuthHandler {
	return &AuthHandler{
		oauthCfg:    oauthCfg,
		merchantRepo: merchantRepo,
		encryptor:   encryptor,
		jwtSecret:   []byte(jwtSecret),
		frontendURL: frontendURL,
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
	// Uses the plaintext token from the just-completed exchange.
	go func() {
		client := shopifysvc.NewClient(shop, token, h.log)
		whSvc := shopifysvc.NewWebhookService(client)
		if err := whSvc.RegisterAll(context.Background(), h.oauthCfg.AppURL); err != nil {
			h.log.Warn().Err(err).Str("shop", shop).Msg("webhook registration failed")
		} else {
			h.log.Info().Str("shop", shop).Msg("shopify webhooks registered")
		}
	}()

	// Issue JWT session cookie
	sessionToken, err := h.issueJWT(merchant.ID, shop)
	if err != nil {
		h.log.Error().Err(err).Msg("jwt issue failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	// SameSite=None + Secure so the session cookie is sent on cross-origin
	// requests from the Vercel frontend to the Render backend.
	c.SetSameSite(http.SameSiteNoneMode)
	c.SetCookie("session", sessionToken, int(7*24*time.Hour/time.Second), "/", "", true, true)

	// Send the shop + JWT to the frontend so it can persist the token in
	// localStorage before redirecting into the Shopify admin iframe.
	// The frontend stores the token, then uses window.location.replace to
	// open the app embedded — keeping the token available for API calls.
	redirectURL := fmt.Sprintf("%s?shop=%s&token=%s",
		strings.TrimRight(h.frontendURL, "/"), shop, sessionToken)
	c.Redirect(http.StatusTemporaryRedirect, redirectURL)
}

func (h *AuthHandler) issueJWT(merchantID uuid.UUID, shop string) (string, error) {
	claims := jwt.MapClaims{
		"merchant_id": merchantID.String(),
		"shop":        shop,
		"exp":         time.Now().Add(7 * 24 * time.Hour).Unix(),
		"iat":         time.Now().Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(h.jwtSecret)
}

func generateState() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
