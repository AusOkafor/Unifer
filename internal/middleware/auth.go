package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
)

const merchantContextKey = "merchant"

// AuthRequired validates the JWT from the session cookie or Authorization header,
// loads the merchant, and stores it in the gin context.
func AuthRequired(jwtSecret string, merchantRepo repository.MerchantRepository) gin.HandlerFunc {
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
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		merchantIDStr, ok := claims["merchant_id"].(string)
		if !ok || merchantIDStr == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token claims"})
			return
		}

		merchantID, err := uuid.Parse(merchantIDStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid merchant id"})
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
	if cookie, err := c.Cookie("session"); err == nil && cookie != "" {
		return cookie
	}
	auth := c.GetHeader("Authorization")
	if after, ok := strings.CutPrefix(auth, "Bearer "); ok {
		return after
	}
	return ""
}
