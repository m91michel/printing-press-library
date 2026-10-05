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

// TestNovelDeadlineHeatHelpWires smoke-tests that the deadline-heat command
// resolves at runtime and renders useful --help output. Catches wiring
// regressions (missing AddCommand, panicking RunE on --help, etc.) before
// review. Keep this smoke test when adding behavior-specific cases.
func TestNovelDeadlineHeatHelpWires(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"deadline-heat", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("deadline-heat --help error = %v (novel command not wired correctly?)", err)
	}
	help := out.String()
	for _, want := range []string{"Usage:", "deadline-heat"} {
		if !strings.Contains(help, want) {
			t.Fatalf("deadline-heat --help missing %q in output:\n%s", want, help)
		}
	}
}

func TestDeadlineHeatPrefersLowCompetitionBuyer(t *testing.T) {
	notices := []ted.Notice{
		callNotice("200-2026", daysFromToday(-2), "Stadt Crowded", "DEU", "45210000", 400000, daysFromToday(7)),
		callNotice("201-2026", daysFromToday(-2), "Stadt Quiet", "DEU", "45210000", 400000, daysFromToday(7)),
		callNotice("202-2026", daysFromToday(-2), "Stadt Unknown", "FRA", "72000000", 400000, daysFromToday(7)),
	}
	many := func(id string) ted.Notice {
		return awardNotice(id, daysFromToday(-60), "Stadt Crowded", "DEU", "45210000", 100000,
			ted.Winner{Name: "A GmbH", LotsWon: 1}, ted.Winner{Name: "B GmbH", LotsWon: 1},
			ted.Winner{Name: "C GmbH", LotsWon: 1}, ted.Winner{Name: "D GmbH", LotsWon: 1})
	}
	one := func(id string) ted.Notice {
		return awardNotice(id, daysFromToday(-60), "Stadt Quiet", "DEU", "45210000", 100000, ted.Winner{Name: "Solo GmbH", LotsWon: 1})
	}
	notices = append(notices, many("300-2026"), many("301-2026"), one("302-2026"), one("303-2026"))
	db := seedTendersDB(t, notices)

	var rows []heatRow
	runTendersJSON(t, &rows, "deadline-heat", "--days", "14", "--db", db, "--data-source", "local")
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %+v", rows)
	}
	byID := map[string]heatRow{}
	for _, r := range rows {
		byID[r.NoticeID] = r
	}
	quiet, crowded, unknown := byID["201-2026"], byID["200-2026"], byID["202-2026"]
	if rows[0].NoticeID != "201-2026" {
		t.Fatalf("low-competition buyer should rank first: %+v", rows)
	}
	if quiet.CompetitionSource != "buyer" || quiet.AvgWinners == nil || *quiet.AvgWinners != 1 || quiet.CompetitionNorm != 0.5 {
		t.Fatalf("quiet buyer history wrong: %+v", quiet)
	}
	if crowded.AvgWinners == nil || *crowded.AvgWinners != 4 || crowded.CompetitionNorm != 0.2 || crowded.Heat >= quiet.Heat {
		t.Fatalf("crowded buyer history wrong: %+v", crowded)
	}
	if unknown.CompetitionSource != "none" || unknown.AvgWinners != nil || unknown.CompetitionNorm != 0.5 {
		t.Fatalf("no-history call should be neutral: %+v", unknown)
	}

	var fallback []heatRow
	db2 := seedTendersDB(t, []ted.Notice{
		callNotice("210-2026", daysFromToday(-2), "Stadt Neu", "DEU", "45230000", 400000, daysFromToday(3)),
		many("310-2026"),
	})
	runTendersJSON(t, &fallback, "deadline-heat", "--days", "14", "--db", db2, "--data-source", "local")
	if len(fallback) != 1 || fallback[0].CompetitionSource != "cpv" || *fallback[0].AvgWinners != 4 {
		t.Fatalf("CPV-division fallback wrong: %+v", fallback)
	}
}
