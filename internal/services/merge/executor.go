package merge

import (
	"context"
	"fmt"

	shopifysvc "merger/backend/internal/services/shopify"
)

// ExecuteResult holds the outcome of a successful merge.
type ExecuteResult struct {
	ResultingCustomerGID string
	SecondaryCount       int
}

// Executor calls the Shopify customerMerge GraphQL API.
// This is the ONLY merge mechanism — Shopify handles order consolidation automatically.
type Executor struct {
	customerSvc *shopifysvc.CustomerService
}

func NewExecutor(customerSvc *shopifysvc.CustomerService) *Executor {
	return &Executor{customerSvc: customerSvc}
}

// Execute merges each secondary customer into the primary using Shopify's native API.
// For multi-way merges (>2 customers), it performs sequential pairwise merges:
//
//	primary ← secondary[0], then primary ← secondary[1], etc.
func (e *Executor) Execute(ctx context.Context, primaryID int64, secondaryIDs []int64, _ map[string]string) (*ExecuteResult, error) {
	primaryGID := shopifysvc.ShopifyIDToGID(primaryID)

	for i, secondaryID := range secondaryIDs {
		secondaryGID := shopifysvc.ShopifyIDToGID(secondaryID)
		result, err := e.customerSvc.Merge(ctx, primaryGID, secondaryGID)
		if err != nil {
			return nil, fmt.Errorf("merge step %d (secondary %d): %w", i+1, secondaryID, err)
		}
		// The resulting customer GID from each step becomes the primary for the next
		primaryGID = result.CustomerID
	}

	return &ExecuteResult{
		ResultingCustomerGID: primaryGID,
		SecondaryCount:       len(secondaryIDs),
	}, nil
}
