package middleware_test

// Unit tests for the AuthRequired middleware.
//
// Shopify App Store requirement T1.5: the app must not rely on third-party
// cookies (e.g. a session cookie set by the backend) because browsers in
// incognito mode and Safari's ITP block cross-origin cookies by default.
//
// These tests verify that:
//  1. A valid Shopify session token in the Authorization header is accepted.
//  2. A missing or malformed token is rejected with 401.
//  3. A token with the wrong audience (wrong API key) is rejected.
//  4. A token whose dest claim doesn't map to a known merchant is rejected.
//  5. The middleware does NOT accept a bare cookie with no Authorization header
//     when the shop is unknown — it must rely on the token's dest claim, not a
//     stale cookie.
//
// Tokens are HS256 JWTs signed with a test API secret — same algorithm Shopify
// uses when issuing App Bridge session tokens.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"merger/backend/internal/middleware"
	"merger/backend/internal/models"
	"merger/backend/internal/repository"
)

const (
	testAPISecret = "test-shopify-api-secret-32bytes!!"
	testAPIKey    = "test-api-key-abc123"
	testShop      = "test.myshopify.com"
)

// ─── stub ─────────────────────────────────────────────────────────────────────

type stubMerchantRepo struct {
	merchant *models.Merchant
}

func (s *stubMerchantRepo) Create(_ context.Context, _ *models.Merchant) error { return nil }
func (s *stubMerchantRepo) FindByDomain(_ context.Context, domain string) (*models.Merchant, error) {
	if s.merchant != nil && s.merchant.ShopDomain == domain {
		return s.merchant, nil
	}
	return nil, errors.New("not found")
}
func (s *stubMerchantRepo) FindByID(_ context.Context, _ uuid.UUID) (*models.Merchant, error) {
	return nil, errors.New("not found")
}
func (s *stubMerchantRepo) ListAll(_ context.Context) ([]models.Merchant, error) { return nil, nil }
func (s *stubMerchantRepo) UpdateToken(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (s *stubMerchantRepo) Delete(_ context.Context, _ uuid.UUID) error                { return nil }

var _ repository.MerchantRepository = (*stubMerchantRepo)(nil)

// ─── helpers ─────────────────────────────────────────────────────────────────

func newMerchant() *models.Merchant {
	return &models.Merchant{
		ID:         uuid.New(),
		ShopDomain: testShop,
	}
}

// makeShopifyToken builds a Shopify-style session token signed with the test secret.
func makeShopifyToken(shop, apiKey, secret string, exp time.Time) string {
	claims := jwt.MapClaims{
		"iss":  "https://" + shop + "/admin",
		"dest": "https://" + shop,
		"aud":  []string{apiKey},
		"sub":  "42",
		"exp":  exp.Unix(),
		"nbf":  time.Now().Add(-10 * time.Second).Unix(),
		"iat":  time.Now().Add(-10 * time.Second).Unix(),
		"jti":  uuid.New().String(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok, _ := t.SignedString([]byte(secret))
	return tok
}

func newTestRouter(repo repository.MerchantRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.AuthRequired(testAPISecret, testAPIKey, repo))
	r.GET("/protected", func(c *gin.Context) {
		m := middleware.GetMerchant(c)
		c.JSON(http.StatusOK, gin.H{"shop": m.ShopDomain})
	})
	return r
}

func getWithBearer(t *testing.T, r *gin.Engine, token string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "/protected", nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ─── tests ────────────────────────────────────────────────────────────────────

func TestAuth_ValidSessionToken_Accepted(t *testing.T) {
	merchant := newMerchant()
	r := newTestRouter(&stubMerchantRepo{merchant: merchant})

	tok := makeShopifyToken(testShop, testAPIKey, testAPISecret, time.Now().Add(time.Minute))
	w := getWithBearer(t, r, tok)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuth_NoToken_Returns401(t *testing.T) {
	r := newTestRouter(&stubMerchantRepo{merchant: newMerchant()})
	w := getWithBearer(t, r, "")
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"missing Authorization header must return 401")
}

func TestAuth_MalformedToken_Returns401(t *testing.T) {
	r := newTestRouter(&stubMerchantRepo{merchant: newMerchant()})
	w := getWithBearer(t, r, "not.a.jwt")
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"malformed token must return 401")
}

func TestAuth_WrongSecret_Returns401(t *testing.T) {
	merchant := newMerchant()
	r := newTestRouter(&stubMerchantRepo{merchant: merchant})

	tok := makeShopifyToken(testShop, testAPIKey, "wrong-secret", time.Now().Add(time.Minute))
	w := getWithBearer(t, r, tok)
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"token signed with wrong secret must return 401")
}

func TestAuth_WrongAudience_Returns401(t *testing.T) {
	merchant := newMerchant()
	r := newTestRouter(&stubMerchantRepo{merchant: merchant})

	tok := makeShopifyToken(testShop, "other-app-api-key", testAPISecret, time.Now().Add(time.Minute))
	w := getWithBearer(t, r, tok)
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"token with wrong audience (different API key) must return 401")
}

func TestAuth_ExpiredToken_Returns401(t *testing.T) {
	merchant := newMerchant()
	r := newTestRouter(&stubMerchantRepo{merchant: merchant})

	tok := makeShopifyToken(testShop, testAPIKey, testAPISecret, time.Now().Add(-time.Minute))
	w := getWithBearer(t, r, tok)
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"expired token must return 401")
}

func TestAuth_UnknownMerchant_Returns401(t *testing.T) {
	// Token is valid but the shop isn't in our DB — merchant not installed.
	r := newTestRouter(&stubMerchantRepo{merchant: nil})

	tok := makeShopifyToken(testShop, testAPIKey, testAPISecret, time.Now().Add(time.Minute))
	w := getWithBearer(t, r, tok)
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"valid token for unknown merchant must return 401")
}

func TestAuth_MerchantInjectedIntoContext(t *testing.T) {
	merchant := newMerchant()
	r := newTestRouter(&stubMerchantRepo{merchant: merchant})

	tok := makeShopifyToken(testShop, testAPIKey, testAPISecret, time.Now().Add(time.Minute))
	w := getWithBearer(t, r, tok)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), testShop,
		"merchant shop domain must be available in handler context")
}

// TestAuth_NoCookieDependency proves the middleware does not accept a session
// cookie when no Authorization header is present and the cookie value does not
// contain a valid Shopify JWT.  This is the incognito-mode safety check —
// cross-origin cookies are blocked, so authentication must succeed via the
// Bearer token alone.
func TestAuth_NoCookieDependency(t *testing.T) {
	merchant := newMerchant()
	r := newTestRouter(&stubMerchantRepo{merchant: merchant})

	req, err := http.NewRequest(http.MethodGet, "/protected", nil)
	require.NoError(t, err)
	// Simulate a stale session cookie (set by the old auth flow before migration)
	// with NO Authorization header.
	req.AddCookie(&http.Cookie{Name: "session", Value: "stale-jwt-value"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"a stale cookie with no Authorization header must not grant access — incognito safety")
}
