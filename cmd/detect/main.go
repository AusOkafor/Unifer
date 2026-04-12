// cmd/detect/main.go — queue a detect_duplicates job for the test merchant.
package main

import (
	"context"
	"fmt"
	"os"

	"merger/backend/internal/config"
	"merger/backend/internal/db"
	"merger/backend/internal/queue"
	"merger/backend/internal/repository"
	"merger/backend/internal/services/jobs"
	"merger/backend/internal/utils"

	"github.com/google/uuid"
)

func main() {
	log := utils.NewLogger("development")

	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	sqlDB, err := db.NewPostgres(cfg.DatabaseURL)
	if err != nil {
		panic(err)
	}
	defer sqlDB.Close()

	redisClient, err := queue.NewRedisClient(cfg.RedisURL)
	if err != nil {
		panic(err)
	}
	defer redisClient.Close()

	jobRepo := repository.NewJobRepo(sqlDB)
	q := queue.New(redisClient)
	dispatcher := jobs.NewDispatcher(jobRepo, q, log)

	merchantID, _ := uuid.Parse("00000000-0000-0000-0000-000000000001")
	jobID, err := dispatcher.Dispatch(context.Background(), "detect_duplicates", merchantID, map[string]string{
		"merchant_id": merchantID.String(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "dispatch error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("job queued: %s\n", jobID)
	fmt.Println("watch the server logs — detection completes in seconds")
	fmt.Printf("\nPoll status: GET http://localhost:3000/api/jobs/%s\n", jobID)
}
