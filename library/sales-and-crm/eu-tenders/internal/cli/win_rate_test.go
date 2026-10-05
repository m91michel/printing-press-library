// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
	"strings"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
)

// TestNovelWinRateHelpWires smoke-tests that the win-rate command
// resolves at runtime and renders useful --help output. Catches wiring
// regressions (missing AddCommand, panicking RunE on --help, etc.) before
// review. Keep this smoke test when adding behavior-specific cases.
func TestNovelWinRateHelpWires(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"win-rate", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("win-rate --help error = %v (novel command not wired correctly?)", err)
	}
	help := out.String()
	for _, want := range []string{"Usage:", "win-rate"} {
		if !strings.Contains(help, want) {
			t.Fatalf("win-rate --help missing %q in output:\n%s", want, help)
		}
	}
}

func TestWinRatePerBuyer(t *testing.T) {
	a := "Stadt Alpha"
	db := seedTendersDB(t, []ted.Notice{
		callNotice("40-2026", daysFromToday(-40), a, "DEU", "45210000", 0, daysFromToday(1)),
		callNotice("41-2026", daysFromToday(-35), a, "DEU", "45210000", 0, daysFromToday(1)),
		callNotice("42-2026", daysFromToday(-30), a, "DEU", "45210000", 0, daysFromToday(1)),
		callNotice("43-2026", daysFromToday(-25), a, "DEU", "45210000", 0, daysFromToday(1)),
		awardNotice("44-2026", daysFromToday(-10), a, "DEU", "45210000", 1000,
			ted.Winner{Name: "Bau GmbH", Country: "DEU"}),
		awardNotice("45-2026", daysFromToday(-5), a, "DEU", "45210000", 1000,
			ted.Winner{Name: "Bau GmbH", Country: "AUT"}),
		callNotice("46-2026", daysFromToday(-5), "Stadt Beta", "DEU", "45210000", 0, daysFromToday(1)),
	})
	var got []buyerAwardStat
	runTendersJSON(t, &got, "win-rate", "--country", "DEU", "--show-winners", "--db", db)
	if len(got) != 1 {
		t.Fatalf("buyer with 1 call must be filtered by --min-calls 3, got %+v", got)
	}
	b := got[0]
	if b.BuyerName != a || b.Calls != 4 || b.Awards != 2 || b.AwardRate != 0.5 || b.UniqueWinners != 2 || b.WinnerDiversity != 1 {
		t.Fatalf("stats wrong (same name in two countries counts as two winners): %+v", b)
	}
	if len(b.TopWinners) != 2 {
		t.Fatalf("top winners wrong: %+v", b.TopWinners)
	}
	var all []buyerAwardStat
	runTendersJSON(t, &all, "win-rate", "--country", "DEU", "--min-calls", "1", "--db", db)
	if len(all) != 2 || all[0].Calls != 4 || all[1].BuyerName != "Stadt Beta" || all[0].TopWinners != nil {
		t.Fatalf("--min-calls 1 should list both, ordered by calls: %+v", all)
	}
}
