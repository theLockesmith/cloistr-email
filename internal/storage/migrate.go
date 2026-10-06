package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// BaselineVersion is recorded in email.schema_migrations once the baseline
// schema (configs/schema.sql, complete as of migration 012) is in place,
// whether the migrator built it or adopted an existing database that already
// had it.
const BaselineVersion = "baseline-012"

// lastFoldedMigration is the highest numbered migration already contained in
// the baseline. Files numbered at or below it are history and never run.
const lastFoldedMigration = 12

// migrateLockKey identifies the transaction-scoped advisory lock that
// serialises migrator runs across replicas. Any fixed value works; it only has
// to be the same in every replica and unused by anything else in the database.
const migrateLockKey int64 = 0x636c6f6973747201 // "cloistr" + 0x01

// Migration is one numbered schema change, applied at most once.
type Migration struct {
	Version string // file name without .sql, e.g. "013_add_index"
	SQL     string
}

// Migrator applies the service's own schema at startup.
//
// One run is one transaction: an advisory lock, then either building the
// baseline (empty database) or adopting an existing schema after verifying
// it, then every numbered migration not yet recorded, then COMMIT. Anything
// failing rolls the whole run back.
type Migrator struct {
	db         *sql.DB
	baseline   string
	migrations []Migration
	logger     *zap.Logger
}

var migrationName = regexp.MustCompile(`^(\d+)_[A-Za-z0-9_]+\.sql$`)

// NewMigrator loads schema.sql and migrations/*.sql from fsys.
func NewMigrator(db *sql.DB, fsys fs.FS, logger *zap.Logger) (*Migrator, error) {
	baseline, err := fs.ReadFile(fsys, "schema.sql")
	if err != nil {
		return nil, fmt.Errorf("read baseline schema: %w", err)
	}

	paths, err := fs.Glob(fsys, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	type numbered struct {
		n int
		m Migration
	}
	var found []numbered
	seen := map[int]string{}
	for _, p := range paths {
		name := path.Base(p)
		match := migrationName.FindStringSubmatch(name)
		if match == nil {
			return nil, fmt.Errorf("migration %s: name must be NNN_description.sql", name)
		}
		n, _ := strconv.Atoi(match[1])
		if prev, dup := seen[n]; dup {
			return nil, fmt.Errorf("migrations %s and %s share number %d", prev, name, n)
		}
		seen[n] = name
		if n <= lastFoldedMigration {
			continue // already part of the baseline
		}
		body, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		found = append(found, numbered{n, Migration{Version: strings.TrimSuffix(name, ".sql"), SQL: string(body)}})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].n < found[j].n })

	m := &Migrator{db: db, baseline: string(baseline), logger: logger}
	for _, f := range found {
		m.migrations = append(m.migrations, f.m)
	}
	return m, nil
}

