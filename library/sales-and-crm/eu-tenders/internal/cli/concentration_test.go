// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
	"math"
	"strings"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
)

// TestNovelConcentrationHelpWires smoke-tests that the concentration command
// resolves at runtime and renders useful --help output. Catches wiring
// regressions (missing AddCommand, panicking RunE on --help, etc.) before
// review. Keep this smoke test when adding behavior-specific cases.
func TestNovelConcentrationHelpWires(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"concentration", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("concentration --help error = %v (novel command not wired correctly?)", err)
	}
	help := out.String()
	for _, want := range []string{"Usage:", "concentration"} {
		if !strings.Contains(help, want) {
			t.Fatalf("concentration --help missing %q in output:\n%s", want, help)
		}
	}
}

func TestConcentrationHHIAndShares(t *testing.T) {
	db := seedTendersDB(t, []ted.Notice{
		awardNotice("30-2026", daysFromToday(-10), "Stadt A", "DEU", "45210000", 50,
			ted.Winner{Name: "Alpha Bau", Country: "DEU", LotsWon: 1}),
		awardNotice("31-2026", daysFromToday(-20), "Stadt B", "DEU", "45230000", 30,
			ted.Winner{Name: "Beta Bau", Country: "DEU", LotsWon: 1}),
		awardNotice("32-2026", daysFromToday(-30), "Stadt C", "DEU", "45230000", 100,
			ted.Winner{Name: "Gamma Bau", Country: "DEU", LotsWon: 1, Value: 20},
			ted.Winner{Name: "Delta Bau", Country: "DEU", LotsWon: 1}),
		awardNotice("33-2026", daysFromToday(-30), "Ville D", "FRA", "45230000", 999,
			ted.Winner{Name: "Alpha Bau", Country: "FRA", LotsWon: 1}),
	})
	var got concentrationResult
	runTendersJSON(t, &got, "concentration", "--country", "DEU", "--cpv", "45", "--db", db)
	if got.Awards != 3 || got.WinnersTotal != 4 || got.MarketTotalValue != 100 {
		t.Fatalf("slice totals wrong: %+v", got)
	}
	if math.Abs(got.HHI-3800) > 0.01 || got.Band != hhiBandHigh {
		t.Fatalf("want HHI 3800 highly concentrated, got %v %q", got.HHI, got.Band)
	}
	want := []struct {
		name  string
		share float64
	}{{"Alpha Bau", 50}, {"Beta Bau", 30}, {"Gamma Bau", 20}}
	if len(got.Top) != 3 {
		t.Fatalf("zero-value winner must not appear in top: %+v", got.Top)
	}
	for i, w := range want {
		if got.Top[i].Name != w.name || got.Top[i].SharePct != w.share || got.Top[i].Rank != i+1 || got.Top[i].Country != "DEU" {
			t.Fatalf("top[%d] = %+v, want %s %.0f%%", i, got.Top[i], w.name, w.share)
		}
	}
	if got.HHILast12m == nil || math.Abs(*got.HHILast12m-3800) > 0.01 || got.HHIPrior12m != nil {
		t.Fatalf("yoy wrong: last=%v prior=%v", got.HHILast12m, got.HHIPrior12m)
	}

	var empty concentrationResult
	runTendersJSON(t, &empty, "concentration", "--country", "POL", "--db", db)
	if empty.HHI != 0 || empty.Top == nil || len(empty.Top) != 0 || empty.Awards != 0 {
		t.Fatalf("empty slice should give hhi 0 and top []: %+v", empty)
	}
}

func TestHHIBands(t *testing.T) {
	for _, tc := range []struct {
		hhi, total float64
		want       string
	}{{1000, 1, hhiBandCompetitive}, {2000, 1, hhiBandModerate}, {2500, 1, hhiBandModerate}, {2600, 1, hhiBandHigh}, {0, 0, hhiBandNoData}} {
		if got := hhiBand(tc.hhi, tc.total); got != tc.want {
			t.Errorf("hhiBand(%v) = %q, want %q", tc.hhi, got, tc.want)
		}
	}
}
