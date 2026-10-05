// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

// OpenQueryOnly opens an existing store for queries only: no MkdirAll and no
// migrations. mode=ro makes the main file read-only. The query_only pragma
// is set per pooled connection by the DSN and refuses writes to every schema
// on that connection only while it stays set; a statement that runs
// PRAGMA query_only=0 could lift it, and ATTACH could open another file.
// Callers that run user-supplied SQL must therefore reject PRAGMA, ATTACH
// and write statements themselves (the sql command's validateReadOnlySQL
// does this). The path is percent-escaped so spaces, '?' and '#' in a
// directory name survive SQLite's URI parsing.
func OpenQueryOnly(ctx context.Context, dbPath string) (*Store, error) {
	escaped := (&url.URL{Path: filepath.ToSlash(dbPath)}).EscapedPath()
	dsn := "file:" + escaped + "?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	if err := ensureSQLiteDriverInitialized(ctx, dsn); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database (query-only): %w", err)
	}
	db.SetMaxOpenConns(2)
	s := &Store{db: db, path: dbPath}
	// sql.Open is lazy; ping so a missing or unreadable file fails here
	// instead of on the first query.
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("opening database (query-only): %w", err)
	}
	return s, nil
}

// HasNoticesTable reports whether the TED tables exist. A database created
// by another tool, or by an older binary before the first sync, lacks them.
func (s *Store) HasNoticesTable(ctx context.Context) (bool, error) {
	state, err := s.NoticesSchema(ctx)
	return state != NoticesSchemaMissing, err
}

// NoticesSchemaState classifies the notices table of a store.
type NoticesSchemaState string

const (
	// NoticesSchemaMissing: no notices table (fresh file or foreign database).
	NoticesSchemaMissing NoticesSchemaState = "missing"
	// NoticesSchemaLegacy: a notices table written by an older binary. A
	// writable open retires it; a query-only handle cannot.
	NoticesSchemaLegacy NoticesSchemaState = "legacy"
	// NoticesSchemaCurrent: the notices table this binary reads and writes.
	NoticesSchemaCurrent NoticesSchemaState = "current"
)

// NoticesSchema reports whether the notices table is missing, legacy or
// current. It only reads the schema, so it is safe on query-only handles.
func (s *Store) NoticesSchema(ctx context.Context) (NoticesSchemaState, error) {
	cols, exists, err := tableColumns(ctx, s.db, "notices")
	if err != nil {
		return NoticesSchemaMissing, err
	}
	switch {
	case !exists:
		return NoticesSchemaMissing, nil
	case !cols[currentNoticesMarkerColumn]:
		return NoticesSchemaLegacy, nil
	}
	return NoticesSchemaCurrent, nil
}

