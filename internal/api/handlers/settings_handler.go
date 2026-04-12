package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"merger/backend/internal/models"
	"merger/backend/internal/repository"
	"merger/backend/internal/middleware"
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

	settings, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID)
	if err != nil {
		// Return defaults if not yet configured
		settings = &models.MerchantSettings{
			MerchantID:           merchant.ID,
			AutoDetect:           true,
			ConfidenceThreshold:  75,
			RetentionDays:        90,
			NotificationsEnabled: true,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"auto_detect":           settings.AutoDetect,
		"confidence_threshold":  settings.ConfidenceThreshold,
		"retention_days":        settings.RetentionDays,
		"notifications_enabled": settings.NotificationsEnabled,
	})
}

type updateSettingsRequest struct {
	AutoDetect           *bool `json:"auto_detect"`
	ConfidenceThreshold  *int  `json:"confidence_threshold"`
	RetentionDays        *int  `json:"retention_days"`
	NotificationsEnabled *bool `json:"notifications_enabled"`
}

func (h *SettingsHandler) Update(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	var req updateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Load existing or use defaults
	settings, err := h.settingsRepo.Get(c.Request.Context(), merchant.ID)
	if err != nil {
		settings = &models.MerchantSettings{
			MerchantID:           merchant.ID,
			AutoDetect:           true,
			ConfidenceThreshold:  75,
			RetentionDays:        90,
			NotificationsEnabled: true,
		}
	}

	if req.AutoDetect != nil {
		settings.AutoDetect = *req.AutoDetect
	}
	if req.ConfidenceThreshold != nil {
		t := *req.ConfidenceThreshold
		if t < 0 || t > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "confidence_threshold must be 0-100"})
			return
		}
		settings.ConfidenceThreshold = t
	}
	if req.RetentionDays != nil && *req.RetentionDays > 0 {
		settings.RetentionDays = *req.RetentionDays
	}
	if req.NotificationsEnabled != nil {
		settings.NotificationsEnabled = *req.NotificationsEnabled
	}

	if err := h.settingsRepo.Upsert(c.Request.Context(), settings); err != nil {
		h.log.Error().Err(err).Msg("update settings")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save settings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "settings updated"})
}
