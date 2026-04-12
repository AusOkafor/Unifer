package dto

import (
	"encoding/json"
	"time"
)

type JobStatusResponse struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Status    string          `json:"status"`
	Result    *json.RawMessage `json:"result,omitempty"`
	Retries   int             `json:"retries"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}
