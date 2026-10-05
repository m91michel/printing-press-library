// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/store"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

// runTendersNoIsolate is runTenders for tests that prepare the sandboxed
// home or env vars first, which a second Isolate call would reset.
func runTendersNoIsolate(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	cmd := RootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	code := 0
	if err := cmd.Execute(); err != nil {
		code = ExitCode(err)
	}
	return stdout.String(), stderr.String(), code
}

// scriptedTED answers every search with respond(req), capped at req.Limit,
// and records the requests it saw.
func scriptedTED(t *testing.T, respond func(req ted.SearchRequest) []map[string]any) (*httptest.Server, func() []ted.SearchRequest) {
	t.Helper()
	var mu sync.Mutex
	var reqs []ted.SearchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ted.SearchRequest
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		reqs = append(reqs, req)
		mu.Unlock()
		notices := respond(req)
		total := len(notices)
		if req.Limit > 0 && len(notices) > req.Limit {
			notices = notices[:req.Limit]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"totalNoticeCount": total, "notices": notices})
	}))
	t.Cleanup(srv.Close)
	return srv, func() []ted.SearchRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]ted.SearchRequest(nil), reqs...)
	}
}

// liveEnv sandboxes the home and points the client at srv.
func liveEnv(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	home := testenv.Isolate(t)
	t.Setenv("EU_TENDERS_BASE_URL", srv.URL)
	t.Setenv(cliutil.DogfoodEnvVar, "")
	return home
}

func rawAward(id, date, buyer, country, cpv string, total float64, winners ...string) map[string]any {
	countries := make([]any, len(winners))
	names := make([]any, len(winners))
	for i, w := range winners {
		names[i] = w
		countries[i] = country
	}
	return map[string]any{
		"publication-number": id, "notice-type": ted.NoticeTypeAward, "publication-date": date + "+02:00",
		"buyer-name": buyer, "buyer-country": country, "classification-cpv": []any{cpv},
		"result-value-notice": total, "title-proc": "Neubau " + id,
		"organisation-name-tenderer": names, "winner-name": names, "organisation-country-tenderer": countries,
	}
}

func writeLegacyTendersDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`CREATE TABLE notices (
			id TEXT PRIMARY KEY, notice_type TEXT, publication_date TEXT, buyer_name TEXT,
			buyer_country TEXT, cpv_code TEXT, cpv_codes_json TEXT, estimated_value REAL,
			currency TEXT DEFAULT 'EUR', winner_name TEXT, winner_country TEXT, contract_value REAL,
			procedure_type TEXT, submission_deadline TEXT, title TEXT, place_of_performance TEXT,
			notice_url TEXT, previous_notice_id TEXT, raw_data TEXT, synced_at TEXT)`,
		`CREATE VIRTUAL TABLE notices_fts USING fts5(title, buyer_name, winner_name, content=notices, content_rowid=rowid)`,
		`INSERT INTO notices (id, notice_type, publication_date, buyer_name, buyer_country, cpv_code, title, winner_name)
			VALUES ('1-2025', 'can-standard', '2025-01-02', 'Stadt Alt', 'DEU', '45210000', 'Neubau Schule', 'Alt Bau GmbH')`,
		`INSERT INTO notices_fts(rowid, title, buyer_name, winner_name) SELECT rowid, title, buyer_name, winner_name FROM notices`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy DDL: %v", err)
		}
	}
}

