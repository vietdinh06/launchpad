# Launchpad

**Deploy and manage containerized applications on your own machine.**

Launchpad is a lightweight, Heroku-inspired platform-as-a-service (PaaS) written in Go. It provides an authenticated REST API and an API-backed command-line workflow for registering applications, creating versioned releases, and deploying Docker containers. PostgreSQL persists platform state, while Redis Streams coordinates asynchronous deployment work, and Traefik dynamically routes HTTP traffic to running application containers.

## The flow at a glance

```text
1. Start      docker compose up -d
2. Run        go run ./cmd/api
3. Work       go run ./cmd/worker
4. Build      go build -o ./bin/launchpad ./cmd/launchpad
5. Register   ./bin/launchpad apps create demo-app
6. Deploy     ./bin/launchpad deploy --app <app-id> --image launchpad-demo:latest --port 3000
7. Inspect    ./bin/launchpad status <deployment-id>
8. Visit      http://deploy-<deployment-id>.localhost:8000
```

The CLI creates a release and queues a deployment through the API. A background worker executes the deployment, and the deployment record tracks its progress.

## Features

- **Application registry:** Create and list named applications with stable identifiers.
- **Release tracking:** Associate an application with versioned container image references and runtime configuration.
- **Asynchronous deployments:** Persist pending deployment jobs in PostgreSQL and notify workers through Redis Streams.
- **Docker runtime:** Create and start containers using the Docker Engine API, with the runtime isolated behind a Go interface.
- **Deployment status:** Track pending, running, succeeded, and failed deployment jobs without relying on terminal output.
- **Dynamic HTTP routing:** Traefik discovers application containers through Docker labels and routes requests to deployment-specific hostnames on a shared entrypoint.
- **API-backed CLI:** Register and list applications, deploy images, and inspect deployment status from the terminal.
- **Deployment authentication:** Bearer-token middleware protects state-changing API routes; the CLI supplies the deployment token. Authentication behavior is covered by unit tests.
- **Operational resilience:** Keep durable job state in PostgreSQL so a worker can reconcile pending work after interruptions.

**Scope:** This is a **single-machine development MVP**, not a production multi-tenant cloud. It does not yet provide automatic HTTPS, custom domains, multi-node scheduling, Kubernetes orchestration, autoscaling, or a hardened security boundary between untrusted applications.

## Architecture

```mermaid
flowchart TD
    Developer[Developer / curl / API-backed CLI] --> API[Go REST API + auth middleware]
    API --> PG[(PostgreSQL\napps, releases, deployment jobs)]
    API --> Redis[(Redis Streams\ndeployment notifications)]
    Redis --> Worker[Go deployment worker]
    Worker <--> PG
    Worker --> Runtime[Runtime interface]
    Runtime --> Docker[Docker Engine]
    Docker --> App1[App container A]
    Docker --> App2[App container B]
    Browser[Browser] -->|HTTP :8000| Proxy[Traefik reverse proxy]
    Docker -.->|labels and discovery| Proxy
    Proxy -->|shared Docker network| App1
    Proxy -->|shared Docker network| App2
```

### Main components

| Component | Responsibility |
| --- | --- |
| **Go API** | Validates requests, creates application/release/job records, returns deployment identifiers. |
| **PostgreSQL** | Source of truth for applications, releases, deployment jobs, and their lifecycle states. |
| **Redis Streams** | Delivers notifications that new jobs are ready to process; it is **not** the authoritative record of a deployment. |
| **Go worker** | Claims pending jobs, invokes the runtime, and records success or failure. |
| **Runtime interface** | Separates platform-level deployment logic from Docker-specific operations. |
| **Docker Engine** | Runs application containers and attaches routed deployments to a shared network. |
| **Traefik** | Discovers Docker labels and forwards HTTP traffic to the correct container. |
| **CLI** | Communicates with the API and attaches authentication credentials to protected requests. |

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
    W->>D: Create and start container with routing labels
    D-->>W: Container ID or error
    W->>DB: Record succeeded or failed result
    W->>Q: Acknowledge notification (XACK)
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
| HTTP reverse proxy | Traefik |
| Local supporting services | Docker Compose |
| API authentication | Bearer token |
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
PORT=8081
LAUNCHPAD_API_URL=http://localhost:8081
LAUNCHPAD_DEPLOY_TOKEN=replace-with-generated-token
```

Generate a deployment token with `openssl rand -hex 32` and use the same value for the API and CLI. These are illustrative **local development** values, not production credentials. Replace names, ports, and variables with the configuration your checkout actually uses. Do not commit `.env`.

### 2. Start dependencies

```bash
docker compose up -d

