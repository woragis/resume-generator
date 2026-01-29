package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"resume-generator/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v4/pgxpool"
)

type JobsRepo struct {
	pool *pgxpool.Pool
}

func NewJobsRepo(pool *pgxpool.Pool) *JobsRepo {
	return &JobsRepo{pool: pool}
}

func (r *JobsRepo) Save(ctx context.Context, j *domain.ResumeJob) error {
	if r.pool == nil {
		return nil
	}

	metaB, _ := json.Marshal(j.Metadata)

	_, err := r.pool.Exec(ctx, `INSERT INTO resume_jobs (id, user_id, job_description, status, metadata, resume_id, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (id) DO UPDATE SET user_id = EXCLUDED.user_id, job_description = EXCLUDED.job_description, status = EXCLUDED.status, metadata = EXCLUDED.metadata, resume_id = EXCLUDED.resume_id, updated_at = EXCLUDED.updated_at`,
		j.ID, j.UserID, j.JobDescription, j.Status, metaB, j.ResumeID, j.CreatedAt, j.UpdatedAt)

	if err != nil {
		return err
	}

	// Best-effort: persist a resume row (including extras_raw and extras JSONB)
	var resumeID uuid.UUID
	if j.ResumeID != nil {
		resumeID = *j.ResumeID
	} else {
		resumeID = uuid.New()
		j.ResumeID = &resumeID
	}

	filePath := ""
	fileName := ""
	fileSize := 0
	if j.Metadata != nil {
		if p, ok := j.Metadata["generated_html"].(string); ok && p != "" {
			filePath = p
			parts := strings.Split(p, "/")
			if len(parts) > 0 {
				fileName = parts[len(parts)-1]
			}
		}
	}

	var extrasRaw string
	var extrasJSON []byte
	if j.Profile != nil {
		if er, ok := j.Profile["extras_raw"].(string); ok {
			extrasRaw = er
		}
		if ex, ok := j.Profile["extras"]; ok {
			if b, e := json.Marshal(ex); e == nil {
				extrasJSON = b
			}
		}
	}

	if _, e := r.pool.Exec(ctx, `INSERT INTO resumes (id, user_id, file_name, file_path, file_size, extras_raw, extras, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO UPDATE SET file_name = EXCLUDED.file_name, file_path = EXCLUDED.file_path, file_size = EXCLUDED.file_size, extras_raw = EXCLUDED.extras_raw, extras = EXCLUDED.extras, updated_at = EXCLUDED.updated_at`,
		resumeID, j.UserID, fileName, filePath, fileSize, extrasRaw, extrasJSON, j.CreatedAt, j.UpdatedAt); e != nil {
		fmt.Printf("jobs_repo: unable to upsert resumes row (non-fatal): %v\n", e)
	}

	return nil
}