func TestLegacyDefaultStoreIsReportedNotTouched(t *testing.T) {
	home := testenv.Isolate(t)
	legacy := filepath.Join(home, ".config", tendersCLIName, "notices.db")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	writeLegacyTendersDB(t, legacy)
	before := md5File(t, legacy)

	stdout, stderr, code := runTendersNoIsolate(t, "search", "Neubau", "--json")
	if code != 0 || strings.TrimSpace(stdout) != "[]" {
		t.Fatalf("search on an empty default store: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{legacy, "store format changed", "winner contacts require a fresh sync",
		tendersCLIName + " sync --since 90d --param country=DEU --param cpv=45", "--db " + legacy + " is not supported"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("hint misses %q:\n%s", want, stderr)
		}
	}
	if md5File(t, legacy) != before {
		t.Fatal("the legacy store file was modified")
	}

	// An explicit --db is the user's choice; the default-path hint stays out.
	other := filepath.Join(t.TempDir(), "other.db")
	_, stderr, _ = runTendersNoIsolate(t, "search", "Neubau", "--json", "--db", other)
	if strings.Contains(stderr, legacy) {
		t.Fatalf("explicit --db still got the legacy default hint: %s", stderr)
	}

	// Once the default store holds notices the hint goes away.
	st, err := store.OpenWithContext(t.Context(), defaultDBPath(tendersCLIName))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertNotices(t.Context(), []ted.Notice{awardNotice("9-2026", daysFromToday(-1), "Stadt A", "DEU", "45210000", 1)}, nil); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	_, stderr, code = runTendersNoIsolate(t, "search", "Neubau", "--json")
	if code != 0 || strings.Contains(stderr, legacy) {
		t.Fatalf("synced default store: exit %d, stderr %s", code, stderr)
	}
}

