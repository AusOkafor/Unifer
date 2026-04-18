package handlers_test

// Unit tests for WebhookHandler — no database required.
//
// Invariants verified (Shopify App Store requirement T1.4):
//  1. Missing HMAC header → 401 Unauthorized
//  2. Tampered/wrong HMAC → 401 Unauthorized
//  3. customers/data_request with valid HMAC → 200 OK
//  4. customers/redact with valid HMAC → 200 OK
//  5. shop/redact with valid HMAC → 200 OK
//
// Shopify requires 200 for all compliance webhooks and 401 for invalid
// signatures. These are tested here without a database or real Shopify calls.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"merger/backend/internal/api/handlers"
	"merger/backend/internal/models"
	"merger/backend/internal/repository"
)

const testWebhookSecret = "test-webhook-secret-abc123"

// shopSign returns the base64-encoded HMAC-SHA256 of body using secret —
// identical to how Shopify signs webhook payloads.
func shopSign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// ─── stubs ────────────────────────────────────────────────────────────────────

// stubMerchantRepo returns "not found" for all lookups and is a no-op for writes.
// GDPR handlers gracefully skip cleanup when the merchant is not found, so this
// lets us test the HTTP status code without a real database.
type stubMerchantRepo struct{}

func (s *stubMerchantRepo) Create(_ context.Context, _ *models.Merchant) error {
	return nil
}
func (s *stubMerchantRepo) FindByDomain(_ context.Context, _ string) (*models.Merchant, error) {
	return nil, errors.New("not found")
}
func (s *stubMerchantRepo) FindByID(_ context.Context, _ uuid.UUID) (*models.Merchant, error) {
	return nil, errors.New("not found")
}
func (s *stubMerchantRepo) ListAll(_ context.Context) ([]models.Merchant, error) {
	return nil, nil
}
func (s *stubMerchantRepo) UpdateToken(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (s *stubMerchantRepo) Delete(_ context.Context, _ uuid.UUID) error { return nil }

// ─── helpers ─────────────────────────────────────────────────────────────────

func newTestRouter(secret string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := handlers.NewWebhookHandler(
		secret,
		&stubMerchantRepo{},
		nil, // customerCacheRepo — not reached in these tests
		nil, // settingsRepo
		nil, // jobDispatcher
		nil, // idempotency
		zerolog.Nop(),
	)
	r := gin.New()
	r.POST("/webhooks", h.Handle)
	return r
}

func doWebhookRequest(t *testing.T, r *gin.Engine, topic, body, sig string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "/webhooks", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Shopify-Topic", topic)
	req.Header.Set("X-Shopify-Shop-Domain", "test.myshopify.com")
	if sig != "" {
		req.Header.Set("X-Shopify-Hmac-SHA256", sig)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ─── HMAC guard tests ─────────────────────────────────────────────────────────

func TestWebhook_MissingHMAC_Returns401(t *testing.T) {
	r := newTestRouter(testWebhookSecret)
	body := `{"id":1}`
	w := doWebhookRequest(t, r, "customers/update", body, "")
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"missing HMAC header must return 401")
}

func TestWebhook_WrongHMAC_Returns401(t *testing.T) {
	r := newTestRouter(testWebhookSecret)
	body := `{"id":1}`
	w := doWebhookRequest(t, r, "customers/update", body, "bm90YXZhbGlkc2lnbmF0dXJl")
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"incorrect HMAC must return 401")
}

func TestWebhook_TamperedBody_Returns401(t *testing.T) {
	r := newTestRouter(testWebhookSecret)
	originalBody := `{"id":1}`
	tamperedBody := `{"id":2}` // body changed after signing
	sig := shopSign([]byte(originalBody), testWebhookSecret)
	w := doWebhookRequest(t, r, "customers/update", tamperedBody, sig)
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"valid HMAC but tampered body must return 401")
}

func TestWebhook_WrongSecret_Returns401(t *testing.T) {
	r := newTestRouter(testWebhookSecret)
	body := `{"id":1}`
	sig := shopSign([]byte(body), "different-secret") // signed with wrong key
	w := doWebhookRequest(t, r, "customers/update", body, sig)
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"HMAC signed with wrong secret must return 401")
}

// ─── GDPR compliance webhook tests ───────────────────────────────────────────

func TestWebhook_DataRequest_Returns200(t *testing.T) {
	r := newTestRouter(testWebhookSecret)
	body := `{"customer":{"id":123,"email":"alice@example.com"},"data_request":{"id":456}}`
	sig := shopSign([]byte(body), testWebhookSecret)
	w := doWebhookRequest(t, r, "customers/data_request", body, sig)
	assert.Equal(t, http.StatusOK, w.Code,
		"customers/data_request with valid HMAC must return 200")
}

func TestWebhook_CustomersRedact_Returns200(t *testing.T) {
	r := newTestRouter(testWebhookSecret)
	body := `{"customer":{"id":123},"shop_domain":"test.myshopify.com"}`
	sig := shopSign([]byte(body), testWebhookSecret)
	w := doWebhookRequest(t, r, "customers/redact", body, sig)
	assert.Equal(t, http.StatusOK, w.Code,
		"customers/redact with valid HMAC must return 200 (even when merchant not found)")
}

func TestWebhook_ShopRedact_Returns200(t *testing.T) {
	r := newTestRouter(testWebhookSecret)
	body := `{"shop_id":1,"shop_domain":"test.myshopify.com"}`
	sig := shopSign([]byte(body), testWebhookSecret)
	w := doWebhookRequest(t, r, "shop/redact", body, sig)
	assert.Equal(t, http.StatusOK, w.Code,
		"shop/redact with valid HMAC must return 200 (even when merchant not found)")
}

// ─── Valid HMAC on a non-GDPR topic ──────────────────────────────────────────

func TestWebhook_ValidHMAC_UnknownMerchant_Returns200(t *testing.T) {
	// Shopify expects 200 even for topics where the merchant is unknown —
	// the handler must never 4xx on a valid signature.
	r := newTestRouter(testWebhookSecret)
	body := `{"id":999}`
	sig := shopSign([]byte(body), testWebhookSecret)
	w := doWebhookRequest(t, r, "customers/update", body, sig)
	assert.Equal(t, http.StatusOK, w.Code,
		"valid HMAC from unknown merchant must still return 200")
}

// ─── Repository interface compliance ─────────────────────────────────────────

// Compile-time check: stubMerchantRepo must satisfy MerchantRepository.
var _ repository.MerchantRepository = (*stubMerchantRepo)(nil)
