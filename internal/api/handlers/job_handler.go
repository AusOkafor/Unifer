package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"merger/backend/internal/api/dto"
	"merger/backend/internal/repository"
	"merger/backend/internal/middleware"
)

type JobHandler struct {
	jobRepo repository.JobRepository
	log     zerolog.Logger
}

func NewJobHandler(jobRepo repository.JobRepository, log zerolog.Logger) *JobHandler {
	return &JobHandler{jobRepo: jobRepo, log: log}
}

func (h *JobHandler) Status(c *gin.Context) {
	merchant := middleware.GetMerchant(c)

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}

	job, err := h.jobRepo.FindByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	if job.MerchantID != merchant.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	c.JSON(http.StatusOK, dto.JobStatusResponse{
		ID:        job.ID.String(),
		Type:      job.Type,
		Status:    job.Status,
		Result:    job.Result,
		Retries:   job.Retries,
		CreatedAt: job.CreatedAt,
		UpdatedAt: job.UpdatedAt,
	})
}
