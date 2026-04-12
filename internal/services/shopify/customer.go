package shopify

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ShopifyCustomer represents the Shopify customer resource.
type ShopifyCustomer struct {
	ID            int64    `json:"id"`
	Email         string   `json:"email"`
	FirstName     string   `json:"first_name"`
	LastName      string   `json:"last_name"`
	Phone         string   `json:"phone"`
	Tags          string   `json:"tags"`
	Note          string   `json:"note"`
	Addresses     []Address `json:"addresses"`
	OrdersCount   int      `json:"orders_count"`
	TotalSpent    string   `json:"total_spent"`
	State         string   `json:"state"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
}

type Address struct {
	ID        int64  `json:"id"`
	Address1  string `json:"address1"`
	Address2  string `json:"address2"`
	City      string `json:"city"`
	Province  string `json:"province"`
	Country   string `json:"country"`
	Zip       string `json:"zip"`
	Phone     string `json:"phone"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type CustomerService struct {
	client *Client
}

func NewCustomerService(client *Client) *CustomerService {
	return &CustomerService{client: client}
}

type customerListResponse struct {
	Customers []ShopifyCustomer `json:"customers"`
}

type customerResponse struct {
	Customer ShopifyCustomer `json:"customer"`
}

// FetchAll retrieves all customers for the shop using cursor-based pagination.
func (s *CustomerService) FetchAll(ctx context.Context) ([]ShopifyCustomer, error) {
	var all []ShopifyCustomer
	path := "/customers.json?limit=250&fields=id,email,first_name,last_name,phone,tags,note,addresses,orders_count,total_spent,state,created_at,updated_at"

	for path != "" {
		var resp customerListResponse
		if err := s.client.doREST(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, fmt.Errorf("fetch customers page: %w", err)
		}
		all = append(all, resp.Customers...)

		// Cursor-based pagination uses Link headers; for simplicity with doREST
		// we use since_id pagination (simpler and sufficient for bulk sync)
		if len(resp.Customers) < 250 {
			break
		}
		lastID := resp.Customers[len(resp.Customers)-1].ID
		path = fmt.Sprintf("/customers.json?limit=250&since_id=%d&fields=id,email,first_name,last_name,phone,tags,note,addresses,orders_count,total_spent,state,created_at,updated_at", lastID)
	}

	return all, nil
}

// FetchByID retrieves a single customer by Shopify ID.
func (s *CustomerService) FetchByID(ctx context.Context, id int64) (*ShopifyCustomer, error) {
	var resp customerResponse
	path := fmt.Sprintf("/customers/%d.json", id)
	if err := s.client.doREST(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("fetch customer %d: %w", id, err)
	}
	return &resp.Customer, nil
}

// MergeResult holds the result of a customerMerge GraphQL mutation.
type MergeResult struct {
	CustomerID string
	UserErrors []struct {
		Field   string
		Message string
	}
}

// Merge calls Shopify's customerMerge GraphQL mutation.
// This is the ONLY merge mechanism — no order reassignment needed as Shopify handles it.
func (s *CustomerService) Merge(ctx context.Context, primaryGID, secondaryGID string) (*MergeResult, error) {
	mutation := `
		mutation customerMerge($customerOneId: ID!, $customerTwoId: ID!) {
			customerMerge(customerOneId: $customerOneId, customerTwoId: $customerTwoId) {
				resultingCustomer {
					id
				}
				userErrors {
					field
					message
				}
			}
		}`

	variables := map[string]interface{}{
		"customerOneId": primaryGID,
		"customerTwoId": secondaryGID,
	}

	var data struct {
		CustomerMerge struct {
			ResultingCustomer struct {
				ID string `json:"id"`
			} `json:"resultingCustomer"`
			UserErrors []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"userErrors"`
		} `json:"customerMerge"`
	}

	if err := s.client.doGraphQL(ctx, mutation, variables, &data); err != nil {
		return nil, fmt.Errorf("customerMerge GraphQL: %w", err)
	}

	if len(data.CustomerMerge.UserErrors) > 0 {
		errs := data.CustomerMerge.UserErrors
		return nil, fmt.Errorf("customerMerge user error: [%s] %s",
			errs[0].Field, errs[0].Message)
	}

	return &MergeResult{
		CustomerID: data.CustomerMerge.ResultingCustomer.ID,
	}, nil
}

// ShopifyIDToGID converts a numeric Shopify customer ID to a GraphQL Global ID.
func ShopifyIDToGID(id int64) string {
	return fmt.Sprintf("gid://shopify/Customer/%d", id)
}

// GIDToShopifyID extracts the numeric ID from a Shopify GraphQL Global ID.
// e.g. "gid://shopify/Customer/12345" → 12345
func GIDToShopifyID(gid string) (int64, error) {
	parts := strings.Split(gid, "/")
	if len(parts) == 0 {
		return 0, fmt.Errorf("invalid GID: %q", gid)
	}
	id, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse GID %q: %w", gid, err)
	}
	return id, nil
}
