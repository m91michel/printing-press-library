// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

func fileMD5(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func TestOpenQueryOnlyRejectsWritesOnPathWithSpaces(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "dir with spaces #1")
	path := filepath.Join(dir, "my tenders.db")
	rw, err := OpenWithContext(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := rw.UpsertNotices(ctx, []ted.Notice{{ID: "1-2026", NoticeType: ted.NoticeTypeAward}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	before := fileMD5(t, path)

	ro, err := OpenQueryOnly(ctx, path)
	if err != nil {
		t.Fatalf("OpenQueryOnly on a path with spaces: %v", err)
	}
	defer ro.Close()
	if ok, err := ro.HasNoticesTable(ctx); err != nil || !ok {
		t.Fatalf("HasNoticesTable = %v, %v; want true", ok, err)
	}
	if n, err := ro.NoticeCount(ctx, ""); err != nil || n != 1 {
		t.Fatalf("NoticeCount = %d, %v; want 1", n, err)
	}
	if _, err := ro.DB().ExecContext(ctx, `INSERT INTO lead_seen VALUES ('x','DEU','t','t')`); err == nil {
		t.Fatal("INSERT through the query-only handle should fail")
	}
	attached := filepath.Join(t.TempDir(), "attached.db")
	if _, err := ro.DB().ExecContext(ctx, `ATTACH DATABASE ? AS z`, attached); err == nil {
		if _, err := ro.DB().ExecContext(ctx, `CREATE TABLE z.t(x)`); err == nil {
			t.Fatal("writing an attached database through the query-only handle should fail")
		}
	}
	if after := fileMD5(t, path); after != before {
		t.Fatalf("database file changed: %s -> %s", before, after)
	}
}

func TestOpenQueryOnlyMissingFileFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	if st, err := OpenQueryOnly(context.Background(), path); err == nil {
		_ = st.Close()
		t.Fatal("opening a missing file query-only should fail")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("OpenQueryOnly must not create the file, stat err = %v", err)
	}
}

func TestHasNoticesTableFalseOnForeignDB(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "foreign.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenQueryOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if ok, err := ro.HasNoticesTable(ctx); err != nil || ok {
		t.Fatalf("HasNoticesTable on an empty database = %v, %v; want false", ok, err)
	}
}
