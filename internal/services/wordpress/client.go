package wordpress

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/rs/zerolog"
)

const (
	mergeTimeout    = 30 * time.Second
	maxMergeRetries = 3
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
		// Transport-level timeout is a safety net; per-request timeout is
		// enforced via context (see MergeUsers).
		http: &http.Client{Timeout: mergeTimeout + 5*time.Second},
		log:  log,
	}
}

type mergeRequest struct {
	PrimaryID    int64   `json:"primary_id"`
	SecondaryIDs []int64 `json:"secondary_ids"`
}

// MergeUsers calls POST /wp-json/mergeiq/v1/merge on the WP plugin.
//
// Each request is signed with HMAC-SHA256 over "timestamp.body" and a
// monotonic X-MergeIQ-Timestamp header so the WP plugin can reject replays
// older than ±5 minutes.
//
// Retries are only attempted on network-level errors (connection refused,
// timeout). HTTP 4xx/5xx responses are never retried — retrying a merge on
// a non-network error risks double-execution if the plugin partially
// succeeded.
func (c *Client) MergeUsers(ctx context.Context, primaryID int64, secondaryIDs []int64) (*MergeUsersResult, error) {
	body, err := json.Marshal(mergeRequest{PrimaryID: primaryID, SecondaryIDs: secondaryIDs})
	if err != nil {
		return nil, fmt.Errorf("wp client: marshal request: %w", err)
	}

	// Per-request deadline — ensures the merge call never hangs indefinitely.
	ctx, cancel := context.WithTimeout(ctx, mergeTimeout)
	defer cancel()

	// Retry only on network-level errors. HTTP errors and plugin-level errors
	// are never retried — the merge may have partially executed.
	var result *MergeUsersResult
	var lastErr error
	for attempt := range maxMergeRetries {
		result, lastErr = c.doMerge(ctx, body)
		if lastErr == nil {
			break
		}
		if !isNetworkError(lastErr) {
			// Non-retryable: stop immediately and surface the error.
			return nil, lastErr
		}
		if attempt < maxMergeRetries-1 {
			// Exponential backoff: 1s, 2s, 4s.
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	// Validate the plugin actually returned a usable result.
	if result == nil || result.SurvivingUserID == 0 {
		return nil, fmt.Errorf("wp client: plugin returned zero surviving_user_id — merge outcome unknown")
	}

	return result, nil
}

func (c *Client) doMerge(ctx context.Context, body []byte) (*MergeUsersResult, error) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	url := c.baseURL + "/wp-json/mergeiq/v1/merge"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("wp client: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MergeIQ-Timestamp", ts)
	// Signature covers both timestamp and body so the plugin can verify
	// neither was tampered with and reject replays with stale timestamps.
	req.Header.Set("X-MergeIQ-Signature", hmacSignature(ts, body, c.apiKey))

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

// hmacSignature signs "timestamp.body" with the API key using HMAC-SHA256.
// The WP plugin verifies this value and rejects requests where the timestamp
// is more than ±5 minutes from its local clock.
func hmacSignature(timestamp string, body []byte, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// isNetworkError returns true for transport-level failures that are safe to
// retry (connection refused, DNS failure, context deadline from the outer
// caller — not our own per-request deadline).
func isNetworkError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	// net/http wraps transport errors in *url.Error
	var urlErr interface{ Timeout() bool }
	if errors.As(err, &urlErr) {
		return true // connection-level timeout or reset
	}
	return false
}

