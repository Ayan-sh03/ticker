# Infrastructure foundation

```text
Browser -> edge (8080) -> web (static React build)
                     -> /api/* -> Go API -> PostgreSQL
                                  worker -> PostgreSQL

Optional: Prometheus -> API /metrics; Grafana -> Prometheus
```

- Compose's default network provides service discovery by service name.
- Only the gateway, database, and optional observability tools publish local ports.
- API and worker use separate pgx pools; defaults are 12 and 2 connections respectively.
- PostgreSQL data, Prometheus storage, and Grafana state use separate named volumes.
- PostgreSQL readiness gates API/worker startup; API and web readiness gate the gateway.
- API liveness does not depend on PostgreSQL. Readiness does.
- The API and worker handle SIGINT/SIGTERM; API shutdown has a 10-second deadline.
- Backend runtime containers run as an unprivileged user.
- No application schema is installed, and the worker performs no domain jobs.
- Keep existing local data when stopping services; volume deletion requires explicit authorization.

The observability profile includes runtime metrics and a datasource only. Booking metrics, dashboards, load generators, resource-limit experiments, and replica comparisons belong to later milestones.
