package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"merger/backend/internal/models"
)

type CustomerCacheRepository interface {
	Upsert(ctx context.Context, c *models.CustomerCache) error
	FindByMerchant(ctx context.Context, merchantID uuid.UUID) ([]models.CustomerCache, error)
	FindByShopifyID(ctx context.Context, merchantID uuid.UUID, shopifyID int64) (*models.CustomerCache, error)
	DeleteByShopifyID(ctx context.Context, merchantID uuid.UUID, shopifyID int64) error
	// UpdateOrderStats patches the orders_count and total_spent fields from an
	// order webhook without overwriting unrelated fields. No-op if the customer
	// is not yet in cache.
	UpdateOrderStats(ctx context.Context, merchantID uuid.UUID, shopifyID int64, ordersCount int, totalSpent string) error
	// DeleteStaleEntries removes any cached customers for the merchant whose
	// Shopify ID is NOT in the provided set — used after a full sync to purge
	// customers that were merged or deleted in Shopify.
	DeleteStaleEntries(ctx context.Context, merchantID uuid.UUID, activeShopifyIDs []int64) (int64, error)
}

type customerCacheRepo struct {
	db *sqlx.DB
}

func NewCustomerCacheRepo(db *sqlx.DB) CustomerCacheRepository {
	return &customerCacheRepo{db: db}
}

func (r *customerCacheRepo) Upsert(ctx context.Context, c *models.CustomerCache) error {
	query := `
		INSERT INTO customer_cache
			(merchant_id, shopify_customer_id, email, name, phone, address_json, tags,
			 orders_count, total_spent, note, state, verified_email, shopify_created_at, updated_at)
		VALUES
			(:merchant_id, :shopify_customer_id, :email, :name, :phone, :address_json, :tags,
			 :orders_count, :total_spent, :note, :state, :verified_email, :shopify_created_at, NOW())
		ON CONFLICT (merchant_id, shopify_customer_id) DO UPDATE SET
			email              = EXCLUDED.email,
			name               = EXCLUDED.name,
			phone              = EXCLUDED.phone,
			address_json       = EXCLUDED.address_json,
			tags               = EXCLUDED.tags,
			orders_count       = EXCLUDED.orders_count,
			total_spent        = EXCLUDED.total_spent,
			note               = EXCLUDED.note,
			state              = EXCLUDED.state,
			verified_email     = EXCLUDED.verified_email,
			shopify_created_at = COALESCE(EXCLUDED.shopify_created_at, customer_cache.shopify_created_at),
			updated_at         = NOW()
		RETURNING id, updated_at`
	rows, err := r.db.NamedQueryContext(ctx, query, c)
	if err != nil {
		return fmt.Errorf("customer cache upsert: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return rows.Scan(&c.ID, &c.UpdatedAt)
	}
	return nil
}

func (r *customerCacheRepo) FindByMerchant(ctx context.Context, merchantID uuid.UUID) ([]models.CustomerCache, error) {
	var customers []models.CustomerCache
	err := r.db.SelectContext(ctx, &customers,
		`SELECT * FROM customer_cache WHERE merchant_id = $1 ORDER BY updated_at DESC`,
		merchantID,
	)
	if err != nil {
		return nil, fmt.Errorf("customer cache find by merchant: %w", err)
	}
	return customers, nil
}

func (r *customerCacheRepo) FindByShopifyID(ctx context.Context, merchantID uuid.UUID, shopifyID int64) (*models.CustomerCache, error) {
	var c models.CustomerCache
	err := r.db.GetContext(ctx, &c,
		`SELECT * FROM customer_cache WHERE merchant_id = $1 AND shopify_customer_id = $2`,
		merchantID, shopifyID,
	)
	if err != nil {
		return nil, fmt.Errorf("customer cache find by shopify id: %w", err)
	}
	return &c, nil
}

func (r *customerCacheRepo) UpdateOrderStats(ctx context.Context, merchantID uuid.UUID, shopifyID int64, ordersCount int, totalSpent string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE customer_cache
		 SET orders_count = $3, total_spent = $4, updated_at = NOW()
		 WHERE merchant_id = $1 AND shopify_customer_id = $2`,
		merchantID, shopifyID, ordersCount, totalSpent,
	)
	if err != nil {
		return fmt.Errorf("customer cache update order stats: %w", err)
	}
	return nil
}

func (r *customerCacheRepo) DeleteByShopifyID(ctx context.Context, merchantID uuid.UUID, shopifyID int64) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM customer_cache WHERE merchant_id = $1 AND shopify_customer_id = $2`,
		merchantID, shopifyID,
	)
	if err != nil {
		return fmt.Errorf("customer cache delete: %w", err)
	}
	return nil
}

func (r *customerCacheRepo) DeleteStaleEntries(ctx context.Context, merchantID uuid.UUID, activeShopifyIDs []int64) (int64, error) {
	if len(activeShopifyIDs) == 0 {
		// Safety guard: never wipe the entire cache if the sync returned nothing.
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM customer_cache
		 WHERE merchant_id = $1
		   AND shopify_customer_id != ALL($2)`,
		merchantID, pq.Array(activeShopifyIDs),
	)
	if err != nil {
		return 0, fmt.Errorf("customer cache delete stale: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
