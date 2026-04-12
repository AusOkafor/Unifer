package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const processingQueueSuffix = ":processing"

type Queue struct {
	client *redis.Client
}

func New(client *redis.Client) *Queue {
	return &Queue{client: client}
}

// Push enqueues a value (job ID string) to the left of the queue.
func (q *Queue) Push(ctx context.Context, queueName string, value string) error {
	if err := q.client.LPush(ctx, queueName, value).Err(); err != nil {
		return fmt.Errorf("queue push: %w", err)
	}
	return nil
}

// Pop blocks until an item is available, then atomically moves it from
// queueName to queueName:processing (at-least-once delivery).
func (q *Queue) Pop(ctx context.Context, queueName string, timeout time.Duration) (string, error) {
	processingQueue := queueName + processingQueueSuffix
	result, err := q.client.BRPopLPush(ctx, queueName, processingQueue, timeout).Result()
	if err == redis.Nil {
		return "", nil // timeout — no item
	}
	if err != nil {
		return "", fmt.Errorf("queue pop: %w", err)
	}
	return result, nil
}

// Acknowledge removes the item from the processing queue after successful handling.
func (q *Queue) Acknowledge(ctx context.Context, queueName string, value string) error {
	processingQueue := queueName + processingQueueSuffix
	if err := q.client.LRem(ctx, processingQueue, 1, value).Err(); err != nil {
		return fmt.Errorf("queue acknowledge: %w", err)
	}
	return nil
}

// Requeue moves all items from the processing queue back to the main queue.
// Used during crash recovery to re-process stuck jobs.
func (q *Queue) Requeue(ctx context.Context, queueName string) (int, error) {
	processingQueue := queueName + processingQueueSuffix
	count := 0
	for {
		val, err := q.client.RPopLPush(ctx, processingQueue, queueName).Result()
		if err == redis.Nil {
			break
		}
		if err != nil {
			return count, fmt.Errorf("requeue: %w", err)
		}
		_ = val
		count++
	}
	return count, nil
}
