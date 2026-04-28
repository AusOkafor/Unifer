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

// WCCustomerRef identifies a WooCommerce customer in a merge request.
// UserID is 0 and IsGuest is true for order-only customers with no WP account.
// Email is always populated and is the fallback identifier for guest order lookup.
type WCCustomerRef struct {
	UserID  int64  `json:"user_id"`  // 0 for guests
	Email   string `json:"email"`    // billing email — always present
	IsGuest bool   `json:"is_guest"` // true when no WP user account exists
}

// WCMergeRequest is the body sent to POST /wp-json/mergeiq/v1/merge.
// The plugin reassigns orders from all Secondaries to Primary, merges metadata,
// and disables (not deletes) secondary user accounts.
// FieldOverrides carries the Merge Composer field selections from the WP admin UI.
// Keys match WooCommerce user meta keys (e.g. "billing_email", "billing_first_name");
// values are the chosen value. Plugin applies these after order reassignment.
type WCMergeRequest struct {
	Primary        WCCustomerRef     `json:"primary"`
	Secondaries    []WCCustomerRef   `json:"secondaries"`
	FieldOverrides map[string]string `json:"field_overrides,omitempty"`
}

// WCMergeResult is the response from POST /wp-json/mergeiq/v1/merge.
type WCMergeResult struct {
	SurvivingUserID   int64    `json:"surviving_user_id"`   // 0 if primary was a guest with no account created
	SurvivingEmail    string   `json:"surviving_email"`
	MergedCount       int      `json:"merged_count"`        // number of secondary identities merged
	OrdersReassigned  int      `json:"orders_reassigned"`   // orders moved to primary
	Errors            []string `json:"errors,omitempty"`
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
		http:    &http.Client{Timeout: mergeTimeout + 5*time.Second},
		log:     log,
	}
}

// MergeCustomers calls POST /wp-json/mergeiq/v1/merge on the WP plugin.
//
// The plugin is expected to:
//   - Reassign all orders from each secondary identity to the primary
//   - Merge billing/shipping metadata onto the primary user record
//   - Set user_status=2 (disabled) on secondary WP user accounts — NEVER delete
//   - Handle guest secondaries (user_id=0, is_guest=true) by matching on billing_email
//
// Each request is signed with HMAC-SHA256 over "timestamp.body" so the plugin
// can verify authenticity and reject replays older than ±5 minutes.
//
// Retries are only attempted on network-level errors. HTTP 4xx/5xx and plugin
// errors are never retried — the merge may have partially executed.
func (c *Client) MergeCustomers(ctx context.Context, req WCMergeRequest) (*WCMergeResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("wp client: marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, mergeTimeout)
	defer cancel()

	var result *WCMergeResult
	var lastErr error
	for attempt := range maxMergeRetries {
		result, lastErr = c.doMerge(ctx, body)
		if lastErr == nil {
			break
		}
		if !isNetworkError(lastErr) {
			return nil, lastErr
		}
		if attempt < maxMergeRetries-1 {
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

	// A surviving_user_id of 0 is acceptable when both primary and all secondaries
	// are guests (no WP accounts). surviving_email must be present in that case.
	if result == nil || (result.SurvivingUserID == 0 && result.SurvivingEmail == "") {
		return nil, fmt.Errorf("wp client: plugin returned empty surviving identity — merge outcome unknown")
	}

	return result, nil
}

func (c *Client) doMerge(ctx context.Context, body []byte) (*WCMergeResult, error) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	url := c.baseURL + "/wp-json/mergeiq/v1/merge"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("wp client: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MergeIQ-Timestamp", ts)
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

	var result WCMergeResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("wp client: decode response: %w", err)
	}
	if len(result.Errors) > 0 {
		c.log.Warn().Strs("plugin_errors", result.Errors).Msg("wc merge: plugin reported partial errors")
	}
	return &result, nil
}

// hmacSignature signs "timestamp.body" with the API key using HMAC-SHA256.
func hmacSignature(timestamp string, body []byte, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func isNetworkError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var urlErr interface{ Timeout() bool }
	if errors.As(err, &urlErr) {
		return true
	}
	return false
}