docker compose ps
```

Compose starts the backing services (PostgreSQL, Redis, and Traefik), not necessarily the API and worker. Apply the SQL migrations included in `migrations/` using your project's migration method **before** starting the API. The exact migration command depends on whether the repository includes a migration runner or uses `psql` directly; do not skip schema initialization.

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
curl http://localhost:8081/health
```

If your server listens on another port, update the URL throughout the examples.

## Deploy your first application

Launchpad deploys an existing Docker image reference. Automated source builds and registry-backed CI/CD are planned enhancements.

### 1. Build an image

From the directory containing your application's `Dockerfile`:

```bash
docker build -t launchpad-demo:latest .
```

The sample application should listen on `0.0.0.0:3000` inside its container. The image must be available to the Docker daemon used by the worker.

### 2. Build and configure the CLI

From the Launchpad repository root:

```bash
mkdir -p bin
go build -o ./bin/launchpad ./cmd/launchpad
set -a
source .env
set +a
```

The CLI uses `LAUNCHPAD_API_URL` and `LAUNCHPAD_DEPLOY_TOKEN` to communicate with the control plane.

### 3. Register an application

```bash
./bin/launchpad apps create demo-app
./bin/launchpad apps list
```

Copy the returned application UUID.

### 4. Create a release and queue a deployment

```bash
./bin/launchpad deploy --app <app-id> --image launchpad-demo:latest --port 3000
```

The CLI submits the image and runtime configuration to the API and queues deployment work. Copy the returned deployment ID.

### 5. Check status and open the app

```bash
./bin/launchpad status <deployment-id>
curl http://deploy-<deployment-id>.localhost:8000
```

Visit the deployment URL after the job succeeds. Traefik uses the request hostname to choose the target container. Multiple containers can listen on the same internal port without each requiring a separate published host port.

A successful deployment means Docker started the container; it does not independently guarantee application-level readiness.

### How routing works

The worker attaches routed containers to the shared Docker network and supplies Traefik labels describing the hostname, `web` entrypoint, service, and internal container port. Traefik watches Docker and updates its routing configuration dynamically. The local proxy listens on port `8000`, independently of the API on port `8081`. Deployment-specific hostnames are not yet stable application URLs.

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

**API authentication:** State-changing routes require an `Authorization: Bearer <token>` header. The token is configured through `LAUNCHPAD_DEPLOY_TOKEN`. The CLI handles this header automatically.

```bash
curl -sS -X POST http://localhost:8081/applications \
  -H "Authorization: Bearer $LAUNCHPAD_DEPLOY_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"demo-app"}'
```

Consult the handlers for exact request and response schemas; the route table is a high-level reference.

### Application vs. release vs. deployment

```text
Application: "demo-app"                 logical service identity
    └── Release: "launchpad-demo:v1"     image and runtime configuration
         └── Deployment: job-001         attempt to run this release
    └── Release: "launchpad-demo:v2"
         └── Deployment: job-002         another deployment attempt
```

This separation matters: a **release** describes *what* to run; a **deployment** records *the attempt* to run it. Failures are recorded as deployment outcomes instead of deleting the release's metadata.

## Local CLI reference

