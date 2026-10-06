# Design

## Pieces
- `configs/schema.sql` is the **baseline**: the complete schema as of migration
  012, matching production object for object (fixes: drop the trigger on the
  removed `users` table; fold in 011 and 012; name the contacts unique
  constraint `contacts_mailbox_email_key` as 008 did; drop the two indexes that
  only ever existed in this file, not in any migration or in production).
- `configs/migrations/0NN_*.sql` with NN > 012 are **tracked migrations**,
  applied in order, each once. 002-012 are history, folded into the baseline,
  never run by the migrator. Tracked files must not contain BEGIN/COMMIT.
- `email.schema_migrations(version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ)`
  records what ran. The baseline is recorded as version `baseline-012`.
- SQL files are embedded into the binary (`configs/embed.go`), so the image
  needs nothing at runtime.

## Run (one transaction)
1. `BEGIN`; `SET LOCAL lock_timeout`/`statement_timeout`;
   `pg_advisory_xact_lock(<fixed key>)` (transaction-scoped: safe through
   pgbouncer transaction pooling; released by COMMIT/ROLLBACK).
2. Create schema `email` only if absent (catalog check first, so an existing
   schema needs no database-level CREATE privilege).
3. `SET LOCAL search_path = email, public`; create `schema_migrations` if absent.
4. No `baseline-012` row:
   - `email.mailboxes` absent: run the baseline, record it.
   - present (legacy, e.g. production): verify every required table/column;
     if any is missing, ROLLBACK and refuse to start listing them; else record
     the baseline as applied without running it.
5. Apply each tracked migration not yet recorded, in order; record each.
6. Verify required columns (existing check, extended); COMMIT.

## Why adopt-and-verify instead of re-running history
Migrations 004, 006 and 008 are not re-runnable (bare CREATE INDEX, ALTER TYPE,
ADD COLUMN without IF NOT EXISTS), and production carries no record of which
ran. Verifying the shape and recording a baseline makes production a no-op by
construction instead of by luck.
