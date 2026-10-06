package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"git.aegis-hq.xyz/coldforge/cloistr-email/configs"
)

// These tests run the migrator against a real Postgres. Each test gets its own
// throwaway database, created and dropped through TEST_PG_ADMIN_URL (a role
// with CREATEDB). A separate variable from DATABASE_URL on purpose: nothing
// here may ever run against a configured application database.
//
//	docker run -d -e POSTGRES_PASSWORD=test -p 55432:5432 postgres:17-alpine
//	TEST_PG_ADMIN_URL='postgres://postgres:test@localhost:55432/postgres?sslmode=disable' \
//	  go test ./internal/storage -run Migrat -v

func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	adminURL := os.Getenv("TEST_PG_ADMIN_URL")
	if adminURL == "" {
		t.Skip("set TEST_PG_ADMIN_URL to run the migrator tests against a real Postgres")
	}
	admin, err := sql.Open("postgres", adminURL)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })

	name := fmt.Sprintf("migtest_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create database: %v", err)
	}

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse admin url: %v", err)
	}
	u.Path = "/" + name
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
	})
	return db
}

func loadFixture(t *testing.T, db *sql.DB, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if _, err := db.Exec(string(b)); err != nil {
		t.Fatalf("load fixture: %v", err)
	}
}

// schemaObjects lists every table, column, index and constraint in schema
// `email` except the migrator's own bookkeeping table, so two snapshots can be
// compared for "nothing changed".
func schemaObjects(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`
		SELECT 'table ' || table_name FROM information_schema.tables
		 WHERE table_schema = 'email' AND table_name <> 'schema_migrations'
		UNION ALL
		SELECT 'column ' || table_name || '.' || column_name || ' ' || data_type
		       || ' null=' || is_nullable || ' default=' || coalesce(column_default, '-')
		  FROM information_schema.columns
		 WHERE table_schema = 'email' AND table_name <> 'schema_migrations'
		UNION ALL
		SELECT 'index ' || indexname FROM pg_indexes
		 WHERE schemaname = 'email' AND tablename <> 'schema_migrations'
		UNION ALL
		SELECT 'constraint ' || conname || ': ' || pg_get_constraintdef(oid) FROM pg_constraint
		 WHERE connamespace = 'email'::regnamespace AND conrelid::regclass::text NOT LIKE '%schema_migrations'
		UNION ALL
		SELECT 'function ' || proname FROM pg_proc
		 WHERE pronamespace = 'email'::regnamespace
		UNION ALL
		SELECT 'trigger ' || tgname || ' on ' || c.relname
		  FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid
		 WHERE c.relnamespace = 'email'::regnamespace AND NOT t.tgisinternal
		ORDER BY 1`)
	if err != nil {
		t.Fatalf("snapshot schema: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func appliedVersions(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM email.schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, v)
	}
	return out
}

func tableExists(t *testing.T, db *sql.DB, schema, table string) bool {
	t.Helper()
	var ok bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT FROM information_schema.tables
		WHERE table_schema = $1 AND table_name = $2)`, schema, table).Scan(&ok); err != nil {
		t.Fatalf("table exists: %v", err)
	}
	return ok
}

func newTestMigrator(t *testing.T, db *sql.DB) *Migrator {
	t.Helper()
	m, err := NewMigrator(db, configs.SQL, zap.NewNop())
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	return m
}

func TestMigrateEmptyDatabaseBuildsSchemaAndIsIdempotent(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()
	m := newTestMigrator(t, db)

	if err := m.Run(ctx); err != nil {
		t.Fatalf("first run: %v", err)
	}
	for _, table := range []string{"mailboxes", "emails", "attachments", "nip05_cache", "email_complaints"} {
		if !tableExists(t, db, "email", table) {
			t.Errorf("email.%s not created", table)
		}
	}
	if err := verifyRequired(ctx, db); err != nil {
		t.Fatalf("schema built from empty is missing required objects: %v", err)
	}
	if got := appliedVersions(t, db); len(got) == 0 || got[0] != BaselineVersion {
		t.Fatalf("baseline not recorded, schema_migrations = %v", got)
	}

	before := schemaObjects(t, db)
	versionsBefore := appliedVersions(t, db)
	if err := m.Run(ctx); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if after := schemaObjects(t, db); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("second run changed the schema")
	}
	if got := appliedVersions(t, db); strings.Join(got, ",") != strings.Join(versionsBefore, ",") {
		t.Fatalf("second run changed schema_migrations: %v -> %v", versionsBefore, got)
	}
}

func TestMigrateProductionSchemaIsNoOp(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()
	loadFixture(t, db, "testdata/prod_email_schema_20261006.sql")

	before := schemaObjects(t, db)
	if err := newTestMigrator(t, db).Run(ctx); err != nil {
		t.Fatalf("run against production schema: %v", err)
	}
	after := schemaObjects(t, db)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("migrator changed production's schema:\n%s", diffLines(before, after))
	}
	if got := appliedVersions(t, db); len(got) == 0 || got[0] != BaselineVersion {
		t.Fatalf("baseline not recorded on adoption, schema_migrations = %v", got)
	}
}

// The empty-database build must produce the same objects production has: same
// tables, columns, types, defaults, index and constraint names.
func TestBaselineMatchesProduction(t *testing.T) {
	ctx := context.Background()
	prod := freshDB(t)
	loadFixture(t, prod, "testdata/prod_email_schema_20261006.sql")
	built := freshDB(t)
	if err := newTestMigrator(t, built).Run(ctx); err != nil {
		t.Fatalf("build from empty: %v", err)
	}
	p, b := schemaObjects(t, prod), schemaObjects(t, built)
	if strings.Join(p, "\n") != strings.Join(b, "\n") {
		t.Fatalf("baseline differs from production (- production only, + baseline only):\n%s", diffLines(p, b))
	}
}

func TestMigrateLegacyMissingObjectRefusesAndNamesIt(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()
	loadFixture(t, db, "testdata/prod_email_schema_20261006.sql")
	if _, err := db.Exec(`ALTER TABLE email.mailboxes DROP COLUMN preferred_encryption_mode`); err != nil {
		t.Fatalf("drop column: %v", err)
	}

	err := newTestMigrator(t, db).Run(ctx)
	if err == nil {
		t.Fatal("expected refusal on a legacy schema missing a required column")
	}
	if !strings.Contains(err.Error(), "mailboxes.preferred_encryption_mode") {
		t.Fatalf("error does not name the missing column: %v", err)
	}
	if tableExists(t, db, "email", "schema_migrations") {
		t.Fatal("a refused run left schema_migrations behind; it must roll back completely")
	}
}

func TestMigrateConcurrentRunsApplyOnce(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Separate Migrator per goroutine, as separate replicas would have.
			m, err := NewMigrator(db, configs.SQL, zap.NewNop())
			if err != nil {
				errs[i] = err
				return
			}
			errs[i] = m.Run(ctx)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("run %d: %v", i, err)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM email.schema_migrations WHERE version = $1`, BaselineVersion).Scan(&n); err != nil {
		t.Fatalf("count baseline rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("baseline recorded %d times, want 1", n)
	}
}

