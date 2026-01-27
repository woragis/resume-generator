package repository

import (
	"context"
	"encoding/json"

	"resume-generator/internal/domain"

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

	return err
}
