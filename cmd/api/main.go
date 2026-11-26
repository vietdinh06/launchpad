package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type Application struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Server struct {
	db    *pgxpool.Pool
	queue *redis.Client
}

func main() {
	deployToken := os.Getenv("LAUNCHPAD_DEPLOY_TOKEN")
	if deployToken == "" {
		log.Fatal("LAUNCHPAD_DEPLOY_TOKEN not set")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Unable to create connection pool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Unable to ping database: %v\n", err)
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	defer redisClient.Close()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Printf("Redis unavailable at startup: %v", err)
	}

	server := &Server{
		db:    pool,
		queue: redisClient,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", server.health)
	mux.HandleFunc("POST /applications", requireDeployToken(deployToken, server.createApplication))
	mux.HandleFunc("GET /applications", server.listApplications)
	mux.HandleFunc("GET /applications/{id}", server.getApplication)
	mux.HandleFunc("POST /applications/{id}/releases", requireDeployToken(deployToken, server.createRelease))
	mux.HandleFunc("GET /applications/{id}/releases", server.listReleases)
	mux.HandleFunc("POST /releases/{id}/deployments", requireDeployToken(deployToken,server.createDeployments))
	mux.HandleFunc("GET /deployments/{id}", server.getDeployments)

	httpServer := &http.Server{
		Addr:              "127.0.0.1:8081",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Println("Launchpad API listening on :8081")
	log.Fatal(httpServer.ListenAndServe())
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(
		r.Context(), 3*time.Second,
	)
	defer cancel()

	if err := s.db.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

func (s *Server) createApplication(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"name"`
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "expected one JSON object", http.StatusBadRequest)
		return
	}

	request.Name = strings.TrimSpace(request.Name)

	if request.Name == "" || len(request.Name) > 63 {
		http.Error(w, "request length invalid", http.StatusBadRequest)
		return
	}

	app := Application{
		ID:   uuid.NewString(),
		Name: request.Name,
	}

	ctx, cancel := context.WithTimeout(
		r.Context(), 5*time.Second,
	)
	defer cancel()
	err := s.db.QueryRow(
		ctx,
		`INSERT INTO applications (id, name)
         VALUES ($1, $2)
         RETURNING created_at`,
		app.ID,
		app.Name,
	).Scan(&app.CreatedAt)

	if err != nil {
		var pgErr *pgconn.PgError

		if errors.As(err, &pgErr) &&
			pgErr.Code == "23505" {
			http.Error(w, "application already exists",
				http.StatusConflict)
			return
		}

		log.Printf("insert application: %v", err)
		http.Error(w, "database error",
			http.StatusInternalServerError)
		return
	}

	w.Header().Set("Location", "/applications/"+app.ID)
	writeJSON(w, http.StatusCreated, app)
}

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(
		r.Context(), 5*time.Second,
	)
	defer cancel()

	rows, err := s.db.Query(ctx,
		`SELECT id, name, created_at
	FROM applications
	ORDER BY created_at ASC, id DESC`)

	if err != nil {
		log.Printf("query applications: %v", err)
		http.Error(w, "database error",
			http.StatusInternalServerError)
		return
	}

	defer rows.Close()

	apps := []Application{}

	for rows.Next() {
		var app Application

		if err := rows.Scan(
			&app.ID, &app.Name, &app.CreatedAt,
		); err != nil {
			log.Printf("scan application: %v", err)
			http.Error(w, "database error",
				http.StatusInternalServerError)
			return
		}

		apps = append(apps, app)
	}

	if err := rows.Err(); err != nil {
		log.Printf("iterate applications: %v", err)
		http.Error(w, "database error",
			http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, apps)

}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, fmt.Sprintf("JSON encoding error: %v", err),
			http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