func TestMigrateFailingMigrationLeavesNothing(t *testing.T) {
	db := freshDB(t)
	ctx := context.Background()
	m := newTestMigrator(t, db)
	m.migrations = append(m.migrations, Migration{Version: "999_broken", SQL: "CREATE TABLE ok_so_far (id int); SELECT no_such_column FROM ok_so_far;"})

	if err := m.Run(ctx); err == nil {
		t.Fatal("expected the broken migration to fail the run")
	}
	for _, table := range []string{"mailboxes", "schema_migrations", "ok_so_far"} {
		if tableExists(t, db, "email", table) {
			t.Errorf("email.%s exists after a failed run; the whole run must roll back", table)
		}
	}
}

// requiredColumns must be exactly the columns the baseline builds: anything
// less and adoption would accept an incomplete schema, anything more and an
// empty-database build would refuse itself.
func TestRequiredColumnsCoverBaseline(t *testing.T) {
	db := freshDB(t)
	if err := newTestMigrator(t, db).Run(context.Background()); err != nil {
		t.Fatalf("build from empty: %v", err)
	}
	rows, err := db.Query(`SELECT table_name || '.' || column_name FROM information_schema.columns
		WHERE table_schema = 'email' AND table_name <> 'schema_migrations' ORDER BY 1`)
	if err != nil {
		t.Fatalf("list columns: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var built []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		built = append(built, s)
	}
	var listed []string
	for table, cols := range requiredColumns {
		for _, c := range cols {
			listed = append(listed, table+"."+c)
		}
	}
	sort.Strings(listed)
	if strings.Join(built, "\n") != strings.Join(listed, "\n") {
		t.Fatalf("requiredColumns out of step with the baseline (- built only, + listed only):\n%s", diffLines(built, listed))
	}
}

// Migrations run with a short lock wait (so DDL never queues behind live
// traffic for long) and a per-statement ceiling. A migration that observes
// anything else fails the run.
func TestMigrationsRunUnderTheDocumentedTimeouts(t *testing.T) {
	db := freshDB(t)
	m := newTestMigrator(t, db)
	m.migrations = append(m.migrations, Migration{Version: "999_check_timeouts", SQL: `
		DO $$ BEGIN
			IF current_setting('lock_timeout') <> '10s' THEN
				RAISE EXCEPTION 'lock_timeout is %, want 10s', current_setting('lock_timeout');
			END IF;
			IF current_setting('statement_timeout') <> '5min' THEN
				RAISE EXCEPTION 'statement_timeout is %, want 5min', current_setting('statement_timeout');
			END IF;
		END $$;`})
	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("migration saw the wrong timeouts: %v", err)
	}
}

func TestTrackedMigrationsHaveNoTransactionControl(t *testing.T) {
	m, err := NewMigrator(nil, configs.SQL, zap.NewNop())
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}
	txControl := regexp.MustCompile(`(?im)^\s*(BEGIN|COMMIT|ROLLBACK)\s*;`)
	if txControl.MatchString(m.baseline) {
		t.Error("baseline schema contains BEGIN/COMMIT; the migrator owns the transaction")
	}
	for _, mig := range m.migrations {
		if txControl.MatchString(mig.SQL) {
			t.Errorf("%s contains BEGIN/COMMIT; the migrator owns the transaction", mig.Version)
		}
	}
}

func diffLines(a, b []string) string {
	in := func(xs []string) map[string]bool {
		m := make(map[string]bool, len(xs))
		for _, x := range xs {
			m[x] = true
		}
		return m
	}
	ia, ib := in(a), in(b)
	var sb strings.Builder
	for _, x := range a {
		if !ib[x] {
			sb.WriteString("- " + x + "\n")
		}
	}
	for _, x := range b {
		if !ia[x] {
			sb.WriteString("+ " + x + "\n")
		}
	}
	return sb.String()
}
