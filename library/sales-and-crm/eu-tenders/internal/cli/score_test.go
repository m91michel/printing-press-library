// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// cli-printing-press: novel-scaffold-test
// Novel command scaffold tests. Keep the wiring smoke test and add behavior cases as needed.

package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

// TestNovelScoreHelpWires smoke-tests that the score command
// resolves at runtime and renders useful --help output. Catches wiring
// regressions (missing AddCommand, panicking RunE on --help, etc.) before
// review. Keep this smoke test when adding behavior-specific cases.
func TestNovelScoreHelpWires(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"score", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("score --help error = %v (novel command not wired correctly?)", err)
	}
	help := out.String()
	for _, want := range []string{"Usage:", "score"} {
		if !strings.Contains(help, want) {
			t.Fatalf("score --help missing %q in output:\n%s", want, help)
		}
	}
}

func TestScoreRanksByUrgencyValueAndKeywords(t *testing.T) {
	a := callNotice("100-2026", daysFromToday(-3), "Stadt München", "DEU", "45210000", 100000, daysFromToday(5))
	a.Title = "Neubau Grundschule Nord"
	b := callNotice("101-2026", daysFromToday(-3), "Stadt Köln", "DEU", "45210000", 1000000, daysFromToday(30))
	b.Title = "Sanierung Rathaus"
	c := callNotice("102-2026", daysFromToday(-3), "Stadt Bonn", "DEU", "45210000", 500000, daysFromToday(50))
	c.Title = "NEUBAU Feuerwache"
	past := callNotice("103-2026", daysFromToday(-30), "Stadt Ulm", "DEU", "45210000", 900000, daysFromToday(-1))
	past.Title = "Neubau Brücke"
	far := callNotice("104-2026", daysFromToday(-3), "Stadt Ulm", "DEU", "45210000", 900000, daysFromToday(90))
	far.Title = "Neubau Halle"
	db := seedTendersDB(t, []ted.Notice{a, b, c, past, far})

	var rows []scoreRow
	runTendersJSON(t, &rows, "score", "--keywords", "Neubau", "--db", db, "--data-source", "local")
	if len(rows) != 3 {
		t.Fatalf("want 3 open calls within --max-days, got %d: %+v", len(rows), rows)
	}
	want := []string{"100-2026", "102-2026", "101-2026"}
	for i, id := range want {
		if rows[i].NoticeID != id || rows[i].Rank != i+1 {
			t.Fatalf("rank %d: want %s, got %+v", i+1, id, rows[i])
		}
	}
	for _, r := range rows {
		if r.NoticeID == "103-2026" || r.NoticeID == "104-2026" {
			t.Fatalf("past or out-of-window call leaked: %+v", r)
		}
	}
	if rows[0].KeywordPts != 30 || len(rows[0].MatchedKeywords) != 1 || rows[2].KeywordPts != 0 {
		t.Fatalf("keyword points wrong: %+v", rows)
	}
	if rows[2].ValuePts != 30 {
		t.Fatalf("largest value should get 30 value points, got %+v", rows[2])
	}

	var neutral []scoreRow
	runTendersJSON(t, &neutral, "score", "--country", "DEU", "--db", db, "--data-source", "local")
	if len(neutral) != 3 || neutral[0].KeywordPts != 15 || neutral[0].Note == "" {
		t.Fatalf("no-keyword run should give 15 pts and a note: %+v", neutral)
	}
}
