# Ticker

Foundation for the ticket reservation platform described in [PLAN.md](PLAN.md).
Current scope: repository structure, containers, infrastructure wiring, and a minimal web shell.
No domain tables, booking flows, expiration processing, or test suites are implemented.

## Start with Docker

Requirements: Docker Desktop running with Linux containers and Docker Compose v2.

```powershell
Copy-Item .env.example .env
docker compose up --build -d --wait
```

Open http://localhost:8080. All published ports bind to the local machine only.

```powershell
Invoke-RestMethod http://localhost:8080/api/healthz
Invoke-RestMethod http://localhost:8080/api/readyz
docker compose ps
docker compose logs -f api worker
```

The API exposes liveness at `/healthz`, database readiness at `/readyz`, and Go/process metrics at `/metrics`.
The gateway strips `/api` when forwarding requests to the API.
The worker connects to PostgreSQL, reports infrastructure readiness, then waits for shutdown; it does not expire reservations yet.

Stop services while keeping database data:

```powershell
docker compose down
```

Do not use `down -v` unless you intentionally want to delete persistent data.

## Optional observability

```powershell
docker compose --profile observability up --build -d --wait
```

- Prometheus: http://localhost:9090
- Grafana: http://localhost:3000
- Development Grafana credentials are configured in `.env`; the example uses `admin` / `ticker_dev`.

Grafana has a provisioned Prometheus datasource. Domain metrics and dashboards will be added with later features.

## Local development

Requirements: Go 1.26+, Node.js 24, npm, and the Docker database.
The Vite requirement is documented in the [official guide](https://vite.dev/guide/).

```powershell
docker compose up -d db
$env:DATABASE_URL = 'postgres://ticker:ticker_dev@127.0.0.1:5432/ticker?sslmode=disable'
$env:HTTP_ADDR = ':8080'
go run ./cmd/api
```

Use another terminal for the frontend:

```powershell
Set-Location web
npm ci
npm run dev
```

Vite serves on http://localhost:5173 and forwards `/api` to the local API.
Stop the Compose gateway first if it occupies port 8080, or set `HTTP_ADDR` and the Vite proxy target to another port.
If you change the example database credentials, update `DATABASE_URL` accordingly. Go does not load `.env` automatically.

For the worker, run `go run ./cmd/worker` in a terminal with `DATABASE_URL` set.

## Checks without tests

```powershell
./scripts/check.ps1
```

This checks formatting, Go static analysis and compilation, the frontend production build, and the Compose configuration.
It does not create or run any tests. See [AGENTS.md](AGENTS.md) for the explicit restriction.

## Repository layout

```text
cmd/
  api/                       API entry point
  worker/                    worker process scaffold
  seed/                      reserved for deterministic seed tooling
  audit/                     reserved for invariant auditing
internal/
  platform/{config,database,httpserver}/
  identity/                  domain placeholders only
  catalog/
  reservation/
  booking/
db/{migrations,queries}/     domain SQL will be added later
web/                         React + TypeScript + Vite shell
infra/{nginx,prometheus,grafana}/
tests/                       reserved; no test suites
bench/                       reserved; no benchmarks
scripts/                     PowerShell validation commands
docs/                        infrastructure notes
```

The migration container is configured under the `tools` profile, but there are no SQL migrations yet.
Do not run `docker compose run --rm migrate up` until the first migration is added.
Seed, audit, test, and benchmark commands in PLAN.md are future milestones.

## Configuration

Copy `.env.example` to `.env` and change development credentials or published ports there.
Compose uses the file for substitution; `.env` is ignored by Git and excluded from builds.
The database volume persists between restarts. Changing `POSTGRES_PASSWORD` does not change credentials in an existing volume.

PostgreSQL 18 stores data beneath `/var/lib/postgresql`; the volume follows the [official image layout](https://hub.docker.com/_/postgres).
Image versions are selected by release series or explicit tags; Go dependencies and npm packages are locked by `go.sum` and `package-lock.json`.
This is a local development environment. TLS, deployment credentials, and production infrastructure remain future work.
