package shopify

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"merger/backend/internal/models"
)

// ShopifyCustomer represents the Shopify customer resource.
type ShopifyCustomer struct {
	ID             int64                `json:"id"`
	Email          string               `json:"email"`
	FirstName      string               `json:"first_name"`
	LastName       string               `json:"last_name"`
	Phone          string               `json:"phone"`
	Tags           string               `json:"tags"`
	Note           string               `json:"note"`
	State          string               `json:"state"`
	VerifiedEmail  bool                 `json:"verified_email"`
	Addresses      []Address            `json:"addresses"`
	OrdersCount    int                  `json:"orders_count"`
	TotalSpent     string               `json:"total_spent"`
	CreatedAt      string               `json:"created_at"`
	UpdatedAt      string               `json:"updated_at"`
	LastOrderAt    *time.Time
	OrderAddresses []models.OrderAddress
	OrderNames     []string
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

// FetchAll retrieves all customers using the GraphQL Admin API with cursor-based
// pagination. GraphQL does not require the protected customer data REST approval.
func (s *CustomerService) FetchAll(ctx context.Context) ([]ShopifyCustomer, error) {
	const query = `
		query fetchCustomers($first: Int!, $after: String) {
			customers(first: $first, after: $after) {
				edges {
					node {
						legacyResourceId
						firstName
						lastName
						email
						phone
						tags
						note
						state
						verifiedEmail
						createdAt
						numberOfOrders
						amountSpent { amount }
					defaultAddress {
						address1
						city
						province
						zip
						country
					}
					orders(first: 5, sortKey: CREATED_AT, reverse: true) {
						edges {
							node {
								createdAt
								name
								shippingAddress { address1 city zip countryCode }
								billingAddress  { address1 city zip countryCode }
							}
						}
					}
				}
				}
				pageInfo {
					hasNextPage
					endCursor
				}
			}
		}`

	type gqlAddress struct {
		Address1 string `json:"address1"`
		City     string `json:"city"`
		Province string `json:"province"`
		Zip      string `json:"zip"`
		Country  string `json:"country"`
	}
	type gqlOrderAddr struct {
		Address1    string `json:"address1"`
		City        string `json:"city"`
		Zip         string `json:"zip"`
		CountryCode string `json:"countryCode"`
	}
	type gqlOrderNode struct {
		CreatedAt       string        `json:"createdAt"`
		Name            string        `json:"name"`
		ShippingAddress *gqlOrderAddr `json:"shippingAddress"`
		BillingAddress  *gqlOrderAddr `json:"billingAddress"`
	}
	type gqlCustomer struct {
		LegacyResourceID string     `json:"legacyResourceId"`
		FirstName        string     `json:"firstName"`
		LastName         string     `json:"lastName"`
		Email            string     `json:"email"`
		Phone            string     `json:"phone"`
		Tags             []string   `json:"tags"`
		Note             string     `json:"note"`
		State            string     `json:"state"`
		VerifiedEmail    bool       `json:"verifiedEmail"`
		CreatedAt        string     `json:"createdAt"`
		NumberOfOrders   string     `json:"numberOfOrders"`
		AmountSpent      struct {
			Amount string `json:"amount"`
		} `json:"amountSpent"`
		DefaultAddress *gqlAddress `json:"defaultAddress"`
		Orders         struct {
			Edges []struct {
				Node gqlOrderNode `json:"node"`
			} `json:"edges"`
		} `json:"orders"`
	}
	type gqlResponse struct {
		Customers struct {
			Edges []struct {
				Node gqlCustomer `json:"node"`
			} `json:"edges"`
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
		} `json:"customers"`
	}

	var all []ShopifyCustomer
	var cursor *string

	for {
		vars := map[string]interface{}{"first": 250}
		if cursor != nil {
			vars["after"] = *cursor
		}

		var resp gqlResponse
		if err := s.client.doGraphQL(ctx, query, vars, &resp); err != nil {
			return nil, fmt.Errorf("fetch customers page: %w", err)
		}

		for _, edge := range resp.Customers.Edges {
			n := edge.Node
			id, _ := strconv.ParseInt(n.LegacyResourceID, 10, 64)
			ordersCount, _ := strconv.Atoi(n.NumberOfOrders)

			sc := ShopifyCustomer{
				ID:            id,
				FirstName:     n.FirstName,
				LastName:      n.LastName,
				Email:         n.Email,
				Phone:         n.Phone,
				Tags:          strings.Join(n.Tags, ","),
				Note:          n.Note,
				State:         n.State,
				VerifiedEmail: n.VerifiedEmail,
				CreatedAt:     n.CreatedAt,
				OrdersCount:   ordersCount,
				TotalSpent:    n.AmountSpent.Amount,
			}
			if n.DefaultAddress != nil {
				sc.Addresses = []Address{{
					Address1: n.DefaultAddress.Address1,
					City:     n.DefaultAddress.City,
					Province: n.DefaultAddress.Province,
					Zip:      n.DefaultAddress.Zip,
					Country:  n.DefaultAddress.Country,
				}}
			}
			for _, oe := range n.Orders.Edges {
				o := oe.Node
				if t, err := time.Parse(time.RFC3339, o.CreatedAt); err == nil {
					if sc.LastOrderAt == nil || t.After(*sc.LastOrderAt) {
						sc.LastOrderAt = &t
					}
				}
				if o.Name != "" {
					sc.OrderNames = append(sc.OrderNames, o.Name)
				}
				for _, addr := range []*gqlOrderAddr{o.ShippingAddress, o.BillingAddress} {
					if addr != nil && addr.City != "" {
						sc.OrderAddresses = append(sc.OrderAddresses, models.OrderAddress{
							Street: addr.Address1, City: addr.City,
							Zip: addr.Zip, Country: addr.CountryCode,
						})
					}
				}
			}
			all = append(all, sc)
		}

		if !resp.Customers.PageInfo.HasNextPage {
			break
		}
		c := resp.Customers.PageInfo.EndCursor
		cursor = &c
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
// primaryGID's data (name, email, phone, address) is preserved on the surviving record
// via overrideFields regardless of which customer ID Shopify chooses to keep.
func (s *CustomerService) Merge(ctx context.Context, primaryGID, secondaryGID string) (*MergeResult, error) {
	mutation := `
		mutation customerMerge($customerOneId: ID!, $customerTwoId: ID!, $overrideFields: CustomerMergeOverrideFields) {
			customerMerge(customerOneId: $customerOneId, customerTwoId: $customerTwoId, overrideFields: $overrideFields) {
				resultingCustomerId
				job {
					id
					done
				}
				userErrors {
					code
					field
					message
				}
			}
		}`

	variables := map[string]interface{}{
		"customerOneId": primaryGID,
		"customerTwoId": secondaryGID,
		"overrideFields": map[string]interface{}{
			"customerIdOfFirstNameToKeep":       primaryGID,
			"customerIdOfLastNameToKeep":        primaryGID,
			"customerIdOfEmailToKeep":           primaryGID,
			"customerIdOfPhoneNumberToKeep":     primaryGID,
			"customerIdOfDefaultAddressToKeep":  primaryGID,
		},
	}

	var data struct {
		CustomerMerge struct {
			ResultingCustomerID string `json:"resultingCustomerId"`
			Job                 *struct {
				ID   string `json:"id"`
				Done bool   `json:"done"`
			} `json:"job"`
			UserErrors []struct {
				Code    string `json:"code"`
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
		CustomerID: data.CustomerMerge.ResultingCustomerID,
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
