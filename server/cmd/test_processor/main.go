package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"resume-generator/internal/adapter/repository"
	"resume-generator/internal/domain"
	"resume-generator/internal/usecase"
	"resume-generator/pkg/infrastructure"
	"strings"

	"github.com/google/uuid"
)

// Using real renderer and repo implementations for the processor

func startMockAI(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat", func(w http.ResponseWriter, r *http.Request) {
		body, _ := ioutil.ReadAll(r.Body)
		var req map[string]interface{}
		_ = json.Unmarshal(body, &req)
		input, _ := req["input"].(string)

		if input == "" {
			w.WriteHeader(400)
			return
		}
		// crude detection: Enrich requests include the word Enrich
		if contains(input, "Enrich resume") || contains(input, "Enrich resume with overrides") || contains(input, "Enrich resume with") {
			// return an enriched resume JSON (full object)
			enriched := map[string]interface{}{
				"meta":           map[string]interface{}{"name": "Test User", "headline": "Engineer", "contact": map[string]interface{}{"email": "t@example.com"}},
				"summary":        "A short summary that is long enough to pass validation requirements for this test and will be used in the template.",
				"snapshot":       map[string]interface{}{"tech": "Go, Postgres", "achievements": []string{"Delivered a major performance improvement across the pipeline.", "Reduced incident rate by implementing robust retries and alerts.", "Led cross-team initiative to standardize deployments and CI/CD."}, "selected_projects": []string{"Built a real-time data processing pipeline that improved data accuracy and speed.", "Created a microservice architecture that streamlined deployment processes and reduced downtime."}},
				"experience":     []map[string]interface{}{{"company": "Acme", "title": "Engineer", "bullets": []string{"Did things that matter which are long enough to pass the schema."}}},
				"projects":       []map[string]interface{}{{"id": "p1", "title": "P1", "description": "Developed a real-time data processing pipeline that improved data accuracy and speed for analytics and reduced processing time by >50% through algorithmic improvements."}},
				"publications":   []string{"Scaling Event Processing at Nimbus Labs — 2024. Architected event-driven pipelines for low-latency processing.", "Scaling Go Microservices — 2023. Techniques to improve throughput and reduce memory footprint."},
				"certifications": []string{"Certified Kubernetes Administrator"},
				"extras":         "Open-source contributor and speaker.",
			}
			b, _ := json.Marshal(map[string]interface{}{"agent": "mock", "output": string(mustMarshal(enriched))})
			w.Header().Set("Content-Type", "application/json")
			w.Write(b)
			return
		}

		// Default FormatResume response: a full resume without publications/certs/extras
		base := map[string]interface{}{
			"meta":       map[string]interface{}{"name": "Test User", "headline": "Engineer", "contact": map[string]interface{}{"email": "t@example.com"}},
			"summary":    "A short summary that is long enough to pass validation requirements for this test and will be used in the template.",
			"snapshot":   map[string]interface{}{"tech": "Go, Postgres", "achievements": []string{"Delivered a major performance improvement across the pipeline.", "Reduced incident rate by implementing robust retries and alerts.", "Led cross-team initiative to standardize deployments and CI/CD."}, "selected_projects": []string{"Built a real-time data processing pipeline that improved data accuracy and speed.", "Created a microservice architecture that streamlined deployment processes and reduced downtime."}},
			"experience": []map[string]interface{}{{"company": "Acme", "title": "Engineer", "bullets": []string{"Did things that matter which are long enough to pass the schema."}}},
			"projects":   []map[string]interface{}{{"id": "p1", "title": "P1", "description": "Developed a real-time data processing pipeline that improved data accuracy and speed for analytics and reduced processing time by >50% through algorithmic improvements."}},
		}
		b, _ := json.Marshal(map[string]interface{}{"agent": "mock", "output": string(mustMarshal(base))})
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("mock ai server failed: %v", err)
		}
	}()
	return srv
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func mustMarshal(v interface{}) string { b, _ := json.Marshal(v); return string(b) }

func main() {
	// ensure AI client points to our mock server
	os.Setenv("AI_SERVICE_URL", "http://127.0.0.1:8000")

	srv := startMockAI(":8000")
	defer srv.Shutdown(context.Background())

	// create processor with real renderer and a repo wrapper (pool nil for tests)
	r := infrastructure.NewChromedpRenderer()
	repo := repository.NewJobsRepo(nil)
	processor := usecase.NewProcessor(r, repo, "templates", "english")

	// build a job with overrides
	job := &domain.ResumeJob{
		UserID: uuid.MustParse("9136d765-327d-4cf3-bf1c-98aa1449e52d"),
		Profile: map[string]interface{}{
			"publications":   []interface{}{"Scaling Event Processing at Nimbus Labs", "Scaling Go Microservices"},
			"certifications": []interface{}{"Certified Kubernetes Administrator"},
			"extras":         "Open-source contributor and speaker",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := processor.Process(ctx, job); err != nil {
		fmt.Printf("Process failed: %v\n", err)
		return
	}

	fmt.Printf("Process completed. Generated HTML: %v\n", job.Metadata["generated_html"])
}
