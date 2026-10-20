# Repository instructions

## Current scope

- Current authorized scope: infrastructure, catalog schema and read-only APIs, deterministic demo seed, seeded-account cookie sessions, all-or-nothing reservations of 1–6 seats, booking confirmation with owner-only receipts, an append-only reservation audit trail, the customer UI, and tenant-scoped organizer create, read, edit, publish, and delete APIs and UI.
- The initial domain tables are users, tenants, memberships, events, and seats. Enforce tenant/event relationships. Published prices, currency, and layouts remain editable until the first reservation, then stay frozen even after that reservation expires or is cancelled.
- The customer reservation UI is reviewed and committed. Preserve its design and behavior.
- Organizer draft and publication APIs are reviewed and committed. Drafts stay private. Organizers may edit title, description, venue, and dates on their events; changed start dates must be in the future and sales must open before the start. Events may be deleted only while they have no reservation history. Protect edits and deletion against stale versions.
- Organizer event editing and deletion are reviewed and committed.
- Multi-seat reservations (1–6 seats, sorted lock order, audit trail) and serverless hosting are reviewed and merged.
- The current batch adds rate limiting and lock-timeout retries. Limit by signed device cookie, then by a browser fingerprint (address plus browser headers), with only a high per-network flood ceiling, so offices behind one address are not blocked. Limit reserve and confirm per account too. Count logins and writes in Redis so all API instances share one budget; count reads in process memory. If Redis fails, fall back to memory limits; never fail closed. Return 429 with `Retry-After` before any database work; a rate-limited request was not processed, so clients retry with the same idempotency key. Retry lock timeouts and deadlocks a few times inside the API before returning 503 with `Retry-After`. Validate manually and open a pull request for review.
- Two deployments are supported: Docker Compose (nginx, API, worker, PostgreSQL, Redis) and Vercel with Neon PostgreSQL and Upstash Redis, where overdue holds are released on demand instead of by the worker.
- Work in a learning-first loop: before implementing a milestone, ask the user short concept questions about it, discuss the answers, then implement.
- Protected UI requests assert their expected account with `X-Ticker-User`; the API must reject a mismatch with the cookie owner before any mutation or private read.
- Use Hallmark for the UI. Do not use other skills unless the user asks later. Keep the design minimal and interactions snappy.
- Do not implement confirmed-booking cancellation, multi-seat selection in the customer UI, organizer writes beyond event creation, editing, publication, and deletion, public signup, password recovery, or payment integration until the user expands the scope.
- The worker processes reservation expiration only.

## Work batch size

- Keep future implementation batches roughly the size of the catalog foundation step: one initial schema, a demo seed, and a few read-only endpoints. Smaller batches are fine when the change is narrower.
- Complete one coherent, runnable step at a time, then leave it for user review before starting the next milestone.
- Keep the amount of code easy to inspect and trim. Avoid speculative abstractions, unnecessary boilerplate, and unrelated improvements.
- Treat batch size as a scope limit, not a code quota; do not add code just to match the previous batch.

## Tests

- Do not write any unit tests. The user explicitly requested this restriction.
- Do not add integration or end-to-end test suites without a later user instruction authorizing them.
- Validate changes with formatting, static analysis, compilation, Compose validation, and manual database/API smoke checks.
- Do not introduce testing frameworks, coverage requirements, or test-first workflows without a later user instruction expanding the scope.

## Project conventions

- Follow the local, gitignored PLAN.md for the intended architecture and roadmap; its later milestones are not authorization to implement them now.
- Keep AGENTS.md as the only tracked Markdown file for now. PLAN.md must remain local and gitignored.
- Do not create README.md or other project-owned Markdown documentation yet; the user wants the README added at the end.
- Use .gitkeep files to preserve empty scaffold directories.
- Use Go net/http and pgx, React/TypeScript/Vite, and PostgreSQL. Redis is allowed for rate limiting only; do not use it for locks, caching, or queues. Do not add brokers or microservices.
- Keep API and worker as separate processes sharing one Go module.
- Keep credentials in local environment files; commit only example development configuration.
- Keep PowerShell scripts usable from the repository root on Windows.
- Do not delete database volumes or run destructive reset commands without explicit user authorization.

## Git commits

- Do not commit the current work until the user has reviewed it and explicitly authorizes committing.
- Make small, atomic commits, each covering one coherent change.
- Include only files relevant to that change; do not combine unrelated scaffolding, features, fixes, or documentation into a bulk commit.
- Use concise, single-line commit messages that describe the change.
- Stage explicit file paths and inspect the staged diff before committing; avoid blanket staging such as `git add .` or `git add -A`.
- Keep local credentials, dependency directories, build output, and generated artifacts out of commits.
- Do not rewrite existing commits or push to a remote unless the user authorizes it.
