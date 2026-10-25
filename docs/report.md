# Ticker engineering report

This report explains how Ticker keeps seat sales correct, what was measured under load, what was fixed, and what is left out on purpose.

## 1. The problem

Many people want the same few seats at the same moment. The system must:

- never sell a seat twice, even with thousands of requests for one seat;
- hold 1–6 seats all together or not at all;
- let a hold expire after 5 minutes, even if the server that made it crashes;
- survive retries: a request sent twice must not book twice;
- make every change traceable, so a bug shows up as a broken rule instead of a silent loss.

## 2. Data model

| Table | Purpose |
|---|---|
| `tenants`, `memberships`, `users` | Organizer workspaces and accounts. A membership lets a user manage one tenant. |
| `events`, `seats` | Events belong to a tenant; seats belong to an event. Composite foreign keys `(tenant_id, event_id)` stop rows from crossing tenants. |
| `reservations`, `reservation_seats` | A hold of 1–6 seats with a deadline. Status is `HELD`, `CONFIRMED`, `EXPIRED`, or `CANCELLED`. |
| `bookings`, `booking_seats` | A confirmed purchase. It keeps the per-seat price and the fee rate it was charged. |
| `audit_entries` | Append-only history of every status change, with actor and reason. A trigger rejects updates and deletes. |
| `idempotency_records` | The saved response for each reserve and confirm request key. |
| `event_interests`, `platform_settings` | Customer interest, and the default platform fee. |

Rules the database enforces on its own:

- **One active owner per seat.** `reservation_seats (seat_id) WHERE released_at IS NULL` and `booking_seats (seat_id) WHERE released_at IS NULL` are partial unique indexes. Even if the locking code had a bug, a second owner could not be inserted.
- **Seat count matches.** Deferred constraint triggers check that a reservation has exactly `quantity` seats.
- **History is append-only.** `audit_entries` rejects `UPDATE` and `DELETE`.
- **A booking keeps its price.** Each booking stores the per-seat price and fee rate it was charged. Separately, the API refuses to change an event's price, currency, or layout after its first reservation.

## 3. How a seat is won

All writes run in `READ COMMITTED` transactions with short lock timeouts (250 ms for customer writes, 1 s for organizer writes). Row locks decide ownership. Every path takes its locks in the same order, so two requests can never wait on each other in a circle:

| Operation | Lock order |
|---|---|
| Reserve | request key → event `FOR SHARE` → overdue holds on the requested seats → seats by ascending ID |
| Confirm | request key → reservation `FOR UPDATE` → its seats by ID |
| Customer cancel, worker expiry | reservation → its seats by ID (the worker uses `SKIP LOCKED`) |
| Organizer seat blocking | membership → event `FOR SHARE` → overdue holds → seats by ID |
| Organizer booking cancellation | membership → event `FOR SHARE` → reservation → its seats by ID |
| Event edit, publish, delete | membership → event `FOR UPDATE` |

Details that matter:

- **Sorted seat locks.** Two people who want seats {A, B} and {B, A} both lock A first, so one simply waits for the other.
- **Fast rejection.** Before taking any lock, a reserve request checks without locking whether a requested seat is already taken. Most losing requests stop there.
- **Savepoint for losers.** Seats are claimed under a savepoint. A request that loses a seat rolls back to it before writing its idempotency record, so a rejected hold writes no reservation row.
- **Time from the database.** Deadlines and "has the event started" checks use `clock_timestamp()` after the last lock. A request that waited for a lock cannot carry an old decision past a deadline.
- **Expiry without a worker.** Any request that needs seats first releases overdue holds on those seats. The worker only makes this faster, which is why the Vercel deployment can run without one.

## 4. Retries and idempotency

`POST /reservations` and `POST /reservations/{id}/confirm` need an `Idempotency-Key`. The key, a hash of the payload, and the final response are saved in the same transaction as the change. A retry with the same key gets the saved response; a reused key with a different payload gets `409 idempotency_key_reused`. The UI saves pending requests in browser storage and replays them after a reload, after first confirming that the signed-in account has not changed (`X-Ticker-User`).

Cancelling a booking and blocking seats need no key: repeating them leaves the same final state.

## 5. Abuse limits

Limits key on a signed device cookie, then on a browser fingerprint, with only a high per-network ceiling, so one office behind a single IP is not blocked. Logins, signups, and writes share counters in Redis; reads are counted in process memory. If Redis is down, limits fall back to memory and never block everyone. Password hashing uses at most half the CPUs, so a login flood cannot starve seat reads.

Signup sends a six-digit code that is stored hashed in Redis for ten minutes, allows five wrong guesses, and is rate-limited per address. It never reveals whether an account exists until the code is verified.

## 6. Checking the data

`docker compose run --rm audit` opens one read-only snapshot and checks rules that span tables:

- each seat has at most one active hold or active booking;
- every SOLD seat has an active booking, and every active booking's seats are SOLD;
- a blocked seat is never held;
- reservation status agrees with held seats and bookings, and a cancelled booking has released every seat;
- booking prices match their event;
- no hold is expired before its deadline, and no hold stays overdue for long (when a worker runs);
- each reservation's audit entries form exactly one allowed path, such as `HELD → CONFIRMED → CANCELLED`, by the right actor.

