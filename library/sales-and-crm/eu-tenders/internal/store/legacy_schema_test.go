// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

// legacyNoticesDDL is the store layout of the previous major version: a
// notices table without winner_count and an external-content FTS index.
var legacyNoticesDDL = []string{
	`CREATE TABLE notices (
		id TEXT PRIMARY KEY, notice_type TEXT, publication_date TEXT, buyer_name TEXT,
		buyer_country TEXT, cpv_code TEXT, cpv_codes_json TEXT, estimated_value REAL,
		currency TEXT DEFAULT 'EUR', winner_name TEXT, winner_country TEXT, contract_value REAL,
		procedure_type TEXT, submission_deadline TEXT, title TEXT, place_of_performance TEXT,
		notice_url TEXT, previous_notice_id TEXT, raw_data TEXT, synced_at TEXT)`,
	`CREATE VIRTUAL TABLE notices_fts USING fts5(title, buyer_name, winner_name, content=notices, content_rowid=rowid)`,
	`CREATE INDEX idx_notices_type_date ON notices(notice_type, publication_date)`,
	`CREATE TRIGGER notices_ai AFTER INSERT ON notices BEGIN
		INSERT INTO notices_fts(rowid, title, buyer_name, winner_name) VALUES (new.rowid, new.title, new.buyer_name, new.winner_name);
	END`,
	`INSERT INTO notices (id, notice_type, publication_date, buyer_name, buyer_country, cpv_code, title, winner_name)
		VALUES ('1-2025', 'can-standard', '2025-01-02', 'Stadt Alt', 'DEU', '45210000', 'Neubau Schule', 'Alt Bau GmbH')`,
}

func writeLegacyStore(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range legacyNoticesDDL {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy DDL %q: %v", stmt, err)
		}
	}
}

func TestOpenRetiresLegacyNoticesSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	writeLegacyStore(t, path)

	ro, err := OpenQueryOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := ro.NoticesSchema(ctx); err != nil || state != NoticesSchemaLegacy {
		t.Fatalf("query-only schema state = %q, %v; want legacy", state, err)
	}
	_ = ro.Close()

	st, err := OpenWithContext(ctx, path)
	if err != nil {
		t.Fatalf("opening a legacy store must migrate it: %v", err)
	}
	if state, err := st.NoticesSchema(ctx); err != nil || state != NoticesSchemaCurrent {
		t.Fatalf("schema state after open = %q, %v; want current", state, err)
	}
	var legacyRows int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM ` + LegacyNoticesTable + ` WHERE winner_name = 'Alt Bau GmbH'`).Scan(&legacyRows); err != nil || legacyRows != 1 {
		t.Fatalf("legacy row in %s: %d, %v", LegacyNoticesTable, legacyRows, err)
	}
	var idxTable string
	if err := st.DB().QueryRow(`SELECT tbl_name FROM sqlite_master WHERE type='index' AND name='idx_notices_type_date'`).Scan(&idxTable); err != nil || idxTable != "notices" {
		t.Fatalf("idx_notices_type_date belongs to %q (%v); want the new notices table", idxTable, err)
	}
	n := ted.Notice{ID: "2-2026", NoticeType: ted.NoticeTypeAward, Title: "Rohbau", Winners: []ted.Winner{{Name: "Neu GmbH"}}}
	if err := st.UpsertNotices(ctx, []ted.Notice{n}, nil); err != nil {
		t.Fatalf("upsert into the rebuilt schema: %v", err)
	}
	_ = st.Close()

	// A second open is a no-op: the current table is left alone.
	st, err = OpenWithContext(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if c, err := st.NoticeCount(ctx, ""); err != nil || c != 1 {
		t.Fatalf("notice count after reopen = %d, %v; want 1", c, err)
	}
}

func TestRetireLegacyNoticesPicksFreeName(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	writeLegacyStore(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE ` + LegacyNoticesTable + ` (id TEXT)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	st, err := OpenWithContext(ctx, path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	var rows int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM ` + LegacyNoticesTable + `_2`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("second retirement table: %d rows, %v", rows, err)
	}
}

func TestClaimLeadsClaimsEachCompanyOnce(t *testing.T) {
	ctx := context.Background()
	st, err := OpenWithContext(ctx, filepath.Join(t.TempDir(), "claims.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := LeadKey{NameKey: "a gmbh", Country: "DEU"}
	b := LeadKey{NameKey: "b gmbh", Country: "DEU"}

	first, err := st.ClaimLeads(ctx, []LeadKey{a, a})
	if err != nil || !first[a] {
		t.Fatalf("first claim of a = %v, %v; want true", first, err)
	}
	// A concurrent run that passed the same pre-filter loses the claim.
	second, err := st.ClaimLeads(ctx, []LeadKey{a, b})
	if err != nil || second[a] || !second[b] {
		t.Fatalf("second claim = %v, %v; want a=false b=true", second, err)
	}
	seen, err := st.SeenLeads(ctx, []LeadKey{a, b})
	if err != nil || !seen[a] || !seen[b] {
		t.Fatalf("SeenLeads after claims = %v, %v", seen, err)
	}
}
