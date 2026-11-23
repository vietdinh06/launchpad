# Launchpad

**Deploy and manage containerized applications on your own machine.**

Launchpad is a lightweight, Heroku-inspired platform-as-a-service (PaaS) written in Go. It provides a REST API and a local command-line workflow for registering applications, creating versioned releases, and deploying Docker containers. PostgreSQL persists platform state, while Redis Streams coordinates asynchronous deployment work.

## The flow at a glance

```text
1. Start      docker compose up -d              # PostgreSQL + Redis
2. Run        go run ./cmd/api                   # Launchpad API
3. Work       go run ./cmd/worker                # Deployment worker
4. Register   POST /applications                 # Create an app
5. Release    POST /applications/{id}/releases  # Point to a Docker image
6. Deploy     POST /releases/{id}/deployments   # Queue deployment
7. Inspect    GET /deployments/{id}             # Read deployment status
8. Visit      http://localhost:<host-port>      # Access the application
```

The API responds to a deployment request without waiting for Docker to finish. A background worker executes the deployment, and the deployment record tracks its progress.

## What the MVP does

- **Application registry:** Create and list named applications with stable identifiers.
- **Release tracking:** Associate an application with versioned container image references and runtime configuration.
- **Asynchronous deployments:** Persist pending deployment jobs in PostgreSQL and notify workers through Redis Streams.
- **Docker runtime:** Create and start containers using the Docker Engine API, with the runtime isolated behind a Go interface.
- **Deployment status:** Track pending, running, and failed deployments without relying on terminal output.
- **Local hosting:** Publish application container ports to the host, allowing multiple apps to run on distinct host ports.
- **Operational resilience:** Keep durable job state in PostgreSQL so a worker can reconcile pending work after interruptions.

**Scope:** This is a **single-machine development MVP**, not a production multi-tenant cloud. It does not yet provide automatic HTTPS, custom domains, multi-node scheduling, Kubernetes orchestration, autoscaling, or a hardened security boundary between untrusted applications.

## Architecture

```mermaid
flowchart TD
    Developer[Developer / curl / local CLI] --> API[Go REST API]
    API --> PG[(PostgreSQL\napps, releases, deployment jobs)]
    API --> Redis[(Redis Streams\ndeployment notifications)]
    Redis --> Worker[Go deployment worker]
    Worker <--> PG
    Worker --> Runtime[Runtime interface]
    Runtime --> Docker[Docker Engine]
    Docker --> App1[App container A]
    Docker --> App2[App container B]
    Browser[Browser] -->|localhost:host-port A| App1
    Browser -->|localhost:host-port B| App2
```

### Main components

| Component | Responsibility |
| --- | --- |
| **Go API** | Validates requests, creates application/release/job records, returns deployment identifiers. |
| **PostgreSQL** | Source of truth for applications, releases, deployment jobs, and their lifecycle states. |
| **Redis Streams** | Delivers notifications that new jobs are ready to process; it is **not** the authoritative record of a deployment. |
| **Go worker** | Claims pending jobs, invokes the runtime, and records success or failure. |
| **Runtime interface** | Separates platform-level deployment logic from Docker-specific operations. |
| **Docker Engine** | Runs application containers and publishes ports on the host. |

### What happens during a deployment?

```mermaid
sequenceDiagram
    participant User as Developer
    participant API as Go API
    participant DB as PostgreSQL
    participant Q as Redis Streams
    participant W as Worker
    participant D as Docker
    User->>API: POST /releases/{id}/deployments
    API->>DB: Insert pending deployment job
    API->>Q: Publish job notification
    API-->>User: Deployment ID (queued)
    Q-->>W: Job notification
    W->>DB: Atomically claim pending job
    W->>D: Create and start container
    D-->>W: Container ID or error
    W->>DB: Record running or failed result
    User->>API: GET /deployments/{id}
    API->>DB: Read current status
    API-->>User: Status + runtime details
```

A Redis notification does **not** mean a deployment has succeeded. PostgreSQL holds the job status, allowing workers to recover jobs that were committed but not successfully processed.

## Tech stack

| Layer | Technology |
| --- | --- |
| API, workers, runtime abstraction | Go |
| Container execution and image management | Docker Engine API |
| Platform data | PostgreSQL + `pgx` / `pgxpool` |
| Job notifications | Redis Streams |
| Local supporting services | Docker Compose |
| API format | HTTP + JSON |

