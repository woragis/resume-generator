package infrastructure

import (
	"context"
	"os"

	"github.com/jackc/pgx/v4/pgxpool"
)

func NewJobsPool(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := os.Getenv("JOBS_DATABASE_URL")
	if dsn == "" {
		// try default local postgres
		dsn = "postgres://postgres:password@jobs-db:5432/jobs?sslmode=disable"
	}
	pool, err := pgxpool.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return pool, nil
}
