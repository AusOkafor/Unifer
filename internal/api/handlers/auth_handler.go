package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
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
	// Store state in a short-lived cookie to verify on callback
	c.SetCookie("oauth_state", state, 300, "/", "", false, true)

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

	// Issue JWT session cookie
	sessionToken, err := h.issueJWT(merchant.ID, shop)
	if err != nil {
		h.log.Error().Err(err).Msg("jwt issue failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.SetCookie("session", sessionToken, int(7*24*time.Hour/time.Second), "/", "", false, true)
	c.Redirect(http.StatusTemporaryRedirect, h.frontendURL)
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
