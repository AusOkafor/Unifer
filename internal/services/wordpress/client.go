package wordpress

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// MergeUsersResult is the response body from POST /wp-json/mergeiq/v1/merge.
type MergeUsersResult struct {
	SurvivingUserID int64    `json:"surviving_user_id"`
	MergedCount     int      `json:"merged_count"`
	Errors          []string `json:"errors,omitempty"`
}

// Client is an HTTP client for the MergeIQ WordPress plugin REST API.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	log     zerolog.Logger
}

func NewClient(baseURL, apiKey string, log zerolog.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 30 * time.Second},
		log:     log,
	}
}

type mergeRequest struct {
	PrimaryID    int64   `json:"primary_id"`
	SecondaryIDs []int64 `json:"secondary_ids"`
}

// MergeUsers calls POST /wp-json/mergeiq/v1/merge on the WP plugin.
// The request is signed with HMAC-SHA256 using the shared API key.
func (c *Client) MergeUsers(ctx context.Context, primaryID int64, secondaryIDs []int64) (*MergeUsersResult, error) {
	body, err := json.Marshal(mergeRequest{PrimaryID: primaryID, SecondaryIDs: secondaryIDs})
	if err != nil {
		return nil, fmt.Errorf("wp client: marshal request: %w", err)
	}

	url := c.baseURL + "/wp-json/mergeiq/v1/merge"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("wp client: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MergeIQ-Signature", hmacSignature(body, c.apiKey))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wp client: do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("wp client: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wp client: plugin returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result MergeUsersResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("wp client: decode response: %w", err)
	}
	if len(result.Errors) > 0 {
		c.log.Warn().Strs("plugin_errors", result.Errors).Msg("wp merge: plugin reported partial errors")
	}
	return &result, nil
}

func hmacSignature(body []byte, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
