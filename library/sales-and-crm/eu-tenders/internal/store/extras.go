// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// migrateExtras runs after the generated store migrations and before the
// schema-version stamp. It is the canonical place for novel-feature auxiliary
// tables that need to live in the local store.
//
// Edit this file when adding tables for novel commands. Keep migrations
// idempotent with CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS so
// every store open can safely re-run them.
func (s *Store) migrateExtras(ctx context.Context, conn *sql.Conn) error {
	if err := retireLegacyNotices(ctx, conn); err != nil {
		return fmt.Errorf("retiring legacy notices schema: %w", err)
	}
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS notices (
			id TEXT PRIMARY KEY,
			notice_type TEXT NOT NULL DEFAULT '',
			publication_date TEXT NOT NULL DEFAULT '',
			buyer_name TEXT NOT NULL DEFAULT '',
			buyer_country TEXT NOT NULL DEFAULT '',
			buyer_city TEXT NOT NULL DEFAULT '',
			buyer_email TEXT NOT NULL DEFAULT '',
			cpv_code TEXT NOT NULL DEFAULT '',
			cpv_codes_json TEXT NOT NULL DEFAULT '[]',
			estimated_value REAL NOT NULL DEFAULT 0,
			contract_value REAL NOT NULL DEFAULT 0,
			currency TEXT NOT NULL DEFAULT 'EUR',
			procedure_type TEXT NOT NULL DEFAULT '',
			submission_deadline TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			place_of_performance TEXT NOT NULL DEFAULT '',
			performance_city TEXT NOT NULL DEFAULT '',
			previous_notice_id TEXT NOT NULL DEFAULT '',
			notice_url TEXT NOT NULL DEFAULT '',
			winner_count INTEGER NOT NULL DEFAULT 0,
			raw_data TEXT NOT NULL DEFAULT '{}',
			synced_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notices_type_date ON notices(notice_type, publication_date)`,
		`CREATE INDEX IF NOT EXISTS idx_notices_buyer ON notices(buyer_country, buyer_name)`,
		`CREATE INDEX IF NOT EXISTS idx_notices_cpv ON notices(cpv_code)`,
		`CREATE TABLE IF NOT EXISTS notice_winners (
			notice_id TEXT NOT NULL,
			name TEXT NOT NULL,
			name_key TEXT NOT NULL,
			country TEXT NOT NULL DEFAULT '',
			city TEXT NOT NULL DEFAULT '',
			post_code TEXT NOT NULL DEFAULT '',
			nuts TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			phone TEXT NOT NULL DEFAULT '',
			identifier TEXT NOT NULL DEFAULT '',
			size TEXT NOT NULL DEFAULT '',
			lots_won INTEGER NOT NULL DEFAULT 0,
			value REAL NOT NULL DEFAULT 0,
			PRIMARY KEY (notice_id, name_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notice_winners_key ON notice_winners(name_key, country)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS notices_fts USING fts5(
			notice_id UNINDEXED, title, buyer_name, winner_names
		)`,
		`CREATE TABLE IF NOT EXISTS lead_seen (
			name_key TEXT NOT NULL,
			country TEXT NOT NULL DEFAULT '',
			first_seen_at TEXT NOT NULL,
			last_seen_at TEXT NOT NULL,
			PRIMARY KEY (name_key, country)
		)`,
		`CREATE TABLE IF NOT EXISTS ted_sync_state (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
	}
	for _, m := range migrations {
		if _, err := conn.ExecContext(ctx, m); err != nil {
			return fmt.Errorf("extra migration failed: %w", err)
		}
	}
	return nil
}

// LegacyNoticesTable holds the rows of a notices table written by an older
// binary. Its columns differ from the current notices table, so the
// current schema is created next to it; the old rows stay queryable.
const LegacyNoticesTable = "notices_legacy_v1"

// retireLegacyNotices moves an older binary's notices table aside so the
// CREATE TABLE IF NOT EXISTS statements below build the current schema.
// Without it the old table survives and every query on the new columns
// fails with "no such column". It runs on the migration connection so it
// shares the in-flight migration transaction.
func retireLegacyNotices(ctx context.Context, conn *sql.Conn) error {
	cols, exists, err := tableColumns(ctx, conn, "notices")
	if err != nil {
		return err
	}
	if exists && !cols[currentNoticesMarkerColumn] {
		// Older binaries kept the FTS table in sync with triggers on
		// notices. They write into the FTS table dropped below, so they
		// would break both the rename and any later write.
		if err := dropSchemaObjects(ctx, conn, "trigger", "notices"); err != nil {
			return err
		}
		if err := dropLegacyFTS(ctx, conn); err != nil {
			return err
		}
		target, err := freeTableName(ctx, conn, LegacyNoticesTable)
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `ALTER TABLE notices RENAME TO "`+target+`"`); err != nil {
			return fmt.Errorf("rename legacy notices to %s: %w", target, err)
		}
		// Indexes move with the renamed table and keep their names, which
		// would turn the current schema's CREATE INDEX IF NOT EXISTS into
		// no-ops on the new table.
		if err := dropSchemaObjects(ctx, conn, "index", target); err != nil {
			return err
		}
	}
	return dropLegacyFTS(ctx, conn)
}

// currentNoticesMarkerColumn exists only in the current notices schema.
const currentNoticesMarkerColumn = "winner_count"

// dropLegacyFTS drops a notices_fts table that lacks the notice_id column
// of the current standalone index. Dropping the virtual table also drops
// its shadow tables. The check reads the stored DDL so it works even when
// the old table's module cannot be loaded.
func dropLegacyFTS(ctx context.Context, conn *sql.Conn) error {
	var ddl string
	err := conn.QueryRowContext(ctx, `SELECT COALESCE(sql, '') FROM sqlite_master WHERE type='table' AND name='notices_fts'`).Scan(&ddl)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking notices_fts: %w", err)
	}
	if strings.Contains(strings.ToLower(ddl), "notice_id") {
		return nil
	}
	if _, err := conn.ExecContext(ctx, `DROP TABLE notices_fts`); err != nil {
		return fmt.Errorf("drop legacy notices_fts: %w", err)
	}
	return nil
}

// tableColumns returns the column names of a table and whether it exists.
func tableColumns(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, table string) (map[string]bool, bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, false, fmt.Errorf("table_info %s: %w", table, err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, false, err
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return cols, len(cols) > 0, nil
}

// dropSchemaObjects drops the explicitly created triggers or indexes on
// table. Automatic indexes (sql IS NULL) belong to constraints and stay.
func dropSchemaObjects(ctx context.Context, conn *sql.Conn, kind, table string) error {
	rows, err := conn.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type=? AND tbl_name=? AND sql IS NOT NULL`, kind, table)
	if err != nil {
		return fmt.Errorf("listing %ss on %s: %w", kind, table, err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, name := range names {
		stmt := "DROP " + strings.ToUpper(kind) + ` IF EXISTS "` + strings.ReplaceAll(name, `"`, `""`) + `"`
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("drop %s %s: %w", kind, name, err)
		}
	}
	return nil
}

// freeTableName returns base, or base with a numeric suffix when an earlier
// retirement already used that name.
func freeTableName(ctx context.Context, conn *sql.Conn, base string) (string, error) {
	for i := 1; i < 1000; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s_%d", base, i)
		}
		var n int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name=?`, name).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return name, nil
		}
	}
	return "", fmt.Errorf("no free table name for %s", base)
}
