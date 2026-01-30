package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/jackc/pgx/v4/pgxpool"
)

// AggregateResult holds the combined objects gathered from the various DBs.
type AggregateResult map[string]interface{}

// queryJSON runs a SQL that returns a single json value and unmarshals it.
func queryJSON(ctx context.Context, pool *pgxpool.Pool, sql string, args ...interface{}) (interface{}, error) {
	var raw []byte
	err := pool.QueryRow(ctx, sql, args...).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var out interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// connectPool connects using an env var and returns the pool or error.
func connectPool(ctx context.Context, env string) (*pgxpool.Pool, error) {
	dsn := os.Getenv(env)
	if dsn == "" {
		return nil, fmt.Errorf("env %s not set", env)
	}
	pool, err := pgxpool.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return pool, nil
}

// AggregateForUser attempts to collect profile, experiences, projects,
// publications and resume history for the given user id (text uuid).
// It is intentionally best-effort: missing tables or columns will be skipped
// and the function will return whatever it could fetch.
func AggregateForUser(ctx context.Context, userID string) (AggregateResult, error) {
	res := AggregateResult{}

	// Auth DB: users, profiles
	if pool, err := connectPool(ctx, "AUTH_DATABASE_URL"); err == nil {
		defer pool.Close()
		if v, err := queryJSON(ctx, pool, `SELECT to_jsonb(u) FROM users u WHERE u.id::text=$1 LIMIT 1`, userID); err == nil {
			res["user"] = v
		}
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(p)), '[]') FROM profiles p WHERE p.user_id::text=$1`, userID); err == nil {
			// store raw profiles
			res["profiles"] = v
			// normalize profile.social_links if present and stored as string
			if arr, ok := v.([]interface{}); ok {
				for i, it := range arr {
					if pm, ok := it.(map[string]interface{}); ok {
						if slRaw, has := pm["social_links"]; has {
							switch s := slRaw.(type) {
							case string:
								var parsed map[string]string
								if err := json.Unmarshal([]byte(s), &parsed); err == nil {
									out := map[string]interface{}{}
									for k, vv := range parsed {
										out[k] = vv
									}
									pm["social_links"] = out
								}
							case map[string]interface{}:
								// already an object, keep as-is
							default:
								if b, err := json.Marshal(s); err == nil {
									var parsed map[string]interface{}
									if err2 := json.Unmarshal(b, &parsed); err2 == nil {
										pm["social_links"] = parsed
									}
								}
							}
						}
						arr[i] = pm
					} else {
						arr[i] = it
					}
				}
				res["profiles"] = arr
			}
		}
	}

	// Jobs DB: resumes, resume_jobs, job_applications
	if pool, err := connectPool(ctx, "JOBS_DATABASE_URL"); err == nil {
		defer pool.Close()
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(r)), '[]') FROM resumes r WHERE r.user_id::text=$1`, userID); err == nil {
			res["resumes"] = v
		}
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(j)), '[]') FROM job_applications j WHERE j.user_id::text=$1`, userID); err == nil {
			res["job_applications"] = v
		}
	}

	// Posts DB: projects, publications, case studies, impact metrics
	if pool, err := connectPool(ctx, "POSTS_DATABASE_URL"); err == nil {
		defer pool.Close()
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(p)), '[]') FROM projects p WHERE p.owner_id::text=$1 OR p.user_id::text=$1`, userID); err == nil {
			res["projects"] = v
		}
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(c)), '[]') FROM case_studies c WHERE c.author_id::text=$1 OR c.user_id::text=$1`, userID); err == nil {
			res["case_studies"] = v
		}
		// publications table uses `user_id`; some schemas do not have `author_id`.
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(pub)), '[]') FROM publications pub WHERE pub.user_id::text=$1`, userID); err == nil {
			res["publications"] = v
		}
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(m)), '[]') FROM impact_metrics m WHERE m.user_id::text=$1`, userID); err == nil {
			res["impact_metrics"] = v
		}
	}

	// Management DB: experiences, testimonials, technologies, projects, case studies
	if pool, err := connectPool(ctx, "MGMT_DATABASE_URL"); err == nil {
		defer pool.Close()
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(e)), '[]') FROM experiences e WHERE e.user_id::text=$1`, userID); err == nil {
			res["experiences"] = v
		}
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(t)), '[]') FROM testimonials t WHERE t.user_id::text=$1 OR t.author_id::text=$1`, userID); err == nil {
			res["testimonials"] = v
		}
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(pt)), '[]') FROM project_technologies pt WHERE pt.user_id::text=$1 OR pt.project_owner_id::text=$1`, userID); err == nil {
			res["project_technologies"] = v
		}
		// Fetch project case studies and store as "projects" for resume generation
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(cs)), '[]') FROM project_case_studies cs WHERE cs.project_id IN (SELECT id FROM projects WHERE user_id::text=$1)`, userID); err == nil {
			res["projects"] = v
		}
		// Attempt to fetch certifications from the management DB (optional)
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(c)), '[]') FROM certifications c WHERE c.user_id::text=$1`, userID); err == nil {
			res["certifications"] = v
		}

		// Attempt to fetch extras from the management DB (optional)
		if v, err := queryJSON(ctx, pool, `SELECT coalesce(json_agg(row_to_json(e)), '[]') FROM extras e WHERE e.user_id::text=$1`, userID); err == nil {
			res["extras"] = v
		}
	}

	return res, nil
}

// GetJobApplicationByID fetches a single job_application row by its text uuid.
func GetJobApplicationByID(ctx context.Context, id string) (interface{}, error) {
	// connect to Jobs DB and fetch single json object
	if pool, err := connectPool(ctx, "JOBS_DATABASE_URL"); err == nil {
		defer pool.Close()
		var raw []byte
		err := pool.QueryRow(ctx, `SELECT to_jsonb(j) FROM job_applications j WHERE j.id::text=$1 LIMIT 1`, id).Scan(&raw)
		if err != nil {
			return nil, err
		}
		var out interface{}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return out, nil
	} else {
		return nil, err
	}
}