// UpsertNotices writes notices and their winners in one transaction. Each
// notice's winners and FTS row are replaced, so a re-synced notice never
// keeps stale tokens or winners.
func (s *Store) UpsertNotices(ctx context.Context, notices []ted.Notice, raws []map[string]any) error {
	if len(notices) == 0 {
		return nil
	}
	s.lockForWrite()
	defer s.unlockAfterWrite()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin notice upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339)
	for i, n := range notices {
		if n.ID == "" {
			continue
		}
		cpvJSON, _ := json.Marshal(n.CPVCodes)
		rawJSON := []byte("{}")
		if i < len(raws) && raws[i] != nil {
			rawJSON, _ = json.Marshal(raws[i])
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notices
			(id, notice_type, publication_date, buyer_name, buyer_country, buyer_city, buyer_email,
			 cpv_code, cpv_codes_json, estimated_value, contract_value, currency, procedure_type,
			 submission_deadline, title, place_of_performance, performance_city, previous_notice_id,
			 notice_url, winner_count, raw_data, synced_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
			 notice_type=excluded.notice_type, publication_date=excluded.publication_date,
			 buyer_name=excluded.buyer_name, buyer_country=excluded.buyer_country,
			 buyer_city=excluded.buyer_city, buyer_email=excluded.buyer_email,
			 cpv_code=excluded.cpv_code, cpv_codes_json=excluded.cpv_codes_json,
			 estimated_value=excluded.estimated_value, contract_value=excluded.contract_value,
			 currency=excluded.currency, procedure_type=excluded.procedure_type,
			 submission_deadline=excluded.submission_deadline, title=excluded.title,
			 place_of_performance=excluded.place_of_performance, performance_city=excluded.performance_city,
			 previous_notice_id=excluded.previous_notice_id, notice_url=excluded.notice_url,
			 winner_count=excluded.winner_count, raw_data=excluded.raw_data, synced_at=excluded.synced_at`,
			n.ID, n.NoticeType, n.PublicationDate, n.BuyerName, n.BuyerCountry, n.BuyerCity, n.BuyerEmail,
			n.CPVCode, string(cpvJSON), n.EstimatedValue, n.ContractValue, n.Currency, n.ProcedureType,
			n.SubmissionDeadline, n.Title, n.PlaceOfPerformance, n.PerformanceCity, n.PreviousNoticeID,
			n.NoticeURL, len(n.Winners), string(rawJSON), now); err != nil {
			return fmt.Errorf("upsert notice %s: %w", n.ID, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM notice_winners WHERE notice_id=?`, n.ID); err != nil {
			return fmt.Errorf("clear winners for %s: %w", n.ID, err)
		}
		names := make([]string, 0, len(n.Winners))
		for _, w := range n.Winners {
			names = append(names, w.Name)
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO notice_winners
				(notice_id, name, name_key, country, city, post_code, nuts, email, phone, identifier, size, lots_won, value)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				n.ID, w.Name, ted.NormalizeName(w.Name), w.Country, w.City, w.PostCode, w.NUTS,
				w.Email, w.Phone, w.Identifier, w.Size, w.LotsWon, w.Value); err != nil {
				return fmt.Errorf("insert winner for %s: %w", n.ID, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM notices_fts WHERE notice_id=?`, n.ID); err != nil {
			return fmt.Errorf("clear fts for %s: %w", n.ID, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notices_fts(notice_id, title, buyer_name, winner_names) VALUES (?,?,?,?)`,
			n.ID, n.Title, n.BuyerName, strings.Join(names, " | ")); err != nil {
			return fmt.Errorf("index notice %s: %w", n.ID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit notice upsert: %w", err)
	}
	return nil
}

// SetTEDSyncState records a sync bookkeeping value such as the last sync time.
func (s *Store) SetTEDSyncState(ctx context.Context, key, value string) error {
	s.lockForWrite()
	defer s.unlockAfterWrite()
	_, err := s.db.ExecContext(ctx, `INSERT INTO ted_sync_state(key, value, updated_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339))
	return err
}

// TEDSyncState returns a sync bookkeeping value, or "" when unset.
func (s *Store) TEDSyncState(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM ted_sync_state WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// NoticeCount returns the number of synced notices, optionally of one type.
func (s *Store) NoticeCount(ctx context.Context, noticeType string) (int, error) {
	q := `SELECT COUNT(*) FROM notices`
	var args []any
	if noticeType != "" {
		q += ` WHERE notice_type=?`
		args = append(args, noticeType)
	}
	var n int
	err := s.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

// LeadKey identifies one company for lead seen-state.
type LeadKey struct {
	NameKey string
	Country string
}

// SeenLeads returns which of the given companies were recorded before.
func (s *Store) SeenLeads(ctx context.Context, keys []LeadKey) (map[LeadKey]bool, error) {
	out := map[LeadKey]bool{}
	for _, k := range keys {
		var one int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM lead_seen WHERE name_key=? AND country=?`, k.NameKey, k.Country).Scan(&one)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, nil
}

// ClaimLeads records companies for --new-only in one write transaction and
// reports which of them this call claimed. A company already in lead_seen,
// for example one claimed by a concurrent run since the caller's SeenLeads
// pre-filter, maps to false, so two runs never both return it.
func (s *Store) ClaimLeads(ctx context.Context, keys []LeadKey) (map[LeadKey]bool, error) {
	out := make(map[LeadKey]bool, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	s.lockForWrite()
	defer s.unlockAfterWrite()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, k := range keys {
		if _, done := out[k]; done {
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO lead_seen(name_key, country, first_seen_at, last_seen_at) VALUES (?,?,?,?)
			ON CONFLICT(name_key, country) DO NOTHING`, k.NameKey, k.Country, now, now)
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		out[k] = n == 1
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
