package wordpress

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	mergesvc "merger/backend/internal/services/merge"
)

// Executor implements merge.MergeExecutor for WordPress merchants.
// It delegates to the MergeIQ WP plugin REST endpoint.
type Executor struct {
	client *Client
	log    zerolog.Logger
}

func NewExecutor(client *Client, log zerolog.Logger) *Executor {
	return &Executor{client: client, log: log}
}

func (e *Executor) Execute(ctx context.Context, primaryID int64, secondaryIDs []int64) (*mergesvc.ExecuteResult, error) {
	result, err := e.client.MergeUsers(ctx, primaryID, secondaryIDs)
	if err != nil {
		return nil, fmt.Errorf("wp executor: %w", err)
	}
	return &mergesvc.ExecuteResult{
		ResultingCustomerGID: WPUserGID(result.SurvivingUserID),
		SecondaryCount:       result.MergedCount,
	}, nil
}
