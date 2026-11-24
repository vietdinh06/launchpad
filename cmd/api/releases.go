package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Release struct {
	ID            string    `json:"id"`
	ApplicationID string    `json:"application_id"`
	Version       int       `json:"version"`
	ImageRef      string    `json:"image_ref"`
	CreatedAt     time.Time `json:"created_at"`
	ContainerPort int `json:"container_port"`
}

func (s *Server) createRelease(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")

	if _, err := uuid.Parse(appID); err != nil {
		http.Error(w, "invalid application ID",
			http.StatusBadRequest)
		return
	}

	var req struct {
		ImageRef string `json:"image_ref"`
		ContainerPort int    `json:"container_port"`
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid JSON",
			http.StatusBadRequest)
		return
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "expected one JSON object",
			http.StatusBadRequest)
		return
	}

	req.ImageRef = strings.TrimSpace(req.ImageRef)
	if req.ImageRef == "" || len(req.ImageRef) > 512 {
		http.Error(w, "invalid image reference",
			http.StatusBadRequest)
		return
	}

	if req.ContainerPort == 0 {
    	req.ContainerPort = 80
	}

	if req.ContainerPort < 1 || req.ContainerPort > 65535 {
		http.Error(w, "invalid container port", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(
		r.Context(), 10*time.Second,
	)
	defer cancel()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(context.Background())

	var lockedID string
	err = tx.QueryRow(ctx,
		`SELECT id
         FROM applications
         WHERE id = $1
         FOR UPDATE`,
		appID,
	).Scan(&lockedID)

	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "application not found",
			http.StatusNotFound)
		return
	}

	if err != nil {
		log.Printf("lock application: %v", err)
		http.Error(w, "database error",
			http.StatusInternalServerError)
		return
	}

	var nextVersion int

	err = tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1
         FROM releases
         WHERE application_id = $1`,
		appID,
	).Scan(&nextVersion)

	if err != nil {
		log.Printf("calculate version: %v", err)
		http.Error(w, "database error",
			http.StatusInternalServerError)
		return
	}

	release := Release{
		ID:            uuid.NewString(),
		ApplicationID: appID,
		Version:       nextVersion,
		ImageRef:      req.ImageRef,
		ContainerPort: req.ContainerPort,
	}

	err = tx.QueryRow(ctx,
		`INSERT INTO releases
			(id, application_id, version, image_ref, container_port)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`,
		release.ID,
		release.ApplicationID,
		release.Version,
		release.ImageRef,
		release.ContainerPort,
	).Scan(&release.CreatedAt, &release.ContainerPort)

	if err != nil {
		log.Printf("create release: %v", err)
		http.Error(w, "database error",
			http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		log.Printf("commit release: %v", err)
		http.Error(w, "database error",
			http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, release)
}

func (s *Server) listReleases(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")

	if _, err := uuid.Parse(appID); err != nil {
		http.Error(w, "invalid application ID",
			http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(
		r.Context(), 5*time.Second,
	)
	defer cancel()

	var exists bool

	err := s.db.QueryRow(
		ctx,
		`SELECT EXISTS(
            SELECT 1 FROM applications WHERE id = $1
        )`,
		appID,
	).Scan(&exists)

	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	if !exists {
		http.Error(w, "application not found", http.StatusNotFound)
		return
	}

	rows, err := s.db.Query(
		ctx,
		`
		SELECT id, application_id, version, image_ref, created_at
		FROM releases
		WHERE application_id = $1
		ORDER BY version DESC
		`,
		appID,
	)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	releases := []Release{}

	for rows.Next() {
		var release Release

		if err := rows.Scan(
			&release.ID,
			&release.ApplicationID,
			&release.Version,
			&release.ImageRef,
			&release.CreatedAt,
		); err != nil {
			http.Error(w, "database error",
				http.StatusInternalServerError)
			return
		}

		releases = append(releases, release)
	}

	if err := rows.Err(); err != nil {
		log.Printf("iterate applications: %v", err)
		http.Error(w, "database error",
			http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, releases)
}