It prints violations with examples and exits 1. It never repairs data, because a repair would have to guess who owns a seat. CI runs it after seeding a fresh database.

## 7. Organizer features

- **Seat blocking.** 1–200 seats per request, all or nothing. Sold and held seats cannot change.
- **Bookings view.** Pages use a `(created_at, id)` cursor, so rows that change while someone pages never shift later pages. Counts and rows come from one `REPEATABLE READ` snapshot and one database time, so a hold never counts as both open and expired. Indexes match the page order.
- **Booking cancellation.** It keeps all rows as history and releases the seats. The audit trail records `CONFIRMED → CANCELLED` with the organizer as actor.
- **Insights.** Revenue has one definition, the `booking_revenue` view: active bookings, at the price and fee rate saved on each booking, with the fee rounded half up. Because each booking stores its rate, changing a tenant's fee affects only new bookings. Days and hours use the tenant's time zone, which must be a named zone such as Asia/Kolkata; a trigger rejects anything PostgreSQL does not know. The funnel runs from interest to holds to confirmed, expired, and cancelled. Money totals leave the database as exact decimal strings, because a sum can exceed a 64-bit integer and JavaScript loses precision above 2^53. Timing only counts bookings made after the current sales opening, since organizers can move it, and the sell-out time is reported as unknown while seats are blocked, because blocking lowers capacity after the fact. Page views are not tracked.

## 8. Load testing

Tests ran on one 4-CPU machine with Docker Desktop: API 2 CPU / 1 GiB, PostgreSQL 2 CPU / 3 GiB, and the k6 generator 2 CPU / 2 GiB. One journey read three URLs; one journey in five also reserved, confirmed, and read the receipt. Every run ended with the database rule checks.

What happened before the fixes:

1. **1,000 users, one seat, same instant:** exactly one winner. But 634 users got HTTP 503 instead of a clean "seat taken".
2. **10,000-seat map, 50 → 2,400 journeys/s:** 63% of answers failed. Each seat-map read scanned 10,000 rows, the database hit 2 full cores, and reads passed the 3 s limit.
3. **1,000-seat map, same ramp:** no errors, but p95 latency was 1.3 s.
4. **Abuse traffic, 300 requests/s:** no 5xx; size limits and login checks held.
5. **Steady 80 journeys/s for 10 minutes:** p95 68 ms, with flat memory.

Fixes, each tracked as a GitHub issue and merged in pull requests #15–#18:

| Issue | Fix |
|---|---|
| Seat map hammered the database | A 2-second per-event seat-map cache with `singleflight`, so one query serves every concurrent reader. The cache never decides ownership. |
| #7 Seat-map query grew with every past sale | It reads only the event's open holds, backed by a partial index of held reservations. |
| #8 Seat map re-encoded on every request | Encoded JSON plus a gzipped copy are built once per snapshot and reused while unchanged. |
| #9, #11 Losers held locks and wrote rows | A lock-free pre-check, a savepoint for losing requests, and inserting the reservation only after every seat is won. |
| #10 Login hashing starved the CPU | Hashing is limited to half the CPUs, and a login waits up to 1 s for a slot. |
| #12 Database pool too large | Measured 12, 16, and 48 connections on a 2-CPU database; 12 stays the default. |
| #13 One cancelled caller failed all waiters | The shared cache fetch runs on its own context. |
| #14 Edge overhead | Slimmer nginx images, upstream keepalive, and larger proxy buffers. |

Results after the cache fix (same tests):

| Test | Metric | Before | After |
|---|---|---|---|
| 10,000-seat map | HTTP 503 answers | 13,163 | 0 |
| 10,000-seat map | database peak CPU | 269% | 131% |
| 1,000-seat map | mean rate | 165 journeys/s | 205 journeys/s |
| 1,000-seat map | p95 / p99 | 1,337 / 1,752 ms | 1,050 / 1,279 ms |
| all runs | data rule violations | 0 | 0 |

The 10,000-seat run after the fix stopped when k6 reached its 2 GiB limit. That is a generator limit: the server returned no errors before it. The API stayed under 70 MiB of memory in every test. All numbers come from one shared machine, so they compare runs with each other, not with production hardware.

## 9. Trade-offs and limits

- **PostgreSQL decides everything.** No distributed locks, queues, or caches take part in ownership. This is simpler and safe, but one primary database is the ceiling for writes to a single event.
- **Short cache, small lie.** The public seat map can be up to 2 seconds old. It may show a seat as free that was just taken. The reserve request then says so, and ownership is never wrong.
- **No payments or refunds.** Confirmation stands in for payment, and organizer cancellation does not refund.
- **Not built:** customer cancellation of confirmed bookings, organizer signup, password recovery, editing fee rates or time zones in the UI, and page-view tracking.
- **No automated tests, by choice.** Correctness rests on database constraints, the audit command, CI checks, and manual smoke tests. A race-test suite would be the next thing to add.
