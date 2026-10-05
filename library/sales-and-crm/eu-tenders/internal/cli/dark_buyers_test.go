// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"fmt"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
	"strings"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
)

// TestNovelDarkBuyersHelpWires smoke-tests that the dark-buyers command
// resolves at runtime and renders useful --help output. Catches wiring
// regressions (missing AddCommand, panicking RunE on --help, etc.) before
// review. Keep this smoke test when adding behavior-specific cases.
func TestNovelDarkBuyersHelpWires(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"dark-buyers", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("dark-buyers --help error = %v (novel command not wired correctly?)", err)
	}
	help := out.String()
	for _, want := range []string{"Usage:", "dark-buyers"} {
		if !strings.Contains(help, want) {
			t.Fatalf("dark-buyers --help missing %q in output:\n%s", want, help)
		}
	}
}

func TestDarkBuyersFlags(t *testing.T) {
	var ns []ted.Notice
	for i := 0; i < 5; i++ {
		ns = append(ns, callNotice(fmt.Sprintf("5%d-2026", i), daysFromToday(-30), "Stille Gemeinde", "DEU", "45210000", 0, daysFromToday(5)))
	}
	for i := 0; i < 3; i++ {
		ns = append(ns,
			callNotice(fmt.Sprintf("6%d-2026", i), daysFromToday(-30), "Treue Stadt", "DEU", "45210000", 0, daysFromToday(5)),
			awardNotice(fmt.Sprintf("7%d-2026", i), daysFromToday(-10), "Treue Stadt", "DEU", "45210000", 1000,
				ted.Winner{Name: "Haus Bau GmbH", Country: "DEU"}),
			callNotice(fmt.Sprintf("8%d-2026", i), daysFromToday(-30), "Offene Stadt", "DEU", "45210000", 0, daysFromToday(5)),
			awardNotice(fmt.Sprintf("9%d-2026", i), daysFromToday(-10), "Offene Stadt", "DEU", "45210000", 1000,
				ted.Winner{Name: fmt.Sprintf("Firma %d", i), Country: "DEU"}),
		)
	}
	db := seedTendersDB(t, ns)
	var got []darkBuyer
	runTendersJSON(t, &got, "dark-buyers", "--country", "DEU", "--db", db)
	if len(got) != 2 {
		t.Fatalf("want 2 flagged buyers (healthy one excluded), got %+v", got)
	}
	byName := map[string]darkBuyer{}
	for _, d := range got {
		byName[d.BuyerName] = d
		if d.Note != darkBuyerNote {
			t.Errorf("missing heuristic note: %+v", d)
		}
	}
	quiet := byName["Stille Gemeinde"]
	if quiet.Calls != 5 || quiet.Awards != 0 || len(quiet.Reasons) != 1 || quiet.Reasons[0] != darkReasonLowAwardRate {
		t.Fatalf("low award rate buyer wrong: %+v", quiet)
	}
	loyal := byName["Treue Stadt"]
	if loyal.Awards != 3 || loyal.UniqueWinners != 1 || loyal.TopWinner != "Haus Bau GmbH" || len(loyal.Reasons) != 1 || loyal.Reasons[0] != darkReasonSingleWinner {
		t.Fatalf("single winner buyer wrong: %+v", loyal)
	}
	if _, ok := byName["Offene Stadt"]; ok {
		t.Fatalf("healthy buyer flagged: %+v", got)
	}
	var none []darkBuyer
	runTendersJSON(t, &none, "dark-buyers", "--country", "POL", "--db", db)
	if none == nil || len(none) != 0 {
		t.Fatalf("want [] for empty slice, got %+v", none)
	}
}