## Quick start

### Prerequisites

- Go (use the version specified in `go.mod`)
- Docker Engine or Docker Desktop, with the daemon running
- Docker Compose v2
- `curl` (and optionally `jq` for readable JSON)
- A locally available Docker image for the sample application

### 1. Clone and configure

```bash
git clone https://github.com/<your-username>/launchpad.git
cd launchpad

# Create a local environment file from the repo's example, if present.
cp .env.example .env
```

Provide local connection settings consistent with your `compose.yaml`. For example, **if** Compose exposes PostgreSQL on `5432` and Redis on `6379`:

```dotenv
DATABASE_URL=postgres://launchpad:launchpad@localhost:5432/launchpad?sslmode=disable
REDIS_ADDR=localhost:6379
PORT=8080
```

These are illustrative **local development** values, not production credentials. Replace names, ports, and variables with the configuration your checkout actually uses. Do not commit `.env`.

### 2. Start dependencies

```bash
docker compose up -d

docker compose ps
```

Compose starts the backing services (PostgreSQL and Redis), not necessarily the API and worker. Apply the SQL migrations included in `migrations/` using your project's migration method **before** starting the API. The exact migration command depends on whether the repository includes a migration runner or uses `psql` directly; do not skip schema initialization.

### 3. Start Launchpad

Open two terminals at the repository root. Load your environment variables in each terminal if your program reads them from the shell:

**Terminal A — API**

```bash
set -a
source .env
set +a
go run ./cmd/api
```

**Terminal B — worker**

```bash
set -a
source .env
set +a
go run ./cmd/worker
```

Verify that the API is reachable:

```bash
curl http://localhost:8080/health
```

If your server listens on another port, update the URL throughout the examples.

## Deploy your first application

The MVP deployment API uses an **existing Docker image reference**. Building source code into an image can be done locally with Docker; automated Git-source builds are a later enhancement.

### 1. Build an image

From the directory containing your sample application's `Dockerfile`:

```bash
docker build -t launchpad-demo:latest .
```

For example, the image might run an HTTP server on port `3000`. The image must be available to the Docker daemon used by the Launchpad worker.

### 2. Register the application

```bash
curl -sS -X POST http://localhost:8080/applications \
  -H 'Content-Type: application/json' \
  -d '{"name":"demo-app"}'
```

Copy the returned application `id` into the next step. You can inspect registered apps with:

```bash
curl -sS http://localhost:8080/applications
```

### 3. Create a release

The following payload illustrates the **intended** release fields for the architecture. Align field names with the actual handler before using it unchanged:

```bash
curl -sS -X POST "http://localhost:8080/applications/$APP_ID/releases" \
  -H 'Content-Type: application/json' \
  -d '{
    "image_ref": "launchpad-demo:latest",
    "container_port": 3000
  }'
```

A **release** identifies the image and configuration to run. Creating one does not start a container. Save the returned release `id`.

### 4. Queue a deployment

Choose an available host port different from the API port (for example, `8081`). This illustrative request assumes the deployment handler accepts `host_port`:

```bash
curl -sS -X POST "http://localhost:8080/releases/$RELEASE_ID/deployments" \
  -H 'Content-Type: application/json' \
  -d '{"host_port":8081}'
```

The API creates a pending job. It does not block until the container becomes healthy. Save the returned deployment `id`.

### 5. Check status and open the app

```bash
curl -sS "http://localhost:8080/deployments/$DEPLOYMENT_ID"
```

Once the deployment is reported as running, try:

```bash
curl http://localhost:8081
```

If your image listens on another internal port, set `container_port` accordingly. The **container port** is where the process listens inside Docker; the **host port** is where your machine exposes it. Two applications can use the same internal container port, but they cannot bind the same host port on the same host/IP.

## API reference

The application, release, and deployment endpoints are:

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Check API health. |
| `POST` | `/applications` | Register a new application. |
| `GET` | `/applications` | List applications. |
| `GET` | `/applications/{id}` | Inspect an application. |
| `POST` | `/applications/{id}/releases` | Create a versioned release. |
| `GET` | `/applications/{id}/releases` | List the application's releases. |
| `POST` | `/releases/{id}/deployments` | Enqueue a release deployment. |
| `GET` | `/deployments/{id}` | Inspect a deployment job and its status. |

