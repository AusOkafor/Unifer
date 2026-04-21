package middleware

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/repository"
)

const wpAccessTokenTTL = 15 * time.Minute
const wpRefreshTokenTTL = 7 * 24 * time.Hour

// AuthRequiredWordPress validates a WordPress merchant access token (HS256 JWT
// signed with WP_JWT_SECRET). The `sub` claim is the merchant UUID; the
// `type` claim must equal "access". Sets the same "merchant" context key as
// Shopify auth so handlers can call GetMerchant() uniformly.
func AuthRequiredWordPress(jwtSecret string, merchantRepo repository.MerchantRepository, log zerolog.Logger) gin.HandlerFunc {
	secret := []byte(jwtSecret)
	return func(c *gin.Context) {
		tokenStr := extractToken(c)
		if tokenStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return secret, nil
		})
		if err != nil {
			log.Warn().Err(err).Msg("wp auth: JWT parse failed")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		if claims["type"] != "access" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token type"})
			return
		}

		sub, _ := claims["sub"].(string)
		merchantID, err := uuid.Parse(sub)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token subject"})
			return
		}

		merchant, err := merchantRepo.FindByID(c.Request.Context(), merchantID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "merchant not found"})
			return
		}

		c.Set(merchantContextKey, merchant)
		c.Next()
	}
}

// IssueAccessToken creates a 15-minute HS256 JWT for a WordPress merchant.
func IssueAccessToken(jwtSecret string, merchantID uuid.UUID) (string, error) {
	claims := jwt.MapClaims{
		"sub":  merchantID.String(),
		"type": "access",
		"exp":  time.Now().Add(wpAccessTokenTTL).Unix(),
		"iat":  time.Now().Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(jwtSecret))
	if err != nil {
		return "", fmt.Errorf("issue access token: %w", err)
	}
	return signed, nil
}

// IssueRefreshToken generates a 32-byte random token and returns both the raw
// token (to send to the client once) and its SHA-256 hex hash (to store in DB).
func IssueRefreshToken() (raw, hash string, expiresAt time.Time, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", time.Time{}, fmt.Errorf("issue refresh token: %w", err)
	}
	raw = hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	hash = hex.EncodeToString(sum[:])
	expiresAt = time.Now().Add(wpRefreshTokenTTL)
	return raw, hash, expiresAt, nil
}

// HashRefreshToken returns the SHA-256 hex hash of a raw refresh token string.
// Used when validating an incoming refresh request.
func HashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
