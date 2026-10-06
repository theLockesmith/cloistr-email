# Tasks

- [x] Integration tests first (real Postgres, gated on TEST_PG_ADMIN_URL) (verified 2026-10-06: 8 tests in internal/storage/migrate_test.go fail before the migrator exists, pass after against Postgres 17; three deliberate mutations, unnamed contacts constraint, a schema-changing 013, no advisory lock, each made the matching test fail)
- [x] Baseline schema.sql fixed to match production (verified 2026-10-06: TestBaselineMatchesProduction, empty build vs production dump identical in tables, columns, types, defaults, index and constraint names)
- [x] Embedded SQL + migrator in internal/storage, wired into Migrate() (verified 2026-10-06: go build ./..., go vet, go test ./... -short all 13 packages ok)
- [x] Proof: empty DB built by the real image through pgbouncer transaction mode as a non-superuser role (verified 2026-10-06: "built email schema from baseline", 13 tables + schema_migrations; tables/columns/constraints and index names identical to production)
- [x] Proof: real image against a copy of production's current schema (verified 2026-10-06: role without database CREATE; "adopted existing email schema as baseline"; 227 email objects unchanged; second start a no-op)
- [x] Proof: two replicas at once on an empty DB (verified 2026-10-06: one built, one found nothing to do; baseline recorded once)
- [ ] Review, merge (tell cloistr-orchestrator first), watch rollout to Ready, lift the production pin
- [ ] Follow-up migration 013: idx_audit_log_mailbox_pubkey and idx_email_templates_mailbox_pubkey (dropped from the baseline because production never had them)
