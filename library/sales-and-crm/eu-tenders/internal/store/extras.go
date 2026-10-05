// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrateExtras runs after the generated store migrations and before the
// schema-version stamp. It is the canonical place for novel-feature auxiliary
// tables that need to live in the local store.
//
// Edit this file when adding tables for novel commands. Keep migrations
// idempotent with CREATE TABLE IF NOT EXISTS / CREATE INDEX IF NOT EXISTS so
// every store open can safely re-run them.
func (s *Store) migrateExtras(ctx context.Context, conn *sql.Conn) error {
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