// Run brings the database's email schema up to date, or changes nothing.
func (m *Migrator) Run(ctx context.Context) (err error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// A second replica waits here for the first to commit, then finds nothing
	// to do. Transaction-scoped on purpose: production connects through
	// pgbouncer in transaction mode, where a session-level lock could be held
	// by a server connection that is then handed to someone else.
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '120s'`); err != nil {
		return fmt.Errorf("set lock timeout: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrateLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	// Once we hold it, DDL must not queue behind live traffic for long: fail
	// and retry on the next start rather than stall requests on a table lock.
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '10s'; SET LOCAL statement_timeout = '5min'`); err != nil {
		return fmt.Errorf("set statement timeouts: %w", err)
	}

	// Check before creating, so an existing schema needs no database-level
	// CREATE privilege (production's role owns the schema, not the database).
	var schemaExists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT FROM pg_namespace WHERE nspname = 'email')`).Scan(&schemaExists); err != nil {
		return fmt.Errorf("check email schema: %w", err)
	}
	if !schemaExists {
		if _, err := tx.ExecContext(ctx, `CREATE SCHEMA email`); err != nil {
			return fmt.Errorf("create email schema: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL search_path = email, public`); err != nil {
		return fmt.Errorf("set search_path: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS email.schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := appliedSet(ctx, tx)
	if err != nil {
		return err
	}

	if !applied[BaselineVersion] {
		var hasMailboxes bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM information_schema.tables
			WHERE table_schema = 'email' AND table_name = 'mailboxes')`).Scan(&hasMailboxes); err != nil {
			return fmt.Errorf("check for existing schema: %w", err)
		}
		if hasMailboxes {
			// A schema built by hand before the migrator existed (production).
			// Never guess-repair it: adopt it only if it has everything the
			// baseline defines, otherwise refuse and say exactly what is missing.
			if err := verifyRequired(ctx, tx); err != nil {
				return fmt.Errorf("existing email schema was never recorded by the migrator and does not match the baseline, refusing to adopt it: %w", err)
			}
			m.logger.Info("adopted existing email schema as baseline", zap.String("version", BaselineVersion))
		} else {
			if _, err := tx.ExecContext(ctx, m.baseline); err != nil {
				return fmt.Errorf("apply baseline schema: %w", err)
			}
			m.logger.Info("built email schema from baseline", zap.String("version", BaselineVersion))
		}
		if err := record(ctx, tx, BaselineVersion); err != nil {
			return err
		}
	}

	for _, mig := range m.migrations {
		if applied[mig.Version] {
			continue
		}
		if _, err := tx.ExecContext(ctx, mig.SQL); err != nil {
			return fmt.Errorf("apply migration %s: %w", mig.Version, err)
		}
		if err := record(ctx, tx, mig.Version); err != nil {
			return err
		}
		m.logger.Info("applied migration", zap.String("version", mig.Version))
	}

	if err := verifyRequired(ctx, tx); err != nil {
		return fmt.Errorf("schema incomplete after migrating: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	committed = true
	return nil
}

func appliedSet(ctx context.Context, tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT version FROM email.schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func record(ctx context.Context, tx *sql.Tx, version string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO email.schema_migrations (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record %s: %w", version, err)
	}
	return nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// requiredColumns is every column the baseline defines, per table. An existing
// schema is adopted only if it has all of them, and every run ends by checking
// them. TestRequiredColumnsCoverBaseline keeps this list equal to what the
// baseline actually builds, so it cannot silently fall behind.
var requiredColumns = map[string][]string{
	"attachments":      {"blossom_sha256", "blossom_url", "content_type", "created_at", "email_id", "filename", "id", "size_bytes"},
	"audit_log":        {"action", "created_at", "details", "id", "ip_address", "mailbox_pubkey", "resource_id", "resource_type", "user_agent"},
	"contacts":         {"always_encrypt", "blocked", "created_at", "deleted_at", "email", "id", "mailbox_pubkey", "name", "notes", "npub", "organization", "phone", "updated_at"},
	"domains":          {"active", "created_at", "dkim_private_key", "dkim_selector", "domain", "id", "updated_at", "verified"},
	"email_bounces":    {"bounce_type", "created_at", "diagnostic_code", "id", "original_message_id", "original_recipient", "reason", "received_at", "remote_server", "sender_pubkey"},
	"email_complaints": {"feedback_type", "id", "original_message_id", "original_recipient", "received_at", "reporting_mta", "sender_pubkey"},
	"email_templates":  {"body", "created_at", "deleted_at", "id", "is_signature", "mailbox_pubkey", "name", "subject", "updated_at"},
	"emails":           {"bcc", "body", "cc", "created_at", "deleted_at", "direction", "encryption_mode", "encryption_nonce", "folder", "from_address", "html_body", "id", "in_reply_to", "is_encrypted", "labels", "mailbox_pubkey", "message_id", "nostr_verification_error", "nostr_verified", "nostr_verified_at", "read_at", "recipient_npub", "references_header", "sender_npub", "status", "subject", "to_address", "updated_at"},
	"encryption_keys":  {"contact_npub", "created_at", "id", "imported", "key_type", "mailbox_pubkey", "public_key", "updated_at", "verified"},
	"mailboxes":        {"created_at", "deleted_at", "display_name", "preferred_encryption_mode", "pubkey", "send_elevated", "send_enabled", "send_suspended_at", "updated_at"},
	"nip05_cache":      {"cached_at", "email", "expires_at", "id", "npub", "valid"},
	"outbound_queue":   {"attempts", "created_at", "id", "last_attempt", "last_error", "max_attempts", "message_id", "metadata", "next_attempt", "raw_message", "recipients", "sender", "status"},
	"sessions":         {"created_at", "deleted_at", "expires_at", "id", "mailbox_pubkey", "token"},
}

// errSchemaMissing lists required objects the database lacks.
var errSchemaMissing = errors.New("missing")

// verifyRequired returns an error naming every required table.column absent
// from schema email, or nil when all are present.
func verifyRequired(ctx context.Context, q querier) error {
	rows, err := q.QueryContext(ctx, `
		SELECT table_name, column_name FROM information_schema.columns
		 WHERE table_schema = 'email'`)
	if err != nil {
		return fmt.Errorf("read email columns: %w", err)
	}
	defer func() { _ = rows.Close() }()
	have := map[string]bool{}
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			return fmt.Errorf("scan email columns: %w", err)
		}
		have[t+"."+c] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read email columns: %w", err)
	}

	var missing []string
	for table, cols := range requiredColumns {
		for _, c := range cols {
			if !have[table+"."+c] {
				missing = append(missing, table+"."+c)
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("%w: %s", errSchemaMissing, strings.Join(missing, ", "))
}
