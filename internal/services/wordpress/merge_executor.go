package wordpress

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"merger/backend/internal/repository"
	mergesvc "merger/backend/internal/services/merge"
)

// Executor implements merge.MergeExecutor for WooCommerce merchants.
// It resolves customer IDs (including negative guest IDs) to full customer
// references — with emails — before calling the plugin's merge endpoint.
// The plugin uses emails to locate guest order streams that have no WP account.
type Executor struct {
	client            *Client
	customerCacheRepo repository.CustomerCacheRepository
	merchantRepo      repository.MerchantRepository
	siteURL           string // WP site URL — used to look up merchantID at execute time
	log               zerolog.Logger
}

func NewExecutor(
	client *Client,
	customerCacheRepo repository.CustomerCacheRepository,
	merchantRepo repository.MerchantRepository,
	siteURL string,
	log zerolog.Logger,
) *Executor {
	return &Executor{
		client:            client,
		customerCacheRepo: customerCacheRepo,
		merchantRepo:      merchantRepo,
		siteURL:           siteURL,
		log:               log,
	}
}

func (e *Executor) Execute(ctx context.Context, primaryID int64, secondaryIDs []int64, fieldOverrides map[string]string) (*mergesvc.ExecuteResult, error) {
	// Resolve the merchant so we can scope the cache lookup.
	merchant, err := e.merchantRepo.FindByDomain(ctx, e.siteURL)
	if err != nil {
		return nil, fmt.Errorf("wp executor: merchant lookup: %w", err)
	}

	// Fetch all involved customer records in one query to get their emails.
	allIDs := make([]int64, 0, 1+len(secondaryIDs))
	allIDs = append(allIDs, primaryID)
	allIDs = append(allIDs, secondaryIDs...)

	customers, err := e.customerCacheRepo.FindByExternalIDs(ctx, merchant.ID, "wordpress", allIDs)
	if err != nil {
		return nil, fmt.Errorf("wp executor: cache lookup: %w", err)
	}

	emailByID := make(map[int64]string, len(customers))
	for _, c := range customers {
		emailByID[c.ShopifyCustomerID] = c.Email
	}

	primary := toCustomerRef(primaryID, emailByID[primaryID])

	secondaries := make([]WCCustomerRef, 0, len(secondaryIDs))
	for _, sid := range secondaryIDs {
		secondaries = append(secondaries, toCustomerRef(sid, emailByID[sid]))
	}

	result, err := e.client.MergeCustomers(ctx, WCMergeRequest{
		Primary:        primary,
		Secondaries:    secondaries,
		FieldOverrides: fieldOverrides,
	})
	if err != nil {
		return nil, fmt.Errorf("wp executor: %w", err)
	}

	return &mergesvc.ExecuteResult{
		ResultingCustomerGID: WCCustomerGID(result.SurvivingUserID, result.SurvivingEmail),
		SecondaryCount:       result.MergedCount,
	}, nil
}

// toCustomerRef converts a stored external ID back to a WCCustomerRef.
// Negative IDs were assigned to guest customers by guestExternalID(); the plugin
// receives user_id=0 and is_guest=true and uses the email to find their orders.
func toCustomerRef(externalID int64, email string) WCCustomerRef {
	if externalID < 0 {
		return WCCustomerRef{UserID: 0, Email: email, IsGuest: true}
	}
	return WCCustomerRef{UserID: externalID, Email: email, IsGuest: false}
}
