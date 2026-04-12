package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

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

type WebhookHandler struct {
	shopifySecret    string
	merchantRepo     repository.MerchantRepository
	customerCacheRepo repository.CustomerCacheRepository
	jobDispatcher    JobDispatcher
	log              zerolog.Logger
}

func NewWebhookHandler(
	shopifySecret string,
	merchantRepo repository.MerchantRepository,
	customerCacheRepo repository.CustomerCacheRepository,
	jobDispatcher JobDispatcher,
	log zerolog.Logger,
) *WebhookHandler {
	return &WebhookHandler{
		shopifySecret:    shopifySecret,
		merchantRepo:     merchantRepo,
		customerCacheRepo: customerCacheRepo,
		jobDispatcher:    jobDispatcher,
		log:              log,
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

	shop := c.GetHeader("X-Shopify-Shop-Domain")
	topic := c.GetHeader("X-Shopify-Topic")

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
	// Build address JSON for storage
	addrJSON, _ := json.Marshal(payload.Addresses)
	tags := strings.Split(payload.Tags, ",")
	cleanTags := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			cleanTags = append(cleanTags, t)
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
	}

	if err := h.customerCacheRepo.Upsert(c.Request.Context(), customer); err != nil {
		h.log.Error().Err(err).Int64("shopify_id", payload.ID).Msg("customer cache upsert")
		return
	}

	// Dispatch a duplicate detection job (debounced — only if none already pending)
	if h.jobDispatcher != nil {
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
