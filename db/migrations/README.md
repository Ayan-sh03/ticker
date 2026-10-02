# Migrations

Reserved for versioned PostgreSQL migrations. No domain schema is authorized in the foundation phase.

When schema work starts, use paired files such as `000001_initial.up.sql` and `000001_initial.down.sql`.
The Compose `migrate` service mounts this directory and uses golang-migrate.
The directory currently has no runnable migrations; the migration command is not part of foundation startup.
