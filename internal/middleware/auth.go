package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
)

const merchantContextKey = "merchant"

// AuthRequired validates a Shopify-issued session token (App Bridge JWT).
//
// Shopify session tokens are HS256 JWTs signed with the app's API secret.
// The `dest` claim contains "https://{shop}" — we strip the scheme to get
// the shop domain and look up the merchant.
//
// Tokens are expected in the Authorization: Bearer header (sent by the
// frontend's apiFetch wrapper via App Bridge getSessionToken).
func AuthRequired(shopifyAPISecret, shopifyAPIKey string, merchantRepo repository.MerchantRepository) gin.HandlerFunc {
	secret := []byte(shopifyAPISecret)
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
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		// Validate audience matches our API key (prevents token reuse across apps).
		if shopifyAPIKey != "" {
			aud, _ := claims.GetAudience()
			valid := false
			for _, a := range aud {
				if a == shopifyAPIKey {
					valid = true
					break
				}
			}
			if !valid {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token audience"})
				return
			}
		}

		// `dest` is "https://{shop}" — strip the scheme to get the domain.
		dest, _ := claims["dest"].(string)
		shopDomain := strings.TrimPrefix(dest, "https://")
		shopDomain = strings.TrimPrefix(shopDomain, "http://")
		if shopDomain == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token claims"})
			return
		}

		merchant, err := merchantRepo.FindByDomain(c.Request.Context(), shopDomain)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "merchant not found"})
			return
		}

		c.Set(merchantContextKey, merchant)
		c.Next()
	}
}

// GetMerchant retrieves the authenticated merchant from the gin context.
func GetMerchant(c *gin.Context) *models.Merchant {
	val, exists := c.Get(merchantContextKey)
	if !exists {
		return nil
	}
	m, _ := val.(*models.Merchant)
	return m
}

func extractToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if after, ok := strings.CutPrefix(auth, "Bearer "); ok && after != "" {
		return after
	}
	// Fallback: session cookie (used in dev when App Bridge is unavailable).
	if cookie, err := c.Cookie("session"); err == nil && cookie != "" {
		return cookie
	}
	return ""
}
