package shopifyauth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const (
	DefaultScopes = "read_customers,write_customers,read_orders,write_orders"
)

type OAuthConfig struct {
	APIKey    string
	APISecret string
	AppURL    string
	Scopes    string
}

// GenerateInstallURL builds the Shopify OAuth authorization URL.
func (c *OAuthConfig) GenerateInstallURL(shop, state string) string {
	scopes := c.Scopes
	if scopes == "" {
		scopes = DefaultScopes
	}
	redirectURI := c.AppURL + "/auth/shopify/callback"
	params := url.Values{
		"client_id":    {c.APIKey},
		"scope":        {scopes},
		"redirect_uri": {redirectURI},
		"state":        {state},
	}
	return fmt.Sprintf("https://%s/admin/oauth/authorize?%s", shop, params.Encode())
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	Scope       string `json:"scope"`
}

// ExchangeCode exchanges an OAuth authorization code for an access token.
func (c *OAuthConfig) ExchangeCode(ctx context.Context, shop, code string) (string, error) {
	body := map[string]string{
		"client_id":     c.APIKey,
		"client_secret": c.APISecret,
		"code":          code,
	}
	b, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("https://%s/admin/oauth/access_token", shop),
		bytes.NewReader(b),
	)
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange: unexpected status %d", resp.StatusCode)
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	return tr.AccessToken, nil
}

// ValidateHMAC verifies that Shopify signed the OAuth callback params.
// Removes the hmac param, sorts remaining params, and compares HMAC-SHA256.
func ValidateHMAC(params url.Values, secret string) bool {
	received := params.Get("hmac")
	if received == "" {
		return false
	}

	var pairs []string
	for k, vals := range params {
		if k == "hmac" {
			continue
		}
		val := strings.NewReplacer("%", "%25", "&", "%26", "=", "%3D").Replace(vals[0])
		key := strings.NewReplacer("%", "%25", "&", "%26", "=", "%3D").Replace(k)
		pairs = append(pairs, key+"="+val)
	}
	sort.Strings(pairs)
	message := strings.Join(pairs, "&")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	expected := fmt.Sprintf("%x", mac.Sum(nil))

	return hmac.Equal([]byte(received), []byte(expected))
}

// ValidateWebhookHMAC verifies the HMAC-SHA256 signature on a Shopify webhook.
// Shopify sends the signature as a base64-encoded value in X-Shopify-Hmac-SHA256.
// body must be the raw, unmodified request body bytes.
func ValidateWebhookHMAC(body []byte, signature, secret string) bool {
	if signature == "" {
		return false
	}

	sig, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)

	return hmac.Equal(sig, expected)
}
