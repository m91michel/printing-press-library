// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

type agentEnvelope struct {
	Meta struct {
		Source string `json:"source"`
	} `json:"meta"`
	Results json.RawMessage `json:"results"`
}

func decodeAgentEnvelope(t *testing.T, stdout string) agentEnvelope {
	t.Helper()
	var env agentEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("decoding --agent output: %v\n%s", err, stdout)
	}
	return env
}

// fullPagesTED serves exactly total notices and, like TED, keeps returning a
// next token even after the last full page.
func fullPagesTED(t *testing.T, total int) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		var req ted.SearchRequest
		_ = json.Unmarshal(body, &req)
		start := 0
		if req.IterationNextToken != "" {
			start, _ = strconv.Atoi(req.IterationNextToken)
		}
		end := min(start+req.Limit, total)
		notices := make([]map[string]any, 0, end-start)
		for i := start; i < end; i++ {
			notices = append(notices, map[string]any{"publication-number": fmt.Sprintf("%d-2026", i+1), "notice-type": ted.NoticeTypeAward})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"totalNoticeCount": total, "notices": notices, "iterationNextToken": strconv.Itoa(end),
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestAutoCommandsReportTheSourceTheyUsed(t *testing.T) {
	db := seededSecurityDB(t)
	stdout, stderr, code := runTenders(t, "awards", "--since", "365d", "--data-source", "auto", "--agent", "--db", db)
	if code != 0 {
		t.Fatalf("awards: exit %d: %s", code, stderr)
	}
	if env := decodeAgentEnvelope(t, stdout); env.Meta.Source != "local" {
		t.Fatalf("awards from a seeded store: meta.source %q, want local", env.Meta.Source)
	}
	stdout, stderr, code = runTenders(t, "deadline", "--country", "DEU", "--agent", "--db", db)
	if code != 0 {
		t.Fatalf("deadline: exit %d: %s", code, stderr)
	}
	if env := decodeAgentEnvelope(t, stdout); env.Meta.Source != "local" {
		t.Fatalf("deadline from a seeded store: meta.source %q, want local", env.Meta.Source)
	}

	srv, _ := fullPagesTED(t, 3)
	testenv.Isolate(t)
	t.Setenv("EU_TENDERS_BASE_URL", srv.URL)
	empty := filepath.Join(t.TempDir(), "none.db")
	cmd := RootCmd()
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"awards", "--since", "30d", "--agent", "--db", empty})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("awards live fallback: %v (%s)", err, errOut.String())
	}
	if env := decodeAgentEnvelope(t, out.String()); env.Meta.Source != "live" {
		t.Fatalf("awards without a store: meta.source %q, want live", env.Meta.Source)
	}
}

func TestPageTEDExactlyFullPagesAreNotTruncated(t *testing.T) {
	srv, calls := fullPagesTED(t, 2*tedPageSize)
	testenv.Isolate(t)
	t.Setenv("EU_TENDERS_BASE_URL", srv.URL)
	t.Setenv(cliutil.DogfoodEnvVar, "")
	db := filepath.Join(t.TempDir(), "sync.db")

	var res syncResult
	runTendersJSONNoIsolate(t, &res, "sync", "--since", "7d", "--max-pages", "2", "--db", db)
	if res.Synced != 2*tedPageSize || res.Truncated || *calls != 2 {
		t.Fatalf("total == pageSize*maxPages: %+v after %d requests", res, *calls)
	}
}

func TestSQLRowCap(t *testing.T) {
	db := seededSecurityDB(t)
	stdout, stderr, code := runTenders(t, "sql", "SELECT id FROM notices", "--limit", "1", "--db", db, "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("rows %v err %v (%s)", rows, err, stdout)
	}
	if !strings.Contains(stderr, "stopped after 1 rows; raise --limit") {
		t.Fatalf("missing row-cap note: %q", stderr)
	}

	stdout, stderr, code = runTenders(t, "sql", "SELECT id FROM notices", "--limit", "2", "--db", db, "--json")
	if code != 0 || strings.Contains(stderr, "stopped after") {
		t.Fatalf("limit equal to row count: exit %d stderr %q", code, stderr)
	}
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("rows %v err %v", rows, err)
	}
}

func TestSQLTimeoutStopsRunawayQuery(t *testing.T) {
	db := seededSecurityDB(t)
	start := time.Now()
	_, stderr, code := runTenders(t, "sql",
		"WITH RECURSIVE r(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM r) SELECT count(*) FROM r",
		"--timeout", "300ms", "--db", db, "--json")
	if code == 0 {
		t.Fatal("runaway CTE must fail at --timeout")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("runaway CTE ran %s past a 300ms timeout", elapsed)
	}
	if !strings.Contains(stderr, "deadline exceeded") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestCapSQLText(t *testing.T) {
	if got := capSQLText("short"); got != "short" {
		t.Fatalf("short value changed: %q", got)
	}
	long := strings.Repeat("ä", sqlMaxValueBytes) // 2 bytes per rune
	got := capSQLText(long)
	if !strings.HasSuffix(got, sqlTruncatedMarker) || len(got) > sqlMaxValueBytes+len(sqlTruncatedMarker) {
		t.Fatalf("capped length %d", len(got))
	}
	if body := strings.TrimSuffix(got, sqlTruncatedMarker); !strings.HasPrefix(long, body) || len(body)%2 != 0 {
		t.Fatal("cap split a UTF-8 sequence")
	}
}

func TestValidateReadOnlySQLRejectsAttachAndPragma(t *testing.T) {
	for _, in := range []string{"ATTACH DATABASE 'x' AS y", "PRAGMA query_only=0"} {
		if got, err := validateReadOnlySQL(in); err == nil {
			t.Errorf("validateReadOnlySQL(%q) = %q, want an error", in, got)
		}
	}
}

// leads queries TED live in auto mode, so it must not claim to be a
// local-only writer (that annotation makes MCP hosts treat it as closed-world).
func TestLeadsAnnotationsKeepOpenWorld(t *testing.T) {
	leads, _, err := RootCmd().Find([]string{"leads"})
	if err != nil || leads.Name() != "leads" {
		t.Fatalf("leads command not found: %v", err)
	}
	for _, k := range []string{"mcp:local-write", "mcp:read-only"} {
		if _, ok := leads.Annotations[k]; ok {
			t.Errorf("leads must not carry %s", k)
		}
	}
	if leads.Annotations["pp:data-source"] != "auto" || leads.Annotations["pp:happy-args"] == "" {
		t.Errorf("leads annotations %v", leads.Annotations)
	}
}

func TestSQLRefusesOversizedValuesInsideSQLite(t *testing.T) {
	db := seedTendersDB(t, []ted.Notice{awardNotice("1-2026", daysFromToday(-1), "Stadt A", "DEU", "45210000", 1)})
	testenv.Isolate(t)
	cmd := RootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"sql", "SELECT zeroblob(300000000) AS a", "--db", db, "--json"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "too big") {
		t.Fatalf("want SQLite length-limit error, got %v", err)
	}
}
