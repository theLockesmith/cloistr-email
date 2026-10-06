# Self-applied schema migrations: cloistr-email

Requested by cloistr-orchestrator 2026-10-06. Production's email schema was
brought current by hand, outside any tool, and a database rebuilt from the
repo alone would not start. Today the service only *checks* the schema at boot.

1. The service applies its own schema at startup. No human step.
2. Idempotent: running it again on an up-to-date database changes nothing.
3. Transactional: all of a run commits, or none of it does.
4. Locked: two replicas starting together cannot both apply; the second waits,
   then finds nothing to do. Must work through pgbouncer in transaction mode
   (production connects via pgbouncer :6432), so session-level locks are out.
5. Empty database: builds the complete current schema.
6. Production's current schema (2026-10-06, after 010+011 were applied by hand):
   a no-op. Not one object added, changed or removed.
7. An existing database that is missing something the code needs refuses to
   start, naming each missing object. (Never guess-repair an unknown schema.)
8. Future schema changes are numbered migrations, each applied exactly once
   and recorded.
9. The baseline schema file builds a database identical to production's.