| Command | Purpose |
| --- | --- |
| `launchpad apps create <name>` | Register an application. |
| `launchpad apps list` | List registered applications. |
| `launchpad deploy --app <uuid> --image <ref> --port <port>` | Create a release and enqueue a deployment. |
| `launchpad status <deployment-id>` | Inspect deployment status and available runtime details. |

Use `./bin/launchpad` when running the locally compiled binary. Name-based deployment lookup, unified log streaming, and rollback commands are planned improvements.

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
| Deployment fails immediately | Docker daemon access, image reference, available host port, worker logs, and Docker network configuration. |
| App is running but unreachable | App process binding address (`0.0.0.0` inside its container), `container_port`, Traefik labels, shared network, and deployment hostname. |
| `address already in use` | Another service/container already owns the chosen host port. |
| API rejects a write request | Check that the API and CLI share the same `LAUNCHPAD_DEPLOY_TOKEN`. |
| Redis restart interrupts notifications | Durable jobs in PostgreSQL should let the worker recover unprocessed work; inspect worker reconciliation. |

## Repository layout

The exact tree may evolve; this is the organization:

```text
launchpad/
├── cmd/
│   ├── api/                 # HTTP API entrypoint
│   ├── worker/              # Async deployment worker
│   └── launchpad/           # API-backed CLI
├── internal/
│   ├── runtime/             # Runtime abstraction and Docker implementation
│   ├── platform/            # Platform data and logic
│   └── deployment/          # Deployment execution
├── migrations/              # PostgreSQL schema changes
├── examples/                # Sample containerized applications
├── compose.yaml             # Local PostgreSQL / Redis / Traefik
├── go.mod
└── README.md
```

## Design decisions

**Why Go?** Go's standard HTTP tooling, concurrency primitives, and Docker SDK support are well suited to a small control plane and separate background workers.

**Why PostgreSQL *and* Redis?** PostgreSQL is durable application and deployment state. Redis Streams is a low-latency wake-up mechanism for workers. A deployment should not disappear just because a Redis notification is missed.

**Why run jobs asynchronously?** Building or starting containers can take longer than a typical API request. Returning a deployment ID immediately lets clients poll for progress and isolates slow runtime operations from HTTP request handling.

**Why a runtime interface?** Application orchestration should not be coupled directly to the Docker client. A later Kubernetes implementation can fit behind the same higher-level deployment contracts.

**Why Traefik?** Dynamic Docker discovery avoids maintaining a separate reverse-proxy configuration for each deployed application, and hostname routing allows many containers to share one HTTP entrypoint.

## Security and limitations

Launchpad is intended for **trusted local development** at this stage. Docker daemon access is highly privileged; do not expose the control-plane API to the public Internet or let untrusted users submit arbitrary images without stronger authorization, resource isolation, secret handling, TLS, and network restrictions. The shared deployment token is a basic authentication boundary, not a multi-user identity or authorization system. Local database credentials in this README are examples only.

## Project status

This repository is an evolving platform-engineering learning project focused on the mechanics behind PaaS systems: control planes, persistent state, deployment queues, container lifecycle management, and the transition from single-host Docker execution to more sophisticated orchestration.

## Roadmap

Future implementations will be introduced in incremental development phases as Launchpad evolves toward a distributed, production-oriented PaaS.

- **CI/CD integration:** GitHub Actions workflows for automated application deployments.
- **Deployment lifecycle:** Stable application URLs, health checks, logs, rollback, and zero-downtime updates.
- **Cloud infrastructure:** Remote hosting, HTTPS, and infrastructure provisioning with Terraform.
- **Container orchestration:** Kubernetes as an additional runtime, multi-node scheduling, and scaling.
- **Observability:** Structured logging, Prometheus metrics, and Grafana dashboards.
- **Security and multi-tenancy:** User identities, role-based access control, secrets management, and workload isolation.
- **Developer experience:** Name-based commands, a management dashboard, and custom domains.
- **AI-assisted operations:** MCP integration and agents for deployment management and troubleshooting.
