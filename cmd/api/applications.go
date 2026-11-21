package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, "invalid application ID",
			http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(
		r.Context(), 5*time.Second,
	)
	defer cancel()

	var app Application

	err := s.db.QueryRow(ctx,
		`SELECT id, name, created_at
         FROM applications
         WHERE id = $1`,
		id,
	).Scan(&app.ID, &app.Name, &app.CreatedAt)

	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "application not found",
			http.StatusNotFound)
		return
	}

	if err != nil {
		log.Printf("get application: %v", err)
		http.Error(w, "database error",
			http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, app)
}
