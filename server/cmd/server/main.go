package main

import (
	"context"
	"log"
	"os"
	"time"

	httpadapter "resume-generator/internal/adapter/http"
	repo "resume-generator/internal/adapter/repository"
	"resume-generator/internal/usecase"
	infra "resume-generator/pkg/infrastructure"

	"github.com/gofiber/fiber/v2"
)

func main() {
	ctx := context.Background()

	// infra setup
	jobsPool, err := infra.NewJobsPool(ctx)
	if err != nil {
		log.Printf("warning: jobs DB not available: %v", err)
	}

	renderer := infra.NewChromedpRenderer()

	jobsRepo := repo.NewJobsRepo(jobsPool)
	processor := usecase.NewProcessor(renderer, jobsRepo, "templates")

	app := fiber.New()

	h := httpadapter.NewHandler(processor, jobsRepo)
	app.Post("/jobs/start", h.StartJob)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	go func() {
		if err := app.Listen(":" + port); err != nil {
			log.Fatalf("server failed: %v", err)
		}
	}()

	// keep process alive
	<-time.After(100 * time.Hour)
	_ = ctx
}
