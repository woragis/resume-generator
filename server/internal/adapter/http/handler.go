package http

import (
	"context"
	"log"
	"time"

	"resume-generator/internal/domain"
	"resume-generator/internal/usecase"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type Handler struct {
	processor *usecase.Processor
	repo      usecase.JobsRepo
}

func NewHandler(p *usecase.Processor, r usecase.JobsRepo) *Handler {
	return &Handler{processor: p, repo: r}
}

type startReq struct {
	UserID           string `json:"userId"`
	JobApplicationID string `json:"jobApplicationId"`
	JobDescription   string `json:"jobDescription,omitempty"`
}

func (h *Handler) StartJob(c *fiber.Ctx) error {
	var req startReq
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid payload"})
	}

	uid, err := uuid.Parse(req.UserID)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid userId"})
	}

	job := &domain.ResumeJob{
		ID:             uuid.New(),
		UserID:         uid,
		JobDescription: req.JobDescription,
		Status:         "pending",
		Metadata:       map[string]interface{}{},
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		Profile:        nil,
	}

	if req.JobApplicationID != "" {
		job.Metadata["job_application_id"] = req.JobApplicationID
	}

	// persist initial job (best-effort)
	if h.repo != nil {
		if err := h.repo.Save(context.Background(), job); err != nil {
			log.Printf("warning: failed to save job: %v", err)
		}
	}

	// spawn background processing
	go func(j *domain.ResumeJob) {
		ctx := context.Background()
		if err := h.processor.Process(ctx, j); err != nil {
			log.Printf("job %s failed: %v", j.ID.String(), err)
		}
	}(job)

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"jobId": job.ID.String(), "status": "started"})
}
