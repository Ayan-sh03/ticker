# Repository instructions

## Current scope

- This phase is structure, Docker, and infrastructure setup only.
- Do not implement booking, reservations, authentication, tenant rules, domain schemas, seed data, or other product features until the user expands the scope.
- Keep domain directories as placeholders. Runnable entry points may contain only infrastructure wiring, health/readiness checks, metrics, and graceful shutdown.

## Tests

- Do not write any unit tests. The user explicitly requested this restriction.
- Do not add integration or end-to-end test suites during this scaffolding phase.
- Validate infrastructure with formatting, static analysis, compilation, Compose validation, and manual service smoke checks.
- Do not introduce testing frameworks, coverage requirements, or test-first workflows without a later user instruction expanding the scope.

## Project conventions

- Follow PLAN.md for the intended architecture; its later milestones are not authorization to implement them now.
- Use Go net/http and pgx, React/TypeScript/Vite, and PostgreSQL. Do not add Redis, brokers, or microservices.
- Keep API and worker as separate processes sharing one Go module.
- Keep credentials in local environment files; commit only example development configuration.
- Keep PowerShell scripts usable from the repository root on Windows.
- Do not delete database volumes or run destructive reset commands without explicit user authorization.

## Git commits

- Make small, atomic commits, each covering one coherent change.
- Include only files relevant to that change; do not combine unrelated scaffolding, features, fixes, or documentation into a bulk commit.
- Use concise, single-line commit messages that describe the change.
- Stage explicit file paths and inspect the staged diff before committing; avoid blanket staging such as `git add .` or `git add -A`.
- Keep local credentials, dependency directories, build output, and generated artifacts out of commits.
- Do not rewrite existing commits or push to a remote unless the user authorizes it.
