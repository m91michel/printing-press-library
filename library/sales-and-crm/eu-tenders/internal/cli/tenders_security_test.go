// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/store"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

func md5File(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// runTenders executes the root command and returns stdout, stderr and the exit code.
func runTenders(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	testenv.Isolate(t)
	cmd := RootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	code := 0
	if err != nil {
		code = ExitCode(err)
	}
	return stdout.String(), stderr.String(), code
}

func seededSecurityDB(t *testing.T) string {
	t.Helper()
	return seedTendersDB(t, []ted.Notice{
		awardNotice("1-2026", daysFromToday(-2), "Land Berlin", "DEU", "45210000", 100000,
			ted.Winner{Name: "Bau GmbH", Country: "DEU", LotsWon: 1}),
		callNotice("2-2026", daysFromToday(-1), "Land Berlin", "DEU", "45210000", 50000, daysFromToday(10)),
	})
}

func TestValidateReadOnlySQL(t *testing.T) {
	ok := map[string]string{
		"SELECT 1":                                     "SELECT 1",
		"SELECT 1;":                                    "SELECT 1",
		"  select 1 ;  ":                               "select 1",
		"WITH x AS (SELECT 1) SELECT * FROM x":         "WITH x AS (SELECT 1) SELECT * FROM x",
		"SELECT 'a;b' AS v":                            "SELECT 'a;b' AS v",
		"SELECT 'it''s; DROP TABLE x' AS v":            "SELECT 'it''s; DROP TABLE x' AS v",
		`SELECT "delete" FROM notices`:                 `SELECT "delete" FROM notices`,
		"SELECT replace(title, 'a', 'b') FROM notices": "SELECT replace(title, 'a', 'b') FROM notices",
		"SELECT updated_at FROM ted_sync_state":        "SELECT updated_at FROM ted_sync_state",
	}
	for in, want := range ok {
		got, err := validateReadOnlySQL(in)
		if err != nil || got != want {
			t.Errorf("validateReadOnlySQL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"SELECT 1; ATTACH DATABASE 'x' AS z",
		"SELECT 1;;",
		"SELECT 1; SELECT 2",
		"ATTACH DATABASE 'x' AS z",
		"WITH x AS (SELECT 1) DELETE FROM notices",
		"WITH x AS (SELECT 1) INSERT INTO lead_seen SELECT 1,2,3,4",
		"SELECT * FROM notices WHERE 1 /* ' */ ; attach 'x' as z --'",
		"SELECT 1 -- '\n; PRAGMA query_only=0",
		"select 1 where 0 union select 1; vacuum",
		"PRAGMA query_only=0",
		"WITH x AS (SELECT 1) REPLACE INTO lead_seen VALUES (1,2,3,4)",
		"UPDATE notices SET title=''",
	}
	for _, in := range bad {
		if got, err := validateReadOnlySQL(in); err == nil {
			t.Errorf("validateReadOnlySQL(%q) = %q, want an error", in, got)
		}
	}
}

func TestSQLCommandSingleStatementAndReadOnly(t *testing.T) {
	db := seededSecurityDB(t)
	before := md5File(t, db)

	attach := filepath.Join(t.TempDir(), "x.db")
	_, stderr, code := runTenders(t, "sql", "SELECT 1; ATTACH DATABASE '"+attach+"' AS z", "--db", db, "--json")
	if code != 2 {
		t.Fatalf("multi-statement sql: want exit 2, got %d (%s)", code, stderr)
	}
	if _, err := os.Stat(attach); !os.IsNotExist(err) {
		t.Fatalf("ATTACH must not create %s", attach)
	}

	for _, q := range []string{"WITH x AS (SELECT 1 AS v) SELECT * FROM x", "SELECT 'a;b' AS v"} {
		stdout, stderr, code := runTenders(t, "sql", q, "--db", db, "--json")
		if code != 0 {
			t.Fatalf("%q: exit %d: %s", q, code, stderr)
		}
		var rows []map[string]any
		if err := json.Unmarshal([]byte(stdout), &rows); err != nil || len(rows) != 1 {
			t.Fatalf("%q: rows %v err %v (%s)", q, rows, err, stdout)
		}
	}
	stdout, stderr, code := runTenders(t, "sql", "SELECT COUNT(*) AS n FROM notices", "--db", db, "--json")
	if code != 0 || !strings.Contains(stdout, `"n": 2`) {
		t.Fatalf("count query: exit %d stdout %s stderr %s", code, stdout, stderr)
	}
	if after := md5File(t, db); after != before {
		t.Fatalf("sql changed the database file: %s -> %s", before, after)
	}
}

func TestReadCommandsLeaveStoreUnchanged(t *testing.T) {
	db := seededSecurityDB(t)
	before := md5File(t, db)
	for _, args := range [][]string{
		{"buyer", "--name", "Berlin"},
		{"search", "Award"},
		{"awards", "--country", "DEU", "--data-source", "local"},
		{"deadline", "--country", "DEU", "--data-source", "local"},
		{"velocity", "--country", "DEU", "--window", "30d"},
	} {
		_, stderr, code := runTenders(t, append(args, "--db", db, "--json")...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, stderr)
		}
		if after := md5File(t, db); after != before {
			t.Fatalf("%v changed the database file", args)
		}
	}
}

func TestReadCommandsTreatForeignDBAsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foreign.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE other(x)`); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	before := md5File(t, path)

	stdout, stderr, code := runTenders(t, "buyer", "--name", "Berlin", "--db", path, "--json")
	if code != 0 || strings.TrimSpace(stdout) != "[]" || !strings.Contains(stderr, "sync") {
		t.Fatalf("buyer on a foreign db: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	stdout, stderr, code = runTenders(t, "awards", "--country", "DEU", "--data-source", "local", "--db", path, "--json")
	if code != 0 || strings.TrimSpace(stdout) != "[]" || !strings.Contains(stderr, "sync") {
		t.Fatalf("awards on a foreign db: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if after := md5File(t, path); after != before {
		t.Fatal("read commands migrated a foreign database")
	}
	if st, err := store.OpenQueryOnly(t.Context(), path); err == nil {
		has, _ := st.HasNoticesTable(t.Context())
		_ = st.Close()
		if has {
			t.Fatal("notices table must not be created by read commands")
		}
	}
}

func TestValidateTEDFilters(t *testing.T) {
	cases := []struct {
		country, cpv string
		ok           bool
	}{
		{"", "", true},
		{"DEU", "45", true},
		{"deu", "45210000", true},
		{"FRA", "45210000-2", true},
		{"DE", "", false},
		{"DEUT", "", false},
		{"DEU OR 1=1", "", false},
		{"D1U", "", false},
		{"", "45x", false},
		{"", "452100001", false},
		{"", "45210000-23", false},
		{"", "45%", false},
		{"", "45 OR notice-type=x", false},
	}
	for _, c := range cases {
		err := validateTEDFilters(c.country, c.cpv)
		if (err == nil) != c.ok {
			t.Errorf("validateTEDFilters(%q, %q) = %v, want ok=%v", c.country, c.cpv, err, c.ok)
		}
		if err != nil && ExitCode(err) != 2 {
			t.Errorf("validateTEDFilters(%q, %q) exit code %d, want 2", c.country, c.cpv, ExitCode(err))
		}
	}
}

func TestFlagValidationRejectsQueryInjection(t *testing.T) {
	db := seededSecurityDB(t)
	for _, args := range [][]string{
		{"leads", "--country", "DEU OR 1=1"},
		{"awards", "--cpv", "45 OR x"},
		{"deadline", "--country", "DE"},
		{"score", "--cpv", "4%"},
		{"deadline-heat", "--country", "XX1"},
		{"winner", "Bau", "--country", "DEU)"},
		{"concentration", "--country", "DEUU"},
		{"win-rate", "--cpv", "abc"},
		{"dark-buyers", "--country", "1"},
		{"velocity", "--cpv", "45;"},
		{"cpv-drift", "--country", "DEU OR"},
		{"buyer", "--name", "x", "--country", "D"},
		{"sync", "--country", "DEU OR 1=1"},
		{"sync", "--param", "cpv=45 OR x"},
	} {
		_, stderr, code := runTenders(t, append(args, "--db", db, "--json")...)
		if code != 2 {
			t.Errorf("%v: want exit 2, got %d (%s)", args, code, stderr)
		}
	}
}

func TestTedQuotedStripsQuotesAndBackslashes(t *testing.T) {
	cases := map[string]string{
		`Bunte`:            `"Bunte"`,
		` Johann "Bunte" `: `"Johann Bunte"`,
		`Bunte\`:           `"Bunte"`,
		`a\" OR x`:         `"a OR x"`,
	}
	for in, want := range cases {
		if got := tedQuoted(in); got != want {
			t.Errorf("tedQuoted(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestCapMaxScan(t *testing.T) {
	t.Setenv(cliutil.DogfoodEnvVar, "")
	for in, want := range map[int]int{0: 0, -1: -1, 500: 500, 10000: 10000, 10001: 10000, 1 << 30: 10000} {
		if got := capMaxScan(in); got != want {
			t.Errorf("capMaxScan(%d) = %d, want %d", in, got, want)
		}
	}
	t.Setenv(cliutil.DogfoodEnvVar, "1")
	for in, want := range map[int]int{0: 50, 10: 10, 500: 50, 1 << 30: 50} {
		if got := capMaxScan(in); got != want {
			t.Errorf("dogfood capMaxScan(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestVelocityRejectsHugeWindow(t *testing.T) {
	for _, w := range []string{"300y", "99999999999d", "999999w", "2000000h"} {
		if _, err := parseWindow("window", w); err == nil {
			t.Errorf("parseWindow(%q) should fail", w)
		}
	}
	if d, err := parseWindow("window", "200y"); err != nil || d != maxWindow {
		t.Errorf("parseWindow(200y) = %v, %v", d, err)
	}
	_, _, code := runTenders(t, "velocity", "--window", "300y", "--json")
	if code != 2 {
		t.Fatalf("velocity --window 300y: want exit 2, got %d", code)
	}
}

// fakeTED serves 300 notices in ITERATION pages of the requested size; the
// last page is flagged timedOut.
func fakeTED(t *testing.T) (*httptest.Server, *[]ted.SearchRequest) {
	t.Helper()
	const total = 300
	var reqs []ted.SearchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ted.SearchRequest
		_ = json.Unmarshal(body, &req)
		reqs = append(reqs, req)
		start := 0
		if req.IterationNextToken != "" {
			start, _ = strconv.Atoi(req.IterationNextToken)
		}
		end := start + req.Limit
		if end > total {
			end = total
		}
		notices := make([]map[string]any, 0, end-start)
		for i := start; i < end; i++ {
			typ := ted.NoticeTypeAward
			if i%2 == 1 {
				typ = ted.NoticeTypeCall
			}
			notices = append(notices, map[string]any{"publication-number": fmt.Sprintf("%d-2026", i+1), "notice-type": typ})
		}
		page := map[string]any{"totalNoticeCount": total, "notices": notices}
		if end < total {
			page["iterationNextToken"] = strconv.Itoa(end)
		} else {
			page["timedOut"] = true
		}
		_ = json.NewEncoder(w).Encode(page)
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

func TestSyncPagesWithIterationTokens(t *testing.T) {
	srv, reqs := fakeTED(t)
	testenv.Isolate(t)
	t.Setenv("EU_TENDERS_BASE_URL", srv.URL)
	t.Setenv(cliutil.DogfoodEnvVar, "")
	db := filepath.Join(t.TempDir(), "sync.db")

	var res syncResult
	runTendersJSONNoIsolate(t, &res, "sync", "--since", "7d", "--db", db)
	if res.Synced != 300 || res.TotalMatching != 300 || res.Truncated || res.Awards != 150 || res.Calls != 150 {
		t.Fatalf("sync result %+v", res)
	}
	if len(*reqs) != 2 || (*reqs)[0].PaginationMode != ted.PaginationIteration || (*reqs)[1].IterationNextToken != "250" {
		t.Fatalf("requests %+v", *reqs)
	}

	*reqs = nil
	var capped syncResult
	runTendersJSONNoIsolate(t, &capped, "sync", "--since", "7d", "--max-pages", "1", "--db", db)
	if capped.Synced != 250 || !capped.Truncated || len(*reqs) != 1 {
		t.Fatalf("--max-pages 1: %+v after %d requests", capped, len(*reqs))
	}

	*reqs = nil
	var limited syncResult
	runTendersJSONNoIsolate(t, &limited, "sync", "--since", "7d", "--limit", "260", "--db", db)
	if limited.Synced != 260 || !limited.Truncated || len(*reqs) != 2 || (*reqs)[1].Limit != 10 {
		t.Fatalf("--limit 260: %+v after %+v", limited, *reqs)
	}
}

// runTendersJSONNoIsolate is runTendersJSON for tests that set env vars
// after isolating, which a second Isolate call would reset.
func runTendersJSONNoIsolate(t *testing.T, out any, args ...string) {
	t.Helper()
	cmd := RootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append(args, "--json"))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%v: %v\nstderr: %s", args, err, stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		t.Fatalf("%v: decoding JSON: %v\nstdout: %s", args, err, stdout.String())
	}
}
