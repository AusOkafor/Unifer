package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/middleware"
	"merger/backend/internal/models"
	"merger/backend/internal/repository"
)

type NotificationHandler struct {
	notifRepo repository.NotificationRepository
	log       zerolog.Logger
}

func NewNotificationHandler(notifRepo repository.NotificationRepository, log zerolog.Logger) *NotificationHandler {
	return &NotificationHandler{notifRepo: notifRepo, log: log}
}

// List returns the merchant's most recent notifications and unread count.
// GET /api/notifications?limit=20
func (h *NotificationHandler) List(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	limit := 20
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 50 {
		limit = l
	}

	ns, err := h.notifRepo.ListByMerchant(c.Request.Context(), merchant.ID, limit)
	if err != nil {
		h.log.Error().Err(err).Msg("notification list")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load notifications"})
		return
	}
	if ns == nil {
		ns = []models.Notification{}
	}

	unread, err := h.notifRepo.UnreadCount(c.Request.Context(), merchant.ID)
	if err != nil {
		// Non-fatal — return list with unread=0 rather than failing
		h.log.Warn().Err(err).Msg("notification unread count")
	}

	c.JSON(http.StatusOK, gin.H{
		"notifications": ns,
		"unread_count":  unread,
	})
}

// MarkRead marks a single notification as read.
// PATCH /api/notifications/:id/read
func (h *NotificationHandler) MarkRead(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid notification id"})
		return
	}

	if err := h.notifRepo.MarkRead(c.Request.Context(), id, merchant.ID); err != nil {
		h.log.Error().Err(err).Str("id", id.String()).Msg("notification mark read")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark notification"})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// MarkAllRead marks all of this merchant's unread notifications as read.
// POST /api/notifications/read-all
func (h *NotificationHandler) MarkAllRead(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	if err := h.notifRepo.MarkAllRead(c.Request.Context(), merchant.ID); err != nil {
		h.log.Error().Err(err).Msg("notification mark all read")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to mark notifications"})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}
