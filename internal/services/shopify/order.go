package shopify

import (
	"context"
	"fmt"
	"net/http"
)

// ShopifyOrder represents a Shopify order (read-only — used for snapshots).
type ShopifyOrder struct {
	ID              int64   `json:"id"`
	Name            string  `json:"name"`
	CustomerID      int64   `json:"customer_id"`
	TotalPrice      string  `json:"total_price"`
	FinancialStatus string  `json:"financial_status"`
	FulfillmentStatus string `json:"fulfillment_status"`
	CreatedAt       string  `json:"created_at"`
}

type OrderService struct {
	client *Client
}

func NewOrderService(client *Client) *OrderService {
	return &OrderService{client: client}
}

type orderListResponse struct {
	Orders []ShopifyOrder `json:"orders"`
}

// FetchByCustomer retrieves all orders for a given customer ID.
// This is READ-ONLY — used for snapshot data capture before merges.
// Order reassignment is handled automatically by Shopify's customerMerge API.
func (s *OrderService) FetchByCustomer(ctx context.Context, customerID int64) ([]ShopifyOrder, error) {
	var all []ShopifyOrder
	path := fmt.Sprintf("/orders.json?customer_id=%d&status=any&limit=250", customerID)

	for path != "" {
		var resp orderListResponse
		if err := s.client.doREST(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("fetch orders for customer %d: %w", customerID, err)
		}
		all = append(all, resp.Orders...)
		if len(resp.Orders) < 250 {
			break
		}
		lastID := resp.Orders[len(resp.Orders)-1].ID
		path = fmt.Sprintf("/orders.json?customer_id=%d&status=any&limit=250&since_id=%d", customerID, lastID)
	}

	return all, nil
}
