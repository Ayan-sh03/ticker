# Ticker

Ticker is a seat reservation system for events. Customers pick up to six seats, hold them for five minutes, and confirm a booking. Organizers create and publish events, block seats, see who booked, cancel bookings, and read sales insights.

The main goal is correctness under load: a seat is never sold twice, a hold takes all of its seats or none, and every change can be checked afterwards.

## What it does

**Customers**
- Browse published events and see live seat availability.
- Sign up with an emailed six-digit code, or log in.
- Select 1–6 seats and hold them for 5 minutes. If a seat was taken, the UI names it and keeps the others selected.
- Confirm the hold to get a booking and a receipt, or cancel it.
- Mark interest in an event.

**Organizers** (per tenant)
- Create private drafts, edit them, publish them, and delete events that have no reservations.
- Block or unblock up to 200 seats in one all-or-nothing change.
- Page through bookings and holds (open, expired, cancelled) with live counts.
- Cancel a confirmed booking before the event starts. Its seats go back on sale.
- See insights: revenue after the platform fee, sell-through, a funnel from interest to bookings, revenue by day, busiest hours, and time to sell out.

## How it is built

```
browser ──> nginx edge ──> web (static React build)
                       └─> api (Go) ──> PostgreSQL
                                    └─> Redis (rate limits, signup codes)
                worker (Go) ──> PostgreSQL   (releases expired holds)
```

- **Go** `net/http` and `pgx` for the API and the worker. They are separate processes in one module.
- **PostgreSQL** holds all state. Row locks taken in a fixed order decide who gets a seat. Constraints and partial unique indexes back them up.
- **Redis** is used only for rate limits and short-lived signup codes. If it fails, rate limits fall back to memory.
- **React + TypeScript + Vite** for the UI.
- An append-only **audit trail** records every hold, confirmation, expiry, and cancellation. A read-only **audit command** checks the whole database for broken rules.

See [docs/report.md](docs/report.md) for the design, the locking rules, and load-test results.

## Run it locally

You need Docker with Compose, plus Go 1.26 and Node 24 for local checks.

```powershell
Copy-Item .env.example .env          # then set DEMO_PASSWORD and the secrets
docker compose up -d --build
docker compose run --rm migrate      # apply database migrations
docker compose run --rm seed         # demo tenant, event, and accounts
```

Open http://localhost:8080. The demo accounts use `DEMO_PASSWORD` from `.env`:

| Account | Role |
|---|---|
| `organizer.42@demo.ticker.local` | Organizer of "City Concerts" |
| `customer1.42@demo.ticker.local` | Customer |
| `customer2.42@demo.ticker.local` | Customer |

Signup codes are printed in the API log when no Resend key is set (`docker compose logs api`).

Useful commands:

```powershell
scripts/check.ps1                                       # gofmt, vet, build, Compose config, web build
docker compose run --rm audit                           # check database invariants (exit 1 on a violation)
docker compose --profile observability up -d            # Prometheus and Grafana on http://localhost:3000
```

After rebuilding the API, run `docker compose restart edge` so nginx picks up the new container.

## API overview

All paths are under `/api`. Writes need the `X-Ticker-Request: 1` header and a same-site origin. The UI also sends `X-Ticker-User`, and the API rejects a request whose user does not match the session cookie.

| Area | Endpoints |
|---|---|
| Catalog | `GET /events`, `GET /events/{id}`, `GET /events/{id}/seats` |
| Accounts | `POST /sessions`, `DELETE /sessions`, `GET /me`, signup code request and verify |
| Reservations | `POST /reservations` (with `Idempotency-Key`), `GET` and `DELETE /reservations/{id}`, `POST /reservations/{id}/confirm` |
| Bookings | `GET /bookings/{id}` |
| Interest | `GET`, `PUT`, `DELETE /events/{id}/interest` |
| Organizer | `/organizer/tenants/{t}/events` (create, list, read, edit, publish, delete), `…/seats` (read, block), `…/bookings`, `…/holds`, `…/bookings/{b}/cancel`, `…/insights`, `/organizer/tenants/{t}/insights` |

## Deploying

Two setups are supported:

- **Docker Compose**: nginx, API, worker, PostgreSQL, and Redis, as above.
- **Vercel** with Neon PostgreSQL and Upstash Redis (`vercel.json`). There is no worker there, so overdue holds are released on demand by the next request that needs those seats. Run the audit with `--worker=false` for this setup.

## Project layout

```
cmd/          api, worker, seed, audit entry points
internal/     catalog, identity, reservation, booking, organizer, interest, audit, seed, platform
db/           migrations and embedded SQL queries
web/          React UI
infra/        nginx, Prometheus, and Grafana config
scripts/      check.ps1
```

## Tests

This project has no automated test suite, by choice. Changes are checked with formatting, static analysis, builds, Compose validation, migrations, the demo seed, the audit command, and manual API and browser smoke checks. CI (`.github/workflows/ci.yml`) runs the automated part on every pull request.
