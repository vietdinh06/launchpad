package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrInvalidTransition = errors.New(
	"invalid deployment state transition",
)

func validTransition(from, to string) bool {
	switch from {
	case "pending":
		return to == "running"
	case "running":
		return to == "succeeded" || to == "failed"
	default:
		return false
	}
}

func (s *Server) transitionDeployment(ctx context.Context, jobID string, from string, to string, containerID *string, errorMessage *string) error {
	if _, err := uuid.Parse(jobID); err != nil {
		return fmt.Errorf("invalid job ID: %w", err)
	}

	if !validTransition(from, to) {
		return ErrInvalidTransition
	}

	if to == "succeeded" && (containerID == nil || *containerID == "") {
		return errors.New(
			"successful deployment requires container ID",
		)
	}

	if to == "failed" && (errorMessage == nil || *errorMessage == "") {
		return errors.New(
			"failed deployment requires error message",
		)
	}

	tag, err := s.db.Exec(ctx,
		`UPDATE deployment_jobs
         SET status = $1,
             container_id = COALESCE($2, container_id),
             error_message = COALESCE($3, error_message),
             updated_at = NOW()
         WHERE id = $4 AND status = $5`,
		to,
		containerID,
		errorMessage,
		jobID,
		from,
	)

	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		var status string

		err := s.db.QueryRow(
			ctx,
			`SELECT status
			FROM deployment_jobs
			WHERE id = $1`,
			jobID,
		).Scan(&status)

		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("deployment not found")
		}
		if err != nil {
			return err
		}

		return fmt.Errorf(
			"%w: current status is %s",
			ErrInvalidTransition, status,
		)
	}

	return nil
}
