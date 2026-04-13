package shopify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type WebhookService struct {
	client *Client
}

func NewWebhookService(client *Client) *WebhookService {
	return &WebhookService{client: client}
}

type webhookTopic struct {
	Topic   string `json:"topic"`
	Address string `json:"address"`
	Format  string `json:"format"`
}

type webhookCreateRequest struct {
	Webhook webhookTopic `json:"webhook"`
}

type existingWebhook struct {
	ID      int64
	Address string
}

type webhookListResponse struct {
	Webhooks []struct {
		ID      int64  `json:"id"`
		Topic   string `json:"topic"`
		Address string `json:"address"`
	} `json:"webhooks"`
}

// RequiredTopics are the Shopify webhook topics the app subscribes to.
// GDPR topics (customers/data_request, customers/redact, shop/redact) are
// registered separately in the Partner Dashboard and are handled by the same
// endpoint — they cannot be registered via the REST API.
//
// Note: customers/merge is not a real Shopify webhook topic (as of 2025-01);
// Shopify does not broadcast merge events via webhooks.
var RequiredTopics = []string{
	"customers/create",
	"customers/update",
	"customers/delete",
	"orders/create",
	"orders/updated",
	"app/uninstalled",
}

// RegisterAll subscribes to all required webhook topics for the shop.
//
// For each required topic it:
//  1. Skips if already registered to the correct callback URL.
//  2. Deletes any existing webhook for the topic pointing to a stale URL
//     (e.g. after an app URL change), then registers the new one.
//  3. Registers if the topic is completely absent.
//
// Errors from individual registrations are collected and returned together so
// a single failure does not block the remaining topics.
func (s *WebhookService) RegisterAll(ctx context.Context, appURL string) error {
	callbackURL := appURL + "/api/webhooks/shopify"

	// List existing webhooks for this shop.
	var list webhookListResponse
	if err := s.client.doREST(ctx, http.MethodGet, "/webhooks.json", nil, &list); err != nil {
		return fmt.Errorf("list webhooks: %w", err)
	}

	// Build a topic → existing webhook map.
	byTopic := make(map[string]existingWebhook, len(list.Webhooks))
	for _, wh := range list.Webhooks {
		byTopic[wh.Topic] = existingWebhook{ID: wh.ID, Address: wh.Address}
	}

	var errs []error

	for _, topic := range RequiredTopics {
		existing, exists := byTopic[topic]

		if exists && existing.Address == callbackURL {
			continue // already registered to the right URL — nothing to do
		}

		// Stale webhook: topic exists but points to a different (old) URL.
		// Delete it first so we can register a fresh one.
		if exists && existing.ID != 0 {
			deletePath := fmt.Sprintf("/webhooks/%d.json", existing.ID)
			if err := s.client.doREST(ctx, http.MethodDelete, deletePath, nil, nil); err != nil {
				// Non-fatal: log via collected errors, still attempt registration.
				errs = append(errs, fmt.Errorf("delete stale webhook %s (id=%d): %w", topic, existing.ID, err))
			}
		}

		req := webhookCreateRequest{
			Webhook: webhookTopic{
				Topic:   topic,
				Address: callbackURL,
				Format:  "json",
			},
		}
		if err := s.client.doREST(ctx, http.MethodPost, "/webhooks.json", req, nil); err != nil {
			errs = append(errs, fmt.Errorf("register webhook %s: %w", topic, err))
		}
	}

	return errors.Join(errs...)
}

// OrderWebhookPayload is the shape of orders/create and orders/updated webhooks.
// We only extract what we need: order ID, customer identity, and updated order stats
// so the customer cache can be kept in sync without a full customer fetch.
type OrderWebhookPayload struct {
	ID       int64 `json:"id"`
	Customer struct {
		ID          int64  `json:"id"`
		OrdersCount int    `json:"orders_count"`
		TotalSpent  string `json:"total_spent"`
	} `json:"customer"`
}

// ParseOrderPayload decodes a raw webhook body into an OrderWebhookPayload.
func ParseOrderPayload(body []byte) (*OrderWebhookPayload, error) {
	var p OrderWebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parse order webhook: %w", err)
	}
	return &p, nil
}

// CustomerWebhookPayload is the shape of customer create/update webhook payloads.
type CustomerWebhookPayload struct {
	ID            int64     `json:"id"`
	Email         string    `json:"email"`
	FirstName     string    `json:"first_name"`
	LastName      string    `json:"last_name"`
	Phone         string    `json:"phone"`
	Tags          string    `json:"tags"`
	Note          string    `json:"note"`
	State         string    `json:"state"`
	VerifiedEmail bool      `json:"verified_email"`
	CreatedAt     string    `json:"created_at"`
	Addresses     []Address `json:"addresses"`
	OrdersCount   int       `json:"orders_count"`
	TotalSpent    string    `json:"total_spent"`
}

// ParseCustomerPayload decodes a raw webhook body into a CustomerWebhookPayload.
func ParseCustomerPayload(body []byte) (*CustomerWebhookPayload, error) {
	var p CustomerWebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parse customer webhook: %w", err)
	}
	return &p, nil
}
