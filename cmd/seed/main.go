package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const merchantID = "00000000-0000-0000-0000-000000000001"

type customer struct {
	shopifyID   int64
	name        string
	email       string
	phone       string
	tags        []string
	ordersCount int
	totalSpent  string
	address     map[string]string
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		panic("DATABASE_URL not set")
	}
	db, err := sqlx.Open("pgx", dbURL)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// Ensure test merchant exists
	db.ExecContext(context.Background(), `
		INSERT INTO merchants (id, shop_domain, access_token_enc, created_at)
		VALUES ('00000000-0000-0000-0000-000000000001', 'test.myshopify.com', 'placeholder', NOW())
		ON CONFLICT (id) DO NOTHING
	`)

	customers := []customer{
		// --- Pair 1: same email, slight name variation ---
		{10001, "James Whitfield", "james.whitfield@gmail.com", "+15551110001", []string{"vip"}, 12, "1450.00", map[string]string{"address1": "12 Oak St", "city": "Austin", "province": "TX", "zip": "78701"}},
		{10002, "Jim Whitfield", "james.whitfield@gmail.com", "+15551110001", []string{"wholesale"}, 3, "320.00", map[string]string{"address1": "12 Oak Street", "city": "Austin", "province": "TX", "zip": "78701"}},

		// --- Pair 2: same phone, different email domains ---
		{10003, "Maria Santos", "maria.santos@outlook.com", "+15552220001", []string{}, 7, "890.50", map[string]string{"address1": "45 Pine Ave", "city": "Miami", "province": "FL", "zip": "33101"}},
		{10004, "Maria Santos", "msantos@gmail.com", "+15552220001", []string{"subscriber"}, 2, "145.00", map[string]string{"address1": "45 Pine Ave", "city": "Miami", "province": "FL", "zip": "33101"}},

		// --- Pair 3: typo in email, same name and address ---
		{10005, "Robert Chen", "robert.chen@yahoo.com", "+15553330001", []string{"loyalty"}, 18, "3200.00", map[string]string{"address1": "8 Maple Rd", "city": "Seattle", "province": "WA", "zip": "98101"}},
		{10006, "Robert Chen", "robert.chem@yahoo.com", "", []string{}, 1, "59.99", map[string]string{"address1": "8 Maple Road", "city": "Seattle", "province": "WA", "zip": "98101"}},

		// --- Pair 4: same email, name split differently ---
		{10007, "Sarah Jane Miller", "sjmiller@company.com", "+15554440001", []string{"b2b"}, 5, "760.00", map[string]string{}},
		{10008, "Sarah Miller", "sjmiller@company.com", "+15554440002", []string{}, 0, "0.00", map[string]string{}},

		// --- Unique customer (no duplicate) ---
		{10009, "Daniel Park", "dpark@personal.io", "+15559990001", []string{"vip", "loyalty"}, 22, "4100.00", map[string]string{"address1": "99 Birch Ln", "city": "Chicago", "province": "IL", "zip": "60601"}},
	}

	for _, c := range customers {
		addrJSON, _ := json.Marshal(c.address)
		// Build a Postgres TEXT[] literal: {"vip","loyalty"}
		tagsLiteral := "{" + strings.Join(quoteElems(c.tags), ",") + "}"

		_, err := db.ExecContext(context.Background(), `
			INSERT INTO customer_cache (
				merchant_id, shopify_customer_id, name, email, phone,
				tags, orders_count, total_spent, address_json, updated_at
			) VALUES (
				$1, $2, $3, $4, $5,
				$6::text[], $7, $8, $9, $10
			)
			ON CONFLICT (merchant_id, shopify_customer_id) DO UPDATE SET
				name         = EXCLUDED.name,
				email        = EXCLUDED.email,
				phone        = EXCLUDED.phone,
				tags         = EXCLUDED.tags,
				orders_count = EXCLUDED.orders_count,
				total_spent  = EXCLUDED.total_spent,
				address_json = EXCLUDED.address_json,
				updated_at   = EXCLUDED.updated_at
		`,
			merchantID, c.shopifyID, c.name, c.email, c.phone,
			tagsLiteral, c.ordersCount, c.totalSpent, addrJSON, time.Now(),
		)
		if err != nil {
			fmt.Printf("  error inserting %s: %v\n", c.name, err)
		} else {
			fmt.Printf("  seeded: %-25s  %s\n", c.name, c.email)
		}
	}
	fmt.Println("\ndone — run POST /api/jobs to trigger detection, or queue a detect job directly")
}

func quoteElems(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return out
}
