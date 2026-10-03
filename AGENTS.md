# Repository instructions

## Current scope

- Current authorized scope: infrastructure, catalog schema and read-only APIs, deterministic demo seed, seeded-account cookie sessions, and single-seat reservations.
- The initial domain tables are users, tenants, memberships, events, and seats. Enforce tenant/event relationships and freeze published prices/layouts.
- This batch adds five-minute holds, owner-only reads/cancellation, request-key replay, and worker expiration. Validate it manually, then leave it uncommitted for review.
- Do not implement booking confirmation, multi-seat acquisition, organizer write APIs, public signup, password recovery, or product UI until the user expands the scope.
- Keep the booking directory as a placeholder. The worker processes reservation expiration only.

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
- Use Go net/http and pgx, React/TypeScript/Vite, and PostgreSQL. Do not add Redis, brokers, or microservices.
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
