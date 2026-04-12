package shopify

import (
	"context"
	"encoding/json"
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

type webhookListResponse struct {
	Webhooks []struct {
		ID      int64  `json:"id"`
		Topic   string `json:"topic"`
		Address string `json:"address"`
	} `json:"webhooks"`
}

// RequiredTopics are the Shopify webhook topics the app must be subscribed to.
var RequiredTopics = []string{
	"customers/create",
	"customers/update",
	"customers/delete",
}

// RegisterAll subscribes to all required customer webhook topics for the shop.
// It checks existing webhooks and only registers missing ones.
func (s *WebhookService) RegisterAll(ctx context.Context, appURL string) error {
	callbackURL := appURL + "/api/webhooks/shopify"

	// List existing webhooks
	var existing webhookListResponse
	if err := s.client.doREST(ctx, http.MethodGet, "/webhooks.json", nil, &existing); err != nil {
		return fmt.Errorf("list webhooks: %w", err)
	}

	registered := make(map[string]bool)
	for _, wh := range existing.Webhooks {
		if wh.Address == callbackURL {
			registered[wh.Topic] = true
		}
	}

	for _, topic := range RequiredTopics {
		if registered[topic] {
			continue
		}
		req := webhookCreateRequest{
			Webhook: webhookTopic{
				Topic:   topic,
				Address: callbackURL,
				Format:  "json",
			},
		}
		if err := s.client.doREST(ctx, http.MethodPost, "/webhooks.json", req, nil); err != nil {
			return fmt.Errorf("register webhook %s: %w", topic, err)
		}
	}

	return nil
}

// CustomerWebhookPayload is the shape of customer create/update webhook payloads.
type CustomerWebhookPayload struct {
	ID          int64     `json:"id"`
	Email       string    `json:"email"`
	FirstName   string    `json:"first_name"`
	LastName    string    `json:"last_name"`
	Phone       string    `json:"phone"`
	Tags        string    `json:"tags"`
	Note        string    `json:"note"`
	Addresses   []Address `json:"addresses"`
	OrdersCount int       `json:"orders_count"`
	TotalSpent  string    `json:"total_spent"`
}

// ParseCustomerPayload decodes a raw webhook body into a CustomerWebhookPayload.
func ParseCustomerPayload(body []byte) (*CustomerWebhookPayload, error) {
	var p CustomerWebhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parse customer webhook: %w", err)
	}
	return &p, nil
}
