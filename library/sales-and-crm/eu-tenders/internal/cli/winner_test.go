// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
	"strings"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
)

// TestNovelWinnerHelpWires smoke-tests that the winner command
// resolves at runtime and renders useful --help output. Catches wiring
// regressions (missing AddCommand, panicking RunE on --help, etc.) before
// review. Keep this smoke test when adding behavior-specific cases.
func TestNovelWinnerHelpWires(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"winner", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("winner --help error = %v (novel command not wired correctly?)", err)
	}
	help := out.String()
	for _, want := range []string{"Usage:", "winner"} {
		if !strings.Contains(help, want) {
			t.Fatalf("winner --help missing %q in output:\n%s", want, help)
		}
	}
}

func TestWinnerProfilesKeepCountriesApart(t *testing.T) {
	db := seedTendersDB(t, []ted.Notice{
		awardNotice("20-2026", daysFromToday(-60), "Stadt München", "DEU", "45210000", 500000,
			ted.Winner{Name: "Johann Bunte Bau", Country: "DEU", Email: "old-bunte@example.com", Phone: "+49 1", City: "Papenburg", LotsWon: 1}),
		awardNotice("21-2026", daysFromToday(-5), "Stadt Köln", "DEU", "45230000", 900000,
			ted.Winner{Name: "Johann Bunte Bau", Country: "DEU", Email: "new-bunte@example.com", LotsWon: 2, Value: 300000},
			ted.Winner{Name: "Andere GmbH", Country: "DEU", LotsWon: 1, Value: 600000}),
		awardNotice("22-2026", daysFromToday(-3), "Stadt Wien", "AUT", "45210000", 100000,
			ted.Winner{Name: "Johann Bunte Bau", Country: "AUT", Email: "wien-bunte@example.com", LotsWon: 1}),
	})

	var got []winnerProfile
	runTendersJSON(t, &got, "winner", "bunte", "--db", db, "--data-source", "local")
	if len(got) != 2 {
		t.Fatalf("same name in DEU and AUT must stay two profiles, got %d: %+v", len(got), got)
	}
	de := got[0]
	if de.Country != "DEU" || de.Wins != 2 || de.LotsWon != 3 || de.TotalValue != 800000 {
		t.Fatalf("DEU profile wrong: %+v", de)
	}
	if de.Email != "new-bunte@example.com" || de.Phone != "+49 1" || de.City != "Papenburg" {
		t.Fatalf("contacts should come from the latest award with a value: %+v", de)
	}
	if de.FirstWin != daysFromToday(-60) || de.LastWin != daysFromToday(-5) {
		t.Fatalf("win dates wrong: %+v", de)
	}
	if len(de.Buyers) != 2 || len(de.RecentAwards) != 2 || de.RecentAwards[0].NoticeID != "21-2026" || de.RecentAwards[0].Value != 300000 {
		t.Fatalf("buyers/recent awards wrong: %+v", de)
	}
	if de.RecentAwards[0].TEDURL == "" || len(de.CPVMix) != 1 || de.CPVMix[0].CPV != "45000000" {
		t.Fatalf("links or cpv mix wrong: %+v", de)
	}
	if got[1].Country != "AUT" || got[1].Wins != 1 || got[1].Email != "wien-bunte@example.com" || got[1].TotalValue != 100000 {
		t.Fatalf("AUT profile wrong: %+v", got[1])
	}

	var deOnly []winnerProfile
	runTendersJSON(t, &deOnly, "winner", "Bunte", "--country", "aut", "--db", db, "--data-source", "local")
	if len(deOnly) != 1 || deOnly[0].Country != "AUT" {
		t.Fatalf("--country filter wrong: %+v", deOnly)
	}

	var none []winnerProfile
	runTendersJSON(t, &none, "winner", "Niemand", "--db", db, "--data-source", "local")
	if none == nil || len(none) != 0 {
		t.Fatalf("want empty [] for no match, got %+v", none)
	}
}