func TestLegacySchemaViaDBFlag(t *testing.T) {
	srv, _ := fakeTED(t)
	liveEnv(t, srv)
	db := filepath.Join(t.TempDir(), "legacy.db")
	writeLegacyTendersDB(t, db)
	before := md5File(t, db)

	stdout, stderr, code := runTendersNoIsolate(t, "search", "Neubau", "--db", db, "--json")
	if code != 0 || strings.TrimSpace(stdout) != "[]" {
		t.Fatalf("search on a legacy store: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "this store was created by an older version; run sync to rebuild it") {
		t.Fatalf("missing legacy-schema hint: %s", stderr)
	}
	if _, stderr, code := runTendersNoIsolate(t, "concentration", "--db", db, "--json"); code != 0 {
		t.Fatalf("analytics on a legacy store must not fail: exit %d %s", code, stderr)
	}
	if md5File(t, db) != before {
		t.Fatal("a read-only command modified the legacy store")
	}

	var res syncResult
	runTendersJSONNoIsolate(t, &res, "sync", "--since", "7d", "--db", db)
	if res.Synced != 300 {
		t.Fatalf("sync into a legacy store: %+v", res)
	}
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var oldRows int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM ` + store.LegacyNoticesTable).Scan(&oldRows); err != nil || oldRows != 1 {
		t.Fatalf("%s after sync: %d rows, %v", store.LegacyNoticesTable, oldRows, err)
	}
	stdout, stderr, code = runTendersNoIsolate(t, "search", "Neubau", "--db", db, "--json")
	if code != 0 || strings.Contains(stderr, "older version") {
		t.Fatalf("search after sync: exit %d stderr %s stdout %s", code, stderr, stdout)
	}
}

func TestMultiWinnerNoticeTotalIsNotCopiedToEachWinner(t *testing.T) {
	db := seedTendersDB(t, []ted.Notice{
		awardNotice("1-2026", daysFromToday(-2), "Stadt A", "DEU", "45210000", 900000,
			ted.Winner{Name: "A GmbH", Country: "DEU", LotsWon: 1},
			ted.Winner{Name: "B GmbH", Country: "DEU", LotsWon: 1},
			ted.Winner{Name: "C GmbH", Country: "DEU", LotsWon: 1}),
		awardNotice("2-2026", daysFromToday(-3), "Stadt A", "DEU", "45210000", 100000,
			ted.Winner{Name: "A GmbH", Country: "DEU", LotsWon: 1}),
	})
	var rows []leadRow
	runTendersJSON(t, &rows, "leads", "--country", "DEU", "--days", "30", "--db", db, "--data-source", "local")
	if len(rows) != 4 {
		t.Fatalf("want 4 lead rows, got %+v", rows)
	}
	for _, r := range rows {
		want := 0.0
		if r.NoticeID == "2-2026" {
			want = 100000
		}
		if r.ContractValue != want {
			t.Errorf("%s on %s: contract_value %v, want %v", r.WinnerName, r.NoticeID, r.ContractValue, want)
		}
	}
	var grouped []companyLead
	runTendersJSON(t, &grouped, "leads", "--country", "DEU", "--days", "30", "--group-by", "company", "--db", db, "--data-source", "local")
	sum := 0.0
	for _, c := range grouped {
		sum += c.TotalValue
		if c.WinnerName == "A GmbH" && c.TotalValue != 100000 {
			t.Errorf("A GmbH total %v, want 100000", c.TotalValue)
		}
	}
	if sum != 100000 {
		t.Fatalf("grouped totals sum to %v; the 3-winner notice total was counted per winner", sum)
	}

	// The live path counts winners from the TED payload.
	srv, _ := scriptedTED(t, func(ted.SearchRequest) []map[string]any {
		return []map[string]any{rawAward("5-2026", daysFromToday(-1), "Stadt A", "DEU", "45210000", 900000, "A GmbH", "B GmbH", "C GmbH")}
	})
	liveEnv(t, srv)
	var live []leadRow
	runTendersJSONNoIsolate(t, &live, "leads", "--country", "DEU", "--days", "30", "--data-source", "live", "--db", filepath.Join(t.TempDir(), "x.db"))
	if len(live) != 3 {
		t.Fatalf("live rows: %+v", live)
	}
	for _, r := range live {
		if r.ContractValue != 0 {
			t.Errorf("live %s: contract_value %v, want 0", r.WinnerName, r.ContractValue)
		}
	}
}

func TestAutoFallsBackToLiveWhenNoLocalRowsMatch(t *testing.T) {
	db := seedTendersDB(t, []ted.Notice{
		awardNotice("1-2026", daysFromToday(-2), "Ville A", "FRA", "45210000", 100000,
			ted.Winner{Name: "Construct SA", Country: "FRA", LotsWon: 1}),
		callNotice("2-2026", daysFromToday(-1), "Ville A", "FRA", "45210000", 50000, daysFromToday(10)),
	})
	srv, requests := scriptedTED(t, func(req ted.SearchRequest) []map[string]any {
		if strings.Contains(req.Query, "notice-type="+ted.NoticeTypeCall) {
			return []map[string]any{{
				"publication-number": "8-2026", "notice-type": ted.NoticeTypeCall, "publication-date": daysFromToday(-1),
				"buyer-name": "Stadt B", "buyer-country": "DEU", "classification-cpv": []any{"45210000"},
				"deadline-receipt-tender-date-lot": []any{daysFromToday(10) + "+02:00"}, "title-proc": "Rohbau",
			}}
		}
		return []map[string]any{rawAward("7-2026", daysFromToday(-1), "Stadt B", "DEU", "45210000", 100000, "Bau GmbH")}
	})
	liveEnv(t, srv)

	stdout, stderr, code := runTendersNoIsolate(t, "leads", "--country", "DEU", "--days", "30", "--data-source", "auto", "--db", db, "--agent")
	if code != 0 {
		t.Fatalf("leads auto: exit %d %s", code, stderr)
	}
	if len(requests()) == 0 {
		t.Fatal("auto mode with no local DEU matches did not query TED")
	}
	if !strings.Contains(stderr, "no local matches; querying TED live") {
		t.Errorf("missing fallback note: %s", stderr)
	}
	env := decodeAgentEnvelope(t, stdout)
	if env.Meta.Source != "live" || !strings.Contains(string(env.Results), "Bau GmbH") {
		t.Fatalf("auto fallback: source %q results %s", env.Meta.Source, env.Results)
	}

	n := len(requests())
	stdout, stderr, code = runTendersNoIsolate(t, "leads", "--country", "DEU", "--days", "30", "--data-source", "local", "--db", db, "--json")
	if code != 0 || strings.TrimSpace(stdout) != "[]" || len(requests()) != n {
		t.Fatalf("--data-source local: exit %d stdout %q, %d new TED requests (%s)", code, stdout, len(requests())-n, stderr)
	}

	stdout, stderr, code = runTendersNoIsolate(t, "deadline", "--country", "DEU", "--data-source", "auto", "--db", db, "--agent")
	if code != 0 || len(requests()) == n {
		t.Fatalf("deadline auto: exit %d, TED requests %d (%s)", code, len(requests())-n, stderr)
	}
	if env := decodeAgentEnvelope(t, stdout); env.Meta.Source != "live" || !strings.Contains(string(env.Results), "8-2026") {
		t.Fatalf("deadline auto fallback: source %q results %s", env.Meta.Source, env.Results)
	}
	n = len(requests())
	stdout, _, code = runTendersNoIsolate(t, "deadline", "--country", "DEU", "--data-source", "local", "--db", db, "--json")
	if code != 0 || strings.TrimSpace(stdout) != "[]" || len(requests()) != n {
		t.Fatalf("deadline local: exit %d stdout %q, %d new TED requests", code, stdout, len(requests())-n)
	}
}

func TestIncumbentsLiveKeepsTenderBeyondMaxScan(t *testing.T) {
	const tenderID = "100-2026"
	srv, requests := scriptedTED(t, func(req ted.SearchRequest) []map[string]any {
		if req.Query == ted.PublicationQuery(tenderID) {
			return []map[string]any{{
				"publication-number": tenderID, "notice-type": ted.NoticeTypeCall, "publication-date": daysFromToday(-1),
				"buyer-name": "Stadt A", "buyer-country": "DEU", "classification-cpv": []any{"45233120"},
				"deadline-receipt-tender-date-lot": []any{daysFromToday(20)}, "title-proc": "Straßenbau",
			}}
		}
		// The buyer has more prior awards than --max-scan; a capped scan
		// sorted by date never reaches the tender itself.
		out := make([]map[string]any, 0, 6)
		for i, w := range []string{"X GmbH", "X GmbH", "Y GmbH", "Z GmbH", "Z GmbH", "Z GmbH"} {
			out = append(out, rawAward(strconv.Itoa(200+i)+"-2026", daysFromToday(-10-i), "Stadt A", "DEU", "45233000", 1000, w))
		}
		return out
	})
	liveEnv(t, srv)
	var res incumbentsResult
	runTendersJSONNoIsolate(t, &res, "incumbents", tenderID, "--data-source", "live", "--max-scan", "2")
	if res.Tender.ID != tenderID || res.Tender.BuyerName != "Stadt A" {
		t.Fatalf("tender block lost: %+v", res)
	}
	if res.PriorAwards != 2 || len(res.Incumbents) != 1 || res.Incumbents[0].Name != "X GmbH" {
		t.Fatalf("prior awards from the capped scan: %+v", res)
	}
	reqs := requests()
	if len(reqs) != 2 || strings.Contains(reqs[1].Query, "publication-number") {
		t.Fatalf("the prior-award query must not search for the tender again: %+v", reqs)
	}
}

func TestNewOnlyNeverReturnsACompanyTwice(t *testing.T) {
	notices := []ted.Notice{}
	for i, name := range []string{"A GmbH", "B GmbH", "C GmbH", "D GmbH", "E GmbH", "F GmbH"} {
		notices = append(notices,
			awardNotice(strconv.Itoa(10+i)+"-2026", daysFromToday(-1-i), "Stadt A", "DEU", "45210000", 1, ted.Winner{Name: name, Country: "DEU", LotsWon: 1}),
			awardNotice(strconv.Itoa(30+i)+"-2026", daysFromToday(-10-i), "Stadt B", "DEU", "45210000", 1, ted.Winner{Name: name, Country: "DEU", LotsWon: 1}))
	}
	db := seedTendersDB(t, notices)
	testenv.Isolate(t)

	seen := map[string]bool{}
	for run, extra := range [][]string{{"--limit", "3"}, {"--limit", "2", "--group-by", "company"}} {
		args := append([]string{"leads", "--country", "DEU", "--days", "30", "--new-only", "--db", db, "--data-source", "local"}, extra...)
		var names []string
		if len(extra) == 4 {
			var grouped []companyLead
			runTendersJSONNoIsolate(t, &grouped, args...)
			for _, c := range grouped {
				names = append(names, c.WinnerName)
			}
		} else {
			var rows []leadRow
			runTendersJSONNoIsolate(t, &rows, args...)
			for _, r := range rows {
				names = append(names, r.WinnerName)
			}
		}
		if len(names) == 0 {
			t.Fatalf("run %d returned nothing", run)
		}
		runSeen := map[string]bool{}
		for _, n := range names {
			if seen[n] {
				t.Fatalf("run %d returned %s again", run, n)
			}
			runSeen[n] = true
		}
		for n := range runSeen {
			seen[n] = true
		}
	}

	// Concurrent runs on one store split the remaining companies.
	var mu sync.Mutex
	var wg sync.WaitGroup
	got := map[string]int{}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stdout, stderr, code := runTendersNoIsolate(t, "leads", "--country", "DEU", "--days", "30", "--new-only", "--db", db, "--data-source", "local", "--group-by", "company", "--json")
			if code != 0 {
				t.Errorf("concurrent run: exit %d %s", code, stderr)
				return
			}
			var grouped []companyLead
			if err := json.Unmarshal([]byte(stdout), &grouped); err != nil {
				t.Errorf("decode: %v %s", err, stdout)
				return
			}
			mu.Lock()
			for _, c := range grouped {
				got[c.WinnerName]++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	for name, n := range got {
		if n > 1 || seen[name] {
			t.Errorf("%s returned %d times by concurrent runs (seen before: %v)", name, n, seen[name])
		}
	}
	if len(got)+len(seen) != 6 {
		t.Errorf("companies returned overall: %d earlier + %d concurrent, want 6", len(seen), len(got))
	}
}

func TestClaimUpToRefillsSlotsLostToConcurrentRuns(t *testing.T) {
	testenv.Isolate(t)
	db := seedTendersDB(t, nil)
	st, err := store.OpenWithContext(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	// Another run claimed A and B after this run's seen-state pre-filter.
	if _, err := st.ClaimLeads(context.Background(), []store.LeadKey{leadStoreKey("A GmbH", "DEU"), leadStoreKey("B GmbH", "DEU")}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	rows := []leadRow{
		{WinnerName: "A GmbH", WinnerCountry: "DEU"},
		{WinnerName: "B GmbH", WinnerCountry: "DEU"},
		{WinnerName: "C GmbH", WinnerCountry: "DEU"},
		{WinnerName: "C GmbH", WinnerCountry: "DEU", NoticeID: "second-lot"},
		{WinnerName: "D GmbH", WinnerCountry: "DEU"},
	}
	cmd := RootCmd()
	cmd.SetContext(context.Background())
	got, lost, err := claimUpTo(cmd, db, rows, 2, func(l leadRow) store.LeadKey { return leadStoreKey(l.WinnerName, l.WinnerCountry) })
	if err != nil {
		t.Fatal(err)
	}
	if lost != 2 || len(got) != 2 || got[0].WinnerName != "C GmbH" || got[1].WinnerName != "C GmbH" {
		t.Fatalf("want both C rows after refilling past A and B, got lost=%d rows=%+v", lost, got)
	}
	got, _, err = claimUpTo(cmd, db, rows, 0, func(l leadRow) store.LeadKey { return leadStoreKey(l.WinnerName, l.WinnerCountry) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].WinnerName != "D GmbH" {
		t.Fatalf("second run should get only D, got %+v", got)
	}
}
