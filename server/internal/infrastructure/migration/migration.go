package migration

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v4/pgxpool"
)

// RunMigrations executes all necessary database migrations on startup
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	slog.Info("Starting database migrations")

	migrations := []Migration{
		{
			Name: "add_extras_raw_to_resumes",
			Up: func(ctx context.Context, pool *pgxpool.Pool) error {
				return addExtrasRawToResumes(ctx, pool)
			},
		},
		{
			Name: "add_extras_jsonb_to_resumes",
			Up: func(ctx context.Context, pool *pgxpool.Pool) error {
				return addExtrasJSONBToResumes(ctx, pool)
			},
		},
	}

	for _, m := range migrations {
		if err := m.Up(ctx, pool); err != nil {
			slog.Error("Migration failed", "name", m.Name, "error", err)
			return err
		}
		slog.Info("Migration completed", "name", m.Name)
	}

	slog.Info("All migrations completed successfully")
	return nil
}

// Migration represents a database migration
type Migration struct {
	Name string
	Up   func(ctx context.Context, pool *pgxpool.Pool) error
}

// addExtrasRawToResumes adds the extras_raw TEXT column if it doesn't exist
func addExtrasRawToResumes(ctx context.Context, pool *pgxpool.Pool) error {
	query := `
		ALTER TABLE resumes 
		ADD COLUMN IF NOT EXISTS extras_raw TEXT;
	`

	if _, err := pool.Exec(ctx, query); err != nil {
		// Log the error but don't fail - the column may already exist
		slog.Warn("Error adding extras_raw column (may already exist)", "error", err)
		return nil
	}

	slog.Info("Successfully added extras_raw column to resumes table")
	return nil
}

// addExtrasJSONBToResumes adds the extras JSONB column if it doesn't exist
func addExtrasJSONBToResumes(ctx context.Context, pool *pgxpool.Pool) error {
	query := `
		ALTER TABLE resumes 
		ADD COLUMN IF NOT EXISTS extras JSONB DEFAULT '{}'::jsonb;
	`

	if _, err := pool.Exec(ctx, query); err != nil {
		// Log the error but don't fail - the column may already exist
		slog.Warn("Error adding extras column (may already exist)", "error", err)
		return nil
	}

	slog.Info("Successfully added extras JSONB column to resumes table")
	return nil
}
