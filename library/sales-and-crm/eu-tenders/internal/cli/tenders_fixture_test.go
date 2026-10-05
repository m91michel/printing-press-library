// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/store"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

// seedTendersDB writes notices into a fresh store and returns its path.
func seedTendersDB(t *testing.T, notices []ted.Notice) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tenders.db")
	st, err := store.OpenWithContext(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := range notices {
		if notices[i].NoticeURL == "" {
			notices[i].NoticeURL = ted.NoticeURL(notices[i].ID)
		}
		if notices[i].Currency == "" {
			notices[i].Currency = "EUR"
		}
	}
	if err := st.UpsertNotices(context.Background(), notices, nil); err != nil {
		t.Fatal(err)
	}
	return path
}

// daysFromToday returns a YYYY-MM-DD date offset from today.
func daysFromToday(n int) string {
	return time.Now().UTC().AddDate(0, 0, n).Format("2006-01-02")
}

// runTendersJSON executes the root command with args plus --json and decodes stdout into out.
func runTendersJSON(t *testing.T, out any, args ...string) {
	t.Helper()
	testenv.Isolate(t)
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

func awardNotice(id, date, buyer, country, cpv string, value float64, winners ...ted.Winner) ted.Notice {
	return ted.Notice{
		ID: id, NoticeType: ted.NoticeTypeAward, PublicationDate: date, BuyerName: buyer,
		BuyerCountry: country, CPVCode: cpv, CPVCodes: []string{cpv}, ContractValue: value,
		Title: "Award " + id, Winners: winners,
	}
}

func callNotice(id, date, buyer, country, cpv string, estimate float64, deadline string) ted.Notice {
	return ted.Notice{
		ID: id, NoticeType: ted.NoticeTypeCall, PublicationDate: date, BuyerName: buyer,
		BuyerCountry: country, CPVCode: cpv, CPVCodes: []string{cpv}, EstimatedValue: estimate,
		SubmissionDeadline: deadline, Title: "Call " + id,
	}
}

func TestLeadsGroupsAndFiltersFromStore(t *testing.T) {
	db := seedTendersDB(t, []ted.Notice{
		awardNotice("1-2026", daysFromToday(-2), "Stadt A", "DEU", "45210000", 100000,
			ted.Winner{Name: "Bau GmbH", Country: "DEU", Email: "a-bau@example.com", City: "Köln", LotsWon: 1, Value: 60000},
			ted.Winner{Name: "Elektro AG", Country: "DEU", Email: "e-el@example.com", LotsWon: 1, Value: 40000}),
		awardNotice("2-2026", daysFromToday(-3), "Stadt B", "DEU", "45230000", 500000,
			ted.Winner{Name: "Bau GmbH", Country: "DEU", Email: "a-bau@example.com", LotsWon: 1}),
		awardNotice("3-2026", daysFromToday(-3), "Ville C", "FRA", "45210000", 900000,
			ted.Winner{Name: "Construct SA", Country: "FRA", LotsWon: 1}),
		awardNotice("4-2026", daysFromToday(-3), "Stadt D", "DEU", "72000000", 900000,
			ted.Winner{Name: "IT GmbH", Country: "DEU", LotsWon: 1}),
	})
	var rows []leadRow
	runTendersJSON(t, &rows, "leads", "--country", "DEU", "--days", "30", "--db", db, "--data-source", "local")
	if len(rows) != 3 {
		t.Fatalf("want 3 DEU construction leads, got %d: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.WinnerName == "IT GmbH" || r.WinnerCountry == "FRA" {
			t.Errorf("filter leak: %+v", r)
		}
	}
	var grouped []companyLead
	runTendersJSON(t, &grouped, "leads", "--country", "DEU", "--days", "30", "--group-by", "company", "--db", db, "--data-source", "local")
	if len(grouped) != 2 || grouped[0].WinnerName != "Bau GmbH" || grouped[0].Wins != 2 || grouped[0].TotalValue != 560000 {
		t.Fatalf("grouping wrong: %+v", grouped)
	}
	var none []leadRow
	runTendersJSON(t, &none, "leads", "--country", "DEU", "--keywords", "Tunnel", "--days", "30", "--db", db, "--data-source", "local")
	if len(none) != 0 {
		t.Fatalf("keyword filter should return nothing, got %+v", none)
	}
	var first, second []leadRow
	runTendersJSON(t, &first, "leads", "--country", "DEU", "--days", "30", "--new-only", "--db", db, "--data-source", "local")
	runTendersJSON(t, &second, "leads", "--country", "DEU", "--days", "30", "--new-only", "--db", db, "--data-source", "local")
	if len(first) != 3 || len(second) != 0 {
		t.Fatalf("--new-only: first=%d second=%d", len(first), len(second))
	}
}

func TestLeadsNewOnlyMarksOnlyReturnedRows(t *testing.T) {
	db := seedTendersDB(t, []ted.Notice{
		awardNotice("1-2026", daysFromToday(-1), "Stadt A", "DEU", "45210000", 1, ted.Winner{Name: "A GmbH", Country: "DEU", LotsWon: 1}),
		awardNotice("2-2026", daysFromToday(-2), "Stadt A", "DEU", "45210000", 1, ted.Winner{Name: "B GmbH", Country: "DEU", LotsWon: 1}),
		awardNotice("3-2026", daysFromToday(-3), "Stadt A", "DEU", "45210000", 1, ted.Winner{Name: "C GmbH", Country: "DEU", LotsWon: 1}),
	})
	args := []string{"leads", "--country", "DEU", "--days", "30", "--limit", "1", "--new-only", "--db", db, "--data-source", "local"}
	seen := map[string]bool{}
	for run := 0; run < 3; run++ {
		var rows []leadRow
		runTendersJSON(t, &rows, args...)
		if len(rows) != 1 || seen[rows[0].WinnerName] {
			t.Fatalf("run %d: want one unseen lead, got %+v", run, rows)
		}
		seen[rows[0].WinnerName] = true
	}
	var rest []leadRow
	runTendersJSON(t, &rest, args...)
	if len(rest) != 0 {
		t.Fatalf("all leads returned once; want none left, got %+v", rest)
	}
}
