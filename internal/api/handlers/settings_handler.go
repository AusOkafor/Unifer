package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"merger/backend/internal/middleware"
	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	billingpkg "merger/backend/internal/services/billing"
)

type SettingsHandler struct {
	settingsRepo repository.SettingsRepository
	log          zerolog.Logger
}

func NewSettingsHandler(settingsRepo repository.SettingsRepository, log zerolog.Logger) *SettingsHandler {
	return &SettingsHandler{settingsRepo: settingsRepo, log: log}
}

func (h *SettingsHandler) Get(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	s, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID)
	if err != nil {
		s = models.DefaultSettings(merchant.ID)
	}

	if enforceSettingsGates(s) {
		// Persist corrected values so the scheduler and webhook handler (which read
		// raw DB) never see stale feature flags from a prior higher-tier plan.
		if upsertErr := h.settingsRepo.Upsert(c.Request.Context(), s); upsertErr != nil {
			h.log.Warn().Err(upsertErr).Str("shop", merchant.ShopDomain).Msg("settings get: failed to persist gate enforcement")
		}
	}
	c.JSON(http.StatusOK, settingsResponse(s))
}

type updateSettingsRequest struct {
	// Existing
	AutoDetect           *bool `json:"auto_detect"`
	ConfidenceThreshold  *int  `json:"confidence_threshold"`
	RetentionDays        *int  `json:"retention_days"`
	NotificationsEnabled *bool `json:"notifications_enabled"`
	// Detection
	ScanFrequency *string `json:"scan_frequency"`
	ScanHour      *int    `json:"scan_hour"`
	SignalEmail   *bool   `json:"signal_email"`
	SignalPhone   *bool   `json:"signal_phone"`
	SignalAddress *bool   `json:"signal_address"`
	SignalName    *bool   `json:"signal_name"`
	// Risk & Safety
	RiskPolicy            *string `json:"risk_policy"`
	RequireAnchor         *bool   `json:"require_anchor"`
	WeakLinkProtection    *bool   `json:"weak_link_protection"`
	BlockDifferentCountry *bool   `json:"block_different_country"`
	BlockFraudTags        *bool   `json:"block_fraud_tags"`
	BlockDisabledAccounts *bool   `json:"block_disabled_accounts"`
	// Bulk Merge
	BulkMaxBatch       *int  `json:"bulk_max_batch"`
	BulkDelayMs        *int  `json:"bulk_delay_ms"`
	BulkRequirePreview *bool `json:"bulk_require_preview"`
	// Granular notifications
	NotifyNewDuplicates *bool `json:"notify_new_duplicates"`
	NotifyHighRisk      *bool `json:"notify_high_risk"`
	NotifyBulkComplete  *bool `json:"notify_bulk_complete"`
	NotifyFailures      *bool `json:"notify_failures"`
	// Developer
	DebugMode *bool `json:"debug_mode"`
	// Behavioral signals
	EnableBehavioralSignals *bool `json:"enable_behavioral_signals"`
}

