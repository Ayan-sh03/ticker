# Ticket reservation platform

## Scope

**Customer:** browse events → select 1–6 seats → reserve for 5 minutes → confirm booking.

**Organizer:** create and publish events, block available seats, inspect bookings and failures.

- Seeded accounts and cookie sessions. Customers own their records; organizers access only their tenant.
- Fixed prices and rectangular layouts; freeze both after publication.
- Exclude public signup, custom venue drawing, transfers, and promotions.

## Stack

- Go (`net/http`, `pgx`), React/TypeScript/Vite, PostgreSQL.
- Separate expiration worker. Docker Compose, k6, Prometheus/Grafana.
- No Redis, broker, or microservices.

```text
Browser → Go API → PostgreSQL ← Expiration worker
               ↓
        Prometheus / Grafana
```

## Data model

| Tables | Required constraints |
|---|---|
| users, sessions | Unique session token hash |
| tenants, memberships | Organizer access; unique tenant/user pair |
| events, seats | Tenant/event relationship; unique event/seat label |
| reservations, reservation_seats | Deadline; quantity 1–6; unique reservation/seat |
| bookings, booking_seats | Unique reservation; unique active booking per seat |
| idempotency_records | Unique actor/operation/key; payload hash and stored result |
| audit_entries | Actor, transition, reason, timestamp |

Use tenant/event-scoped foreign keys. Index seats by event/section, active reservations by deadline, and customer bookings by creation time.

## Correctness contract

- One seat has at most one owner.
- Multi-seat acquisition succeeds entirely or changes nothing.
- One reservation creates at most one booking.
- Same idempotency key + same payload returns the original result.
- Same key + different payload returns `409`.
- Lost response: retry with the original key.
- Expired reservations cannot be confirmed.

```text
Reservation: HELD → CONFIRMED | EXPIRED | CANCELLED
Seat: AVAILABLE → HELD → SOLD
                   ↓
               AVAILABLE
```

Use PostgreSQL `READ COMMITTED` and row locks.

**Lock order:** idempotency record → reservation → seats sorted by immutable ID.

**Reserve:** key → new reservation → lock seats → validate sale time/availability → assign all seats and deadline → persist result → commit. Conflict: persist `409`, allocate nothing.

**Confirm:** key → lock reservation/seats → validate deadline/ownership → create booking → mark seats sold → persist result → commit.

**Expire/cancel:** lock reservation/seats → require HELD and recheck eligibility → release all → record transition → commit. Confirmed booking cancellation is out of scope.

Sample database `clock_timestamp()` after locks. Valid means `time < deadline`. A confirmation validated before the deadline may commit afterward while holding its locks.

Worker uses `SKIP LOCKED`, one reservation per transaction. After crashes, retry uncommitted releases.

## UI requirements

- Responsive section-based seat map; keyboard controls and seat-list alternative.
- Server-based countdown; availability polling every 2 seconds while visible.
- Restore reservation and pending request key after refresh; refetch on reconnect.
- Explicit conflict, expired, unknown-outcome, and confirmed states.
- Organizer timeline: transitions, reasons, request IDs.

## Build order

| Milestone | Deliverable | Acceptance |
|---|---|---|
| 1. Vertical slice | Seeded event → reserve one seat → confirm → receipt | Two contenders produce one owner; refresh restores booking |
| 2. Product | Sessions, organizer creation, tenant checks | Organizer-created event is bookable; cross-tenant requests fail |
| 3. Concurrency | Multi-seat locking, idempotency, expiration | Overlapping requests never partially allocate; timeout retries recover |
| 4. UI | Responsive seat selection and recovery states | Keyboard purchase, refresh, conflict, and reconnect work |
| 5. Verification | Race tests, database audit, metrics | No duplicate bookings or inconsistent ownership after worker crashes |
| 6. Evidence | Benchmarks, report, 3-minute demo | Fresh clone reproduces tests and experiments |

Estimate: **140–200 hours**; **10–14 weeks at 15 hours/week**. Cut visual extras first.

## Verification

Real PostgreSQL tests: 100 contenders/one seat; overlapping seat sets; duplicate and changed-payload keys; lost response after commit; confirmation/expiration race; worker crashes; unauthorized access.

Audit: duplicate bookings, partial ownership, sold seats without bookings, overdue holds. Any unexplained mismatch fails the run.

## Benchmarks

Seed **100 events × 1,000 seats**. Reset between runs.

| Scenario | Offered load / duration |
|---|---|
| Normal | 80% browse, 20% reserve-and-confirm; 50/100/200 journeys/s, 10 min each |
| Sale opening | 1,000 reservation attempts/s for 30 seconds; 5,000 seats |
| Hot event | 90% of traffic targets one event; 200 journeys/s, 10 min |
| Scarcity | 1,000 attempts/s for 30 seconds; 20 seats |
| Sustained | 50 journeys/s for 1 hour |
| Recovery | Stop worker for 60 seconds with expiring holds; restart and audit |

Repeat normal/hot runs with **1, 2, 4 API replicas**. Keep total API database connections fixed at 48.

