package main

import (
    "context"
    "database/sql"
    "errors"
    "log"
    "net/http"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
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

	// writeJSON(w, http.StatusCreated, deployment)
}