func (h *SettingsHandler) Update(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	var req updateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID)
	if err != nil {
		s = models.DefaultSettings(merchant.ID)
	}

	// Apply only provided fields (nil = unchanged).
	if req.AutoDetect != nil {
		s.AutoDetect = *req.AutoDetect
	}
	if req.ConfidenceThreshold != nil {
		t := *req.ConfidenceThreshold
		if t < 0 || t > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "confidence_threshold must be 0–100"})
			return
		}
		s.ConfidenceThreshold = t
	}
	if req.RetentionDays != nil && *req.RetentionDays > 0 {
		s.RetentionDays = *req.RetentionDays
	}
	if req.NotificationsEnabled != nil {
		s.NotificationsEnabled = *req.NotificationsEnabled
	}
	if req.ScanFrequency != nil {
		s.ScanFrequency = *req.ScanFrequency
	}
	if req.ScanHour != nil {
		h := *req.ScanHour
		if h < 0 || h > 23 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "scan_hour must be 0–23"})
			return
		}
		s.ScanHour = h
	}
	if req.SignalEmail != nil {
		s.SignalEmail = *req.SignalEmail
	}
	if req.SignalPhone != nil {
		s.SignalPhone = *req.SignalPhone
	}
	if req.SignalAddress != nil {
		s.SignalAddress = *req.SignalAddress
	}
	if req.SignalName != nil {
		s.SignalName = *req.SignalName
	}
	if req.RiskPolicy != nil {
		s.RiskPolicy = *req.RiskPolicy
	}
	if req.RequireAnchor != nil {
		s.RequireAnchor = *req.RequireAnchor
	}
	if req.WeakLinkProtection != nil {
		s.WeakLinkProtection = *req.WeakLinkProtection
	}
	if req.BlockDifferentCountry != nil {
		s.BlockDifferentCountry = *req.BlockDifferentCountry
	}
	if req.BlockFraudTags != nil {
		s.BlockFraudTags = *req.BlockFraudTags
	}
	if req.BlockDisabledAccounts != nil {
		s.BlockDisabledAccounts = *req.BlockDisabledAccounts
	}
	if req.BulkMaxBatch != nil {
		s.BulkMaxBatch = *req.BulkMaxBatch
	}
	if req.BulkDelayMs != nil {
		s.BulkDelayMs = *req.BulkDelayMs
	}
	if req.BulkRequirePreview != nil {
		s.BulkRequirePreview = *req.BulkRequirePreview
	}
	if req.NotifyNewDuplicates != nil {
		s.NotifyNewDuplicates = *req.NotifyNewDuplicates
	}
	if req.NotifyHighRisk != nil {
		s.NotifyHighRisk = *req.NotifyHighRisk
	}
	if req.NotifyBulkComplete != nil {
		s.NotifyBulkComplete = *req.NotifyBulkComplete
	}
	if req.NotifyFailures != nil {
		s.NotifyFailures = *req.NotifyFailures
	}
	if req.DebugMode != nil {
		s.DebugMode = *req.DebugMode
	}
	if req.EnableBehavioralSignals != nil {
		s.EnableBehavioralSignals = *req.EnableBehavioralSignals
	}

	enforceSettingsGates(s)

	if err := h.settingsRepo.Upsert(c.Request.Context(), s); err != nil {
		h.log.Error().Err(err).Msg("update settings")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save settings"})
		return
	}

	c.JSON(http.StatusOK, settingsResponse(s))
}

// enforceSettingsGates zeros out any feature flags that the merchant's current plan
// does not include. Returns true if any field was changed (caller should persist).
func enforceSettingsGates(s *models.MerchantSettings) bool {
	changed := false
	if !billingpkg.IsFeatureEnabled(s.Plan, billingpkg.FeatureOrderIntelligence) {
		if s.EnableBehavioralSignals {
			s.EnableBehavioralSignals = false
			changed = true
		}
	}
	if !billingpkg.IsFeatureEnabled(s.Plan, billingpkg.FeatureAutoDetect) {
		if s.AutoDetect || s.ScanFrequency != "manual" {
			s.AutoDetect = false
			s.ScanFrequency = "manual"
			changed = true
		}
	}
	if !billingpkg.IsFeatureEnabled(s.Plan, billingpkg.FeatureBulkMerge) {
		if s.BulkMaxBatch != 10 || s.BulkDelayMs != 500 || !s.BulkRequirePreview {
			s.BulkMaxBatch = 10
			s.BulkDelayMs = 500
			s.BulkRequirePreview = true
			changed = true
		}
	}
	return changed
}

func settingsResponse(s *models.MerchantSettings) gin.H {
	return gin.H{
		"auto_detect":            s.AutoDetect,
		"confidence_threshold":   s.ConfidenceThreshold,
		"retention_days":         s.RetentionDays,
		"notifications_enabled":  s.NotificationsEnabled,
		"scan_frequency":         s.ScanFrequency,
		"scan_hour":              s.ScanHour,
		"signal_email":           s.SignalEmail,
		"signal_phone":           s.SignalPhone,
		"signal_address":         s.SignalAddress,
		"signal_name":            s.SignalName,
		"risk_policy":            s.RiskPolicy,
		"require_anchor":         s.RequireAnchor,
		"weak_link_protection":   s.WeakLinkProtection,
		"block_different_country": s.BlockDifferentCountry,
		"block_fraud_tags":       s.BlockFraudTags,
		"block_disabled_accounts": s.BlockDisabledAccounts,
		"bulk_max_batch":         s.BulkMaxBatch,
		"bulk_delay_ms":          s.BulkDelayMs,
		"bulk_require_preview":   s.BulkRequirePreview,
		"notify_new_duplicates":  s.NotifyNewDuplicates,
		"notify_high_risk":       s.NotifyHighRisk,
		"notify_bulk_complete":   s.NotifyBulkComplete,
		"notify_failures":        s.NotifyFailures,
		"debug_mode":                s.DebugMode,
		"enable_behavioral_signals": s.EnableBehavioralSignals,
	}
}
