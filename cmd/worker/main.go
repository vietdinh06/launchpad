package main

import (
    "context"
    "errors"
    "fmt"
    "log"
    "os"
    "os/signal"
    "strings"
    "syscall"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/moby/moby/client"
    "github.com/redis/go-redis/v9"

    "launchpad/internal/deployment"
    "launchpad/internal/runtime"
    dockerruntime "launchpad/internal/runtime/docker"
)

const (
    streamName = "launchpad:deployments"
    groupName  = "launchpad-workers"
)

type Worker struct {
    db       *pgxpool.Pool
    queue    *redis.Client
    executor *deployment.Executor
    consumer string
}

func main() {
    if err := run(); err != nil {
        log.Fatal(err)
    }
}

func run() error {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	dbURL := os.Getenv("DATABASE_URL")
    if dbURL == "" {
        return fmt.Errorf("DATABASE_URL is not set")
    }

    db, err := pgxpool.New(ctx, dbURL)

    if err != nil {
        return err
    }
    defer db.Close()

    if err := db.Ping(ctx); err != nil {
        return fmt.Errorf("PostgreSQL: %w", err)
    }

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	queue := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	defer queue.Close()

	dockerClient, err := client.New(client.FromEnv)
	if err != nil {
        return err
    }
    defer dockerClient.Close()

	rt := dockerruntime.New(dockerClient)

	worker := &Worker{
		db: db,
		queue: queue,
		executor: deployment.NewExecutor(rt),
		consumer: uuid.NewString(),
	}

	// Create the consumer group if it doesn't exist.
    err = queue.XGroupCreateMkStream(
        ctx, streamName, groupName, "0",
    ).Err()

    if err != nil &&
        !strings.Contains(err.Error(), "BUSYGROUP") {
        log.Printf("Redis group setup: %v", err)
    }

	log.Println("Launchpad deployment worker started")

    return worker.loop(ctx)
}

func (w *Worker) loop(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}

		if err := w.scanPending(ctx); err != nil {
			log.Printf("scan pending jobs: %v", err)
		}

		streams, err := w.queue.XReadGroup(
			ctx,
			&redis.XReadGroupArgs{
				Group:    groupName,
                Consumer: w.consumer,
                Streams:  []string{streamName, ">"},
                Count:    10,
                Block:    2 * time.Second,
			},
		).Result()

        if errors.Is(err, redis.Nil) {
            continue
        }

		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			log.Printf("Redis read: %v", err)

            select {
            case <-ctx.Done():
                return nil
            case <-time.After(2 * time.Second):
            }
            continue
		}

		for _, stream := range streams {
			for _, message := range stream.Messages {
                jobID, ok := message.Values["job_id"].(string)

				if !ok {
                    log.Printf("invalid message: %s", message.ID)
                    // Skip poison messages.
                    _ = w.ack(ctx, message.ID)
                    continue
                }

                if err := w.process(ctx, jobID); err != nil {
                    log.Printf("process job %s: %v", jobID, err)
                    // Leave message unacknowledged.
                    continue
                }

                if err := w.ack(ctx, message.ID); err != nil {
                    log.Printf("ack message: %v", err)
                }
			}
		}
	}
}

func (w *Worker) ack(ctx context.Context, messageID string) error {
	return w.queue.XAck(
		ctx,
		streamName,
		groupName,
		messageID,
	).Err()
}

func (w *Worker) scanPending(ctx context.Context) error {
	rows, err := w.db.Query(
		ctx,
		`SELECT id
         FROM deployment_jobs
         WHERE status = 'pending'
         ORDER BY created_at, id
         LIMIT 20`,
	)

	if err != nil {
		return err
	}

	var ids []string

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}

	err = rows.Err()
    rows.Close()
    if err != nil {
        return err
    }

	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := w.process(ctx, id); err != nil {
		 	log.Printf("reconcile job %s: %v", id, err)
		}
	}

	return nil
}

func (w *Worker) process(ctx context.Context, jobID string) error {
	var req runtime.DeployRequest

    err := w.db.QueryRow(ctx,
        `UPDATE deployment_jobs AS j
         SET status = 'running',
             updated_at = NOW()
         FROM releases AS r
         WHERE j.id = $1
           AND j.release_id = r.id
           AND j.status = 'pending'
         RETURNING
             j.id,
             j.release_id,
             r.application_id,
             r.image_ref,
             r.container_port`,
        jobID,
    ).Scan(
        &req.JobID,
        &req.ReleaseID,
        &req.ApplicationID,
        &req.ImageRef,
        &req.ContainerPort,
    )

	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}

	if err != nil {
        return fmt.Errorf("claim job: %w", err)
	}

	req.HostPort = 0

    log.Printf("Deploying job %s, image %s",
        jobID, req.ImageRef)

	deployCtx, cancel := context.WithTimeout(ctx, 2*time.Minute,)

	instance, deployErr := w.executor.Execute(deployCtx, req,)
	cancel()

	status := "succeeded"
	var errorMessage *string
	var containerID *string

	if instance.ID != "" {
		containerID = &instance.ID
	}

	if deployErr != nil {
		status = "failed"
		message := deployErr.Error()
		errorMessage = &message
		log.Printf("Deployment %s failed: %v",
            jobID, deployErr)
	}

	finishCtx, finishCancel := context.WithTimeout(
		context.Background(), 5*time.Second,
	)
	defer finishCancel()


	tag, err := w.db.Exec(
		finishCtx,
		`UPDATE deployment_jobs
		SET status = $1,
			container_id = $2,
			error_message = $3,
			updated_at = NOW()
		WHERE id = $4
			AND status = 'running'`,
		status,
		containerID,
		errorMessage,
		jobID,
	)

	if err != nil {
        return fmt.Errorf("finish job: %w", err)
	}

	if tag.RowsAffected() != 1 {
        return fmt.Errorf(
            "job %s was not in running state", jobID,
        )
	}

	log.Print("Job %s finished: %s", jobID, status)

	return nil
}