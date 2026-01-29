package domain

import (
	"time"

	"github.com/google/uuid"
)

type ResumeJob struct {
	ID             uuid.UUID              `json:"id"`
	UserID         uuid.UUID              `json:"user_id"`
	JobDescription string                 `json:"job_description"`
	Status         string                 `json:"status"`
	Metadata       map[string]interface{} `json:"metadata"`
	ResumeID       *uuid.UUID             `json:"resume_id,omitempty"`
	Language       string                 `json:"language"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	Profile        map[string]interface{} `json:"profile"`
}