Declare limits: each API 1 CPU/512 MiB; database 2 CPU/4 GiB; worker 0.5 CPU/256 MiB. Record actual hardware. Use a separate generator; label same-host replica tests separately from multi-host scaling.

Record requests/s, new reservations/s, bookings/s, p95/p99, conflicts, failures, generator drops, CPU, pool/lock waits, expiration lag. Separate sold-out responses from booking capacity.

Hypotheses at 100 journeys/s: reservation p95 <300 ms, unexpected failures <0.1%, zero generator drops. Safety: zero invariant violations. Repeat three times; publish failures and limitations.

## Done

- Compose setup and test commands documented.
- CI: Go, PostgreSQL integration, TypeScript, purchase/isolation browser tests.
- Correctness tests and final audits pass.
- Demo: booking, contention, expiration recovery.
- Report: commands, commit, seed, hardware, raw results, repetitions, limitations.

## Extras

### Effort by milestone

M1: 20–25 h; M2: 15–20 h; M3: 25–35 h; M4: 25–35 h; M5: 20–30 h; M6: 15–25 h. Base: 120–170 h; allow roughly 20% contingency.

### Transaction limits and recovery

- Start with `lock_timeout=250ms`, `statement_timeout=1s`. Measure before changing. Lock timeout returns overload (`503`), not a seat conflict.
- Retry deadlocks at most twice within the request budget, using the same key. Unknown commit outcome requires replay/read, never a fresh key.
- Normalize payloads before hashing: sorted seat IDs, operation, schema version. Retain key/hash/result for the operation’s lifetime; retain a tombstone if pruning response bodies.
- Persist state changes and audit entries together. Log worker failures; expose overdue holds for investigation. Initial alert: release lag >10 seconds.
- Initial worker tick: 1 second, batches up to 100 reservations, one transaction each. Inventory remains unavailable until release commits.

### Benchmark controls and optimization

- Use k6 arrival-rate executors. Pilot first; preallocate VUs from rate × observed journey duration plus headroom. Monitor generator CPU/memory; shard if necessary.
- Record offered and started journeys, active users, actual HTTP requests, successful bookings, conflicts, overload, unexpected failures, and unsent iterations separately.
- Fix seed, workload, limits, and connection budget across comparisons. Warm normal/hot runs for 2 minutes; start burst/scarcity measurements with fresh inventory. Report success/conflict latency separately.
- Recovery fixture: abandoned holds whose deadlines fall within the worker outage. Confirm all are released after restart.
- API CPU bottleneck: profile handlers, then test replicas. Database bottleneck: inspect queries/indexes. Lock bottleneck: shorten transactions or test admission limits. Pool waits: inspect transaction duration before increasing connections.
- Cache only immutable layout/catalog or stale availability hints. Consider availability caching when polling exceeds 30% of database CPU; never cache ownership decisions.
- Change one variable; repeat three times. Save raw summaries/time series and before/after results. Extra replicas need measured gains.

### Repository and commands

```text
cmd/{api,worker,seed,audit}   internal/{identity,catalog,reservation,booking}
db/{migrations,queries}      web/       tests/       bench/       scripts/       docs/
```

Required command interface, to implement:

```powershell
docker compose up -d db
docker compose run --rm migrate up
docker compose run --rm seed --dataset demo --seed 42
docker compose up --build -d edge api worker web
docker compose run --rm tests
docker compose run --rm audit
./scripts/bench.ps1 -Scenario normal -Rate 100 -ApiReplicas 2 -Seed 42 -Repeat 3
```

CI also checks migration from empty/previous schema, Go race tests, and production builds. Document lock ordering, deadlines, tenant access, and benchmark limitations.

### Optional confirmed-booking cancellation

Organizer-only extension: lock reservation and seats → mark booking/reservation cancelled → revoke active booking-seat rows → release inventory → audit → commit. Repeated cancellation is idempotent. Old confirmation retries must not restore ownership after reassignment.

### Interview questions

1. Why is `READ COMMITTED` sufficient for these invariants?
2. Why do row locks work across replicas while Go mutexes do not?
3. How do overlapping multi-seat requests avoid partial allocation?
4. Why must every operation follow the same lock order?
5. Which clock decides expiration after waiting for a lock?
6. How does a retry recover a committed request with a lost response?
7. What prevents changed-payload key reuse and duplicate bookings?
8. What survives a worker crash before versus after commit?
9. Why might four replicas perform worse than two, and how do you detect generator saturation?
10. How do you prevent cross-tenant access through both queries and foreign keys?

### Resume and domain emphasis

- Built atomic multi-seat reservations; verified **[N] contenders** and **[N] deterministic race scenarios** without invariant violations.
- Sustained **[X] new bookings/s**, p95 **[Y] ms**, on **[hardware]** at **[offered load]**; published reproducible results.
- Reduced **[metric]** from **[before]** to **[after]** through **[specific change]** under unchanged test conditions.

Commerce: inventory conflicts and purchase recovery. Fintech: auditability and deterministic transitions. Travel: timed holds and atomic seat sets. SaaS: tenant isolation, permissions, and operational tools.
