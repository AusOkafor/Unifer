package shopify

import (
	"context"
	"fmt"
	"strings"
)

// BillingService handles Shopify app subscription management.
type BillingService struct {
	client *Client
}

func NewBillingService(client *Client) *BillingService {
	return &BillingService{client: client}
}

// SubscriptionResult holds the outcome of appSubscriptionCreate.
type SubscriptionResult struct {
	SubscriptionID  string // GID: gid://shopify/AppSubscription/12345
	ConfirmationURL string
}

// CreateSubscription calls appSubscriptionCreate and returns the URL where
// the merchant must be redirected to approve the charge.
func (s *BillingService) CreateSubscription(
	ctx context.Context,
	name string,
	priceUSD float64,
	returnURL string,
	test bool,
) (*SubscriptionResult, error) {
	const mutation = `
mutation appSubscriptionCreate(
  $name: String!
  $returnUrl: URL!
  $lineItems: [AppSubscriptionLineItemInput!]!
  $test: Boolean
) {
  appSubscriptionCreate(
    name: $name
    returnUrl: $returnUrl
    lineItems: $lineItems
    test: $test
  ) {
    appSubscription {
      id
      status
    }
    confirmationUrl
    userErrors {
      field
      message
    }
  }
}`

	variables := map[string]interface{}{
		"name":      name,
		"returnUrl": returnURL,
		"test":      test,
		"lineItems": []map[string]interface{}{
			{
				"plan": map[string]interface{}{
					"appRecurringPricingDetails": map[string]interface{}{
						"price": map[string]interface{}{
							"amount":       priceUSD,
							"currencyCode": "USD",
						},
						"interval": "EVERY_30_DAYS",
					},
				},
			},
		},
	}

	var result struct {
		AppSubscriptionCreate struct {
			AppSubscription *struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"appSubscription"`
			ConfirmationURL string `json:"confirmationUrl"`
			UserErrors      []struct {
				Field   []string `json:"field"`
				Message string   `json:"message"`
			} `json:"userErrors"`
		} `json:"appSubscriptionCreate"`
	}

	if err := s.client.doGraphQL(ctx, mutation, variables, &result); err != nil {
		return nil, fmt.Errorf("appSubscriptionCreate: %w", err)
	}

	if len(result.AppSubscriptionCreate.UserErrors) > 0 {
		msgs := make([]string, len(result.AppSubscriptionCreate.UserErrors))
		for i, e := range result.AppSubscriptionCreate.UserErrors {
			msgs[i] = e.Message
		}
		return nil, fmt.Errorf("appSubscriptionCreate user errors: %s", strings.Join(msgs, "; "))
	}

	sub := result.AppSubscriptionCreate.AppSubscription
	if sub == nil {
		return nil, fmt.Errorf("appSubscriptionCreate: no subscription returned")
	}

	return &SubscriptionResult{
		SubscriptionID:  sub.ID,
		ConfirmationURL: result.AppSubscriptionCreate.ConfirmationURL,
	}, nil
}

// GetSubscriptionStatus fetches the current status of a subscription by GID.
// Returns the status string: ACTIVE, PENDING, DECLINED, EXPIRED, FROZEN, CANCELLED.
func (s *BillingService) GetSubscriptionStatus(ctx context.Context, subscriptionGID string) (string, error) {
	const query = `
query appSubscription($id: ID!) {
  node(id: $id) {
    ... on AppSubscription {
      id
      status
    }
  }
}`

	var result struct {
		Node *struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"node"`
	}

	if err := s.client.doGraphQL(ctx, query, map[string]interface{}{"id": subscriptionGID}, &result); err != nil {
		return "", fmt.Errorf("get subscription status: %w", err)
	}
	if result.Node == nil {
		return "", fmt.Errorf("subscription %s not found", subscriptionGID)
	}
	return result.Node.Status, nil
}
