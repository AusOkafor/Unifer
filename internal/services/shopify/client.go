package shopify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"merger/backend/internal/utils"
)

const (
	apiVersion    = "2025-10"
	rateLimitWarn = 38 // warn and slow down if call count reaches this (max is typically 40)
)

type Client struct {
	shopDomain  string
	accessToken string
	httpClient  *http.Client
	log         zerolog.Logger
}

func NewClient(shopDomain, accessToken string, log zerolog.Logger) *Client {
	// ShopDomain must be a bare domain (e.g. "store.myshopify.com"). Strip any
	// scheme prefix that may have been stored by an early registration call so we
	// never build a double-scheme URL like https://http://....
	shopDomain = strings.TrimPrefix(strings.TrimPrefix(shopDomain, "https://"), "http://")
	return &Client{
		shopDomain:  shopDomain,
		accessToken: accessToken,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		log:         log,
	}
}

// doREST executes a REST API call with retry and rate-limit handling.
func (c *Client) doREST(ctx context.Context, method, path string, body, result interface{}) error {
	url := fmt.Sprintf("https://%s/admin/api/%s%s", c.shopDomain, apiVersion, path)

	return utils.RetryWithBackoff(ctx, 3, time.Second, func() error {
		var bodyReader io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("marshal request body: %w", err)
			}
			bodyReader = bytes.NewReader(b)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("X-Shopify-Access-Token", c.accessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("http request: %w", err)
		}
		defer resp.Body.Close()

		// Rate limit handling
		c.handleRateLimit(resp)

		if resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := 2 * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.ParseFloat(ra, 64); err == nil {
					retryAfter = time.Duration(secs * float64(time.Second))
				}
			}
			c.log.Warn().Dur("retry_after", retryAfter).Msg("shopify rate limit hit")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryAfter):
			}
			return fmt.Errorf("rate limited") // triggers retry
		}

		if resp.StatusCode >= 500 {
			return fmt.Errorf("shopify server error: %d", resp.StatusCode)
		}

		if resp.StatusCode >= 400 {
			respBody, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("shopify client error %d: %s", resp.StatusCode, string(respBody))
		}

		if result != nil {
			return json.NewDecoder(resp.Body).Decode(result)
		}
		return nil
	})
}

// doGraphQL executes a GraphQL mutation/query against the Shopify Admin API.
func (c *Client) doGraphQL(ctx context.Context, query string, variables map[string]interface{}, result interface{}) error {
	url := fmt.Sprintf("https://%s/admin/api/%s/graphql.json", c.shopDomain, apiVersion)

	return utils.RetryWithBackoff(ctx, 3, time.Second, func() error {
		payload := map[string]interface{}{
			"query":     query,
			"variables": variables,
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal graphql payload: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("build graphql request: %w", err)
		}
		req.Header.Set("X-Shopify-Access-Token", c.accessToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("graphql http request: %w", err)
		}
		defer resp.Body.Close()

		c.handleRateLimit(resp)

		if resp.StatusCode == http.StatusTooManyRequests {
			time.Sleep(2 * time.Second)
			return fmt.Errorf("rate limited")
		}
		if resp.StatusCode >= 500 {
			return fmt.Errorf("shopify graphql server error: %d", resp.StatusCode)
		}

		var gqlResp struct {
			Data   json.RawMessage `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
			return fmt.Errorf("decode graphql response: %w", err)
		}
		if len(gqlResp.Errors) > 0 {
			msgs := make([]string, len(gqlResp.Errors))
			for i, e := range gqlResp.Errors {
				msgs[i] = e.Message
			}
			return fmt.Errorf("graphql errors: %s", strings.Join(msgs, "; "))
		}

		if result != nil {
			return json.Unmarshal(gqlResp.Data, result)
		}
		return nil
	})
}

// handleRateLimit checks the call limit header and sleeps if approaching the cap.
func (c *Client) handleRateLimit(resp *http.Response) {
	limit := resp.Header.Get("X-Shopify-Shop-Api-Call-Limit")
	if limit == "" {
		return
	}
	parts := strings.SplitN(limit, "/", 2)
	if len(parts) != 2 {
		return
	}
	current, err1 := strconv.Atoi(parts[0])
	if err1 != nil {
		return
	}
	if current >= rateLimitWarn {
		c.log.Warn().Str("call_limit", limit).Msg("shopify API call limit approaching")
		time.Sleep(500 * time.Millisecond)
	}
}
