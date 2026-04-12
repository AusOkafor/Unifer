package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jmoiron/sqlx"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	db, _ := sqlx.Open("pgx", os.Getenv("DATABASE_URL"))
	res, err := db.ExecContext(context.Background(),
		`UPDATE jobs SET status='failed' WHERE type='detect_duplicates' AND status IN ('queued','processing')`)
	if err != nil {
		panic(err)
	}
	n, _ := res.RowsAffected()
	fmt.Printf("cleared %d stuck job(s)\n", n)
}
