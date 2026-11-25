package main

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"log"
	"net/http"
	"time"
)

type DeploymentJob struct {
	ID           string    `json:"id"`
	ReleaseID    string    `json:"release_id"`
	Status       string    `json:"status"`
	ContainerID  *string   `json:"container_id"`
	ErrorMessage *string   `json:"error_message"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (s *Server) createDeployments(w http.ResponseWriter, r *http.Request) {
	releaseID := r.PathValue("id")

	if _, err := uuid.Parse(releaseID); err != nil {
		http.Error(w, "invalid deployment ID",
			http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(
		r.Context(), 5*time.Second,
	)
	defer cancel()

	job := DeploymentJob{
		ID:        uuid.NewString(),
		ReleaseID: releaseID,
	}

	err := s.db.QueryRow(
		ctx,
		`
		INSERT into deployment_jobs (id, release_id)
		SELECT $1, id
		FROM releases
		WHERE id = $2
		RETURNING status, created_at, updated_at`,
		job.ID, releaseID,
	).Scan(
		&job.Status,
		&job.CreatedAt,
		&job.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "deployment not found",
			http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("create deployment: %v", err)
		http.Error(w, "database error",
			http.StatusInternalServerError)
		return
	}

	_, err = s.queue.XAdd(ctx, &redis.XAddArgs{
		Stream: "launchpad:deployments",
		Values: map[string]any{
			"job_id": job.ID,
		},
	}).Result()

	if err != nil {
		log.Printf(
			"Redis notification failed for job %s: %v",
			job.ID, err,
		)
	}

	w.Header().Set("Location", "/deployments/"+job.ID)
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) getDeployments(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")

	if _, err := uuid.Parse(jobID); err != nil {
		http.Error(w, "invalid deployment ID",
			http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(
		r.Context(), 5*time.Second,
	)
	defer cancel()

	var job DeploymentJob
	var containerID sql.NullString
	var errorMessage sql.NullString

	err := s.db.QueryRow(
		ctx,
		`
		SELECT id, release_id, status, container_id, error_message, created_at, updated_at
		FROM deployment_jobs
		WHERE id = $1
		`,
		jobID,
	).Scan(
		&job.ID,
		&job.ReleaseID,
		&job.Status,
		&containerID,
		&errorMessage,
		&job.CreatedAt,
		&job.UpdatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "deployment not found",
			http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("get deployment: %v", err)
		http.Error(w, "database error",
			http.StatusInternalServerError)
		return
	}

	if containerID.Valid {
		job.ContainerID = &containerID.String
	}
	if errorMessage.Valid {
		job.ErrorMessage = &errorMessage.String
	}

	writeJSON(w, http.StatusOK, job)
}
