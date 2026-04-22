package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/api/dto"
	"merger/backend/internal/middleware"
	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	"merger/backend/internal/services/jobs"
	wpsvc "merger/backend/internal/services/wordpress"
	"merger/backend/internal/utils"
)

// WPHandler handles WordPress merchant registration, token refresh, and user sync.
type WPHandler struct {
	merchantRepo    repository.MerchantRepository
	refreshRepo     repository.WPRefreshTokenRepository
	syncSvc         *wpsvc.SyncService
	dispatcher      *jobs.Dispatcher
	encryptor       *utils.Encryptor
	jwtSecret       string
	log             zerolog.Logger
}

func NewWPHandler(
	merchantRepo repository.MerchantRepository,
	refreshRepo repository.WPRefreshTokenRepository,
	syncSvc *wpsvc.SyncService,
	dispatcher *jobs.Dispatcher,
	encryptor *utils.Encryptor,
	jwtSecret string,
	log zerolog.Logger,
) *WPHandler {
	return &WPHandler{
		merchantRepo: merchantRepo,
		refreshRepo:  refreshRepo,
		syncSvc:      syncSvc,
		dispatcher:   dispatcher,
		encryptor:     encryptor,
		jwtSecret:     jwtSecret,
		log:           log,
	}
}

// Register handles POST /api/wp/register (unauthenticated).
// Creates or updates the WordPress merchant and issues access + refresh tokens.
func (h *WPHandler) Register(c *gin.Context) {
	var req dto.WPRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	encKey, err := h.encryptor.Encrypt(req.APIKey)
	if err != nil {
		h.log.Error().Err(err).Msg("wp register: encrypt api key")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	merchant := &models.Merchant{
		ShopDomain:     req.SiteURL,
		AccessTokenEnc: encKey,
		Platform:       "wordpress",
	}
	if err := h.merchantRepo.Create(c.Request.Context(), merchant); err != nil {
		h.log.Error().Err(err).Str("site_url", req.SiteURL).Msg("wp register: upsert merchant")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to register"})
		return
	}

	// Revoke old refresh tokens before issuing new ones.
	if err := h.refreshRepo.RevokeAll(c.Request.Context(), merchant.ID); err != nil {
		h.log.Warn().Err(err).Str("merchant_id", merchant.ID.String()).Msg("wp register: revoke old tokens")
	}

	accessToken, err := middleware.IssueAccessToken(h.jwtSecret, merchant.ID)
	if err != nil {
		h.log.Error().Err(err).Msg("wp register: issue access token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	raw, hash, expiresAt, err := middleware.IssueRefreshToken()
	if err != nil {
		h.log.Error().Err(err).Msg("wp register: issue refresh token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if err := h.refreshRepo.Create(c.Request.Context(), &models.WPRefreshToken{
		MerchantID: merchant.ID,
		TokenHash:  hash,
		ExpiresAt:  expiresAt,
	}); err != nil {
		h.log.Error().Err(err).Msg("wp register: store refresh token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, dto.WPRegisterResponse{
		AccessToken:  accessToken,
		RefreshToken: merchant.ID.String() + ":" + raw,
		ExpiresIn:    900, // 15 minutes
	})
}

// Refresh handles POST /api/wp/auth/refresh (unauthenticated).
// Validates the refresh token and issues a new access token.
func (h *WPHandler) Refresh(c *gin.Context) {
	var req dto.WPRefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// The refresh token is prefixed with the merchant UUID for lookup.
	// Format: <merchant_uuid>:<raw_token>
	merchantID, rawToken, ok := splitRefreshToken(req.RefreshToken)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
		return
	}

	hash := middleware.HashRefreshToken(rawToken)
	stored, err := h.refreshRepo.FindValid(c.Request.Context(), merchantID, hash)
	if err != nil || stored == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token invalid or expired"})
		return
	}

	accessToken, err := middleware.IssueAccessToken(h.jwtSecret, merchantID)
	if err != nil {
		h.log.Error().Err(err).Msg("wp refresh: issue access token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, dto.WPRefreshResponse{
		AccessToken: accessToken,
		ExpiresIn:   900,
	})
}

// SyncUsers handles POST /api/wp/customers/sync (requires WP auth).
// Ingests WordPress users into the customer cache and dispatches a detection job.
func (h *WPHandler) SyncUsers(c *gin.Context) {
	merchant := middleware.GetMerchant(c)
	if merchant == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req dto.WPSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Users) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "users must not be empty"})
		return
	}

	ingested, err := h.syncSvc.IngestUsers(c.Request.Context(), merchant.ID, req.Users)
	if err != nil {
		h.log.Error().Err(err).Str("merchant_id", merchant.ID.String()).Msg("wp sync: ingest failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "sync failed"})
		return
	}

	var jobIDStr string
	if h.dispatcher != nil {
		jobID, err := h.dispatcher.Dispatch(c.Request.Context(), models.JobTypeDetectDuplicates, merchant.ID,
			map[string]string{"merchant_id": merchant.ID.String()})
		if err != nil {
			h.log.Warn().Err(err).Msg("wp sync: failed to dispatch detect job")
		} else {
			jobIDStr = jobID.String()
		}
	}

	c.JSON(http.StatusOK, dto.WPSyncResponse{
		Ingested: ingested,
		JobID:    jobIDStr,
	})
}

// splitRefreshToken parses a "<merchant_uuid>:<raw_token>" refresh token.
func splitRefreshToken(token string) (uuid.UUID, string, bool) {
	const uuidLen = 36
	if len(token) < uuidLen+2 || token[uuidLen] != ':' {
		return uuid.Nil, "", false
	}
	id, err := uuid.Parse(token[:uuidLen])
	if err != nil {
		return uuid.Nil, "", false
	}
	return id, token[uuidLen+1:], true
}
