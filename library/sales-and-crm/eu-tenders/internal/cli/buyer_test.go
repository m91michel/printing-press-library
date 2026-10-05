// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
	"strings"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
)

// TestNovelBuyerHelpWires smoke-tests that the buyer command
// resolves at runtime and renders useful --help output. Catches wiring
// regressions (missing AddCommand, panicking RunE on --help, etc.) before
// review. Keep this smoke test when adding behavior-specific cases.
func TestNovelBuyerHelpWires(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"buyer", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("buyer --help error = %v (novel command not wired correctly?)", err)
	}
	help := out.String()
	for _, want := range []string{"Usage:", "buyer"} {
		if !strings.Contains(help, want) {
			t.Fatalf("buyer --help missing %q in output:\n%s", want, help)
		}
	}
}

func TestBuyerProfilesFromStore(t *testing.T) {
	alpha, beta := "Stadt Alpha", "Stadt Beta"
	db := seedTendersDB(t, []ted.Notice{
		callNotice("10-2026", daysFromToday(-40), alpha, "DEU", "45210000", 100000, daysFromToday(10)),
		callNotice("11-2026", daysFromToday(-30), alpha, "DEU", "45230000", 300000, daysFromToday(20)),
		awardNotice("12-2026", daysFromToday(-20), alpha, "DEU", "45210000", 200000,
			ted.Winner{Name: "Bau GmbH", Country: "DEU", LotsWon: 1}),
		awardNotice("13-2026", daysFromToday(-10), alpha, "DEU", "45230000", 400000,
			ted.Winner{Name: "Bau  GMBH", Country: "DEU", LotsWon: 1}),
		awardNotice("14-2026", daysFromToday(-5), alpha, "DEU", "72000000", 100000,
			ted.Winner{Name: "Tief AG", Country: "DEU", LotsWon: 1}),
		callNotice("15-2026", daysFromToday(-5), beta, "DEU", "45210000", 50000, daysFromToday(30)),
	})

	var got []buyerProfile
	runTendersJSON(t, &got, "buyer", "--name", "ALPHA", "--show-winners", "--db", db)
	if len(got) != 1 {
		t.Fatalf("want 1 profile, got %d: %+v", len(got), got)
	}
	p := got[0]
	if p.BuyerName != alpha || p.Calls != 2 || p.Awards != 3 {
		t.Fatalf("counts wrong: %+v", p)
	}
	if p.TotalAwardedValue != 700000 || p.MedianAwardValue != 200000 || p.AvgEstimatedValue != 200000 {
		t.Fatalf("values wrong: total=%v median=%v avgEst=%v", p.TotalAwardedValue, p.MedianAwardValue, p.AvgEstimatedValue)
	}
	if len(p.TopCPV) != 2 || p.TopCPV[0].CPV != "45000000" || p.TopCPV[0].Count != 4 {
		t.Fatalf("top_cpv wrong: %+v", p.TopCPV)
	}
	if len(p.TopWinners) != 2 || p.TopWinners[0].Wins != 2 || p.TopWinners[0].TotalValue != 600000 || p.TopWinners[0].Country != "DEU" {
		t.Fatalf("top winners wrong: %+v", p.TopWinners)
	}
	if p.FirstNotice != daysFromToday(-40) || p.LastNotice != daysFromToday(-5) || p.NoticesPerMonth <= 0 {
		t.Fatalf("timeline wrong: %+v", p)
	}

	var both []buyerProfile
	runTendersJSON(t, &both, "buyer", "--name", "stadt", "--db", db)
	if len(both) != 2 || both[0].BuyerName != alpha || both[1].BuyerName != beta || both[0].TopWinners != nil {
		t.Fatalf("partial match wrong: %+v", both)
	}

	var none []buyerProfile
	runTendersJSON(t, &none, "buyer", "--name", "Gemeinde Nirgendwo", "--db", db)
	if none == nil || len(none) != 0 {
		t.Fatalf("want empty [] for no match, got %+v", none)
	}
}

func TestBuyerRequiresName(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"buyer", "--show-winners", "--json"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--name is required") {
		t.Fatalf("want --name usage error, got %v", err)
	}
}