**API contract note:** The table describes the intended MVP routes. Verify response JSON, supported request fields, error codes, and any additional status/logging endpoints against the implemented handlers; those details are intentionally not invented here.

### Application vs. release vs. deployment

```text
Application: "demo-app"                 logical service identity
    └── Release: "launchpad-demo:v1"     image and runtime configuration
         └── Deployment: job-001         attempt to run this release
    └── Release: "launchpad-demo:v2"
         └── Deployment: job-002         another deployment attempt
```

This separation matters: a **release** describes *what* to run; a **deployment** records *the attempt* to run it. Failures are recorded as deployment outcomes instead of deleting the release's metadata.

## Local CLI workflow

Launchpad's earlier Docker-focused CLI offers direct local container management. If the CLI remains included at the MVP stage, these are its commands:

```bash
launchpad deploy <directory> <host-port> <app-port>
launchpad list
launchpad status <deployment-id>
launchpad logs <deployment-id>
launchpad stop <deployment-id>
```

For example:

```bash
launchpad deploy ./examples/hello-world 8081 3000
launchpad list
launchpad status <deployment-id>
launchpad logs <deployment-id>
launchpad stop <deployment-id>
```

**Important:** This is the **direct Docker CLI path** from the earlier project phase. It is not necessarily integrated with the PostgreSQL/Redis API workflow. Until a unified API-backed CLI is implemented, do not assume `launchpad list` and `GET /deployments/{id}` refer to exactly the same registry.

## Development and verification

```bash
# Compile packages
go build ./...

# Run tests
go test ./...

# Check backing services
docker compose ps

# Inspect running app containers
docker ps

# Stop supporting services when finished
docker compose down
```

Use `docker compose down -v` **only if** you want to delete Compose-managed volumes and lose local database data.

### Troubleshooting

| Symptom | What to check |
| --- | --- |
| API cannot connect to PostgreSQL | Compose service status, `DATABASE_URL`, credentials, migrations. |
| Deployment stays pending | Worker process, Redis address, Redis stream notifications, pending-job reconciliation. |
| Deployment fails immediately | Docker daemon access, image reference, available host port, worker logs. |
| App is running but unreachable | App process binding address (`0.0.0.0` inside its container), `container_port`, host port binding. |
| `address already in use` | Another service/container already owns the chosen host port. |
| Redis restart interrupts notifications | Durable jobs in PostgreSQL should let the worker recover unprocessed work; inspect worker reconciliation. |

## Repository layout

The exact tree may evolve; this is the organization:

```text
launchpad/
├── cmd/
│   ├── api/                 # HTTP API entrypoint
│   ├── worker/              # Async deployment worker
│   └── launchpad/           # Earlier direct Docker CLI (if retained)
├── internal/
│   ├── api/                 # HTTP routes and handlers
│   ├── runtime/             # Runtime abstraction and Docker implementation
│   ├── storage/             # PostgreSQL queries and persistence
│   └── worker/              # Job claiming and execution
├── migrations/              # PostgreSQL schema changes
├── examples/                # Sample containerized applications
├── compose.yaml             # Local PostgreSQL / Redis
├── go.mod
└── README.md
```

## Design decisions

**Why Go?** Go's standard HTTP tooling, concurrency primitives, and Docker SDK support are well suited to a small control plane and separate background workers.

**Why PostgreSQL *and* Redis?** PostgreSQL is durable application and deployment state. Redis Streams is a low-latency wake-up mechanism for workers. A deployment should not disappear just because a Redis notification is missed.

**Why run jobs asynchronously?** Building or starting containers can take longer than a typical API request. Returning a deployment ID immediately lets clients poll for progress and isolates slow runtime operations from HTTP request handling.

**Why a runtime interface?** Application orchestration should not be coupled directly to the Docker client. A later Kubernetes implementation can fit behind the same higher-level deployment contracts.

## Security and limitations

Launchpad is intended for **trusted local development** at this stage. Docker daemon access is highly privileged; do not expose the control-plane API to the public Internet or let untrusted users submit arbitrary images without authentication, authorization, resource isolation, secret handling, and network restrictions. Local database credentials in this README are examples only.

## Project status

This repository is an evolving platform-engineering learning project focused on the mechanics behind PaaS systems: control planes, persistent state, deployment queues, container lifecycle management, and the transition from single-host Docker execution to more sophisticated orchestration.
