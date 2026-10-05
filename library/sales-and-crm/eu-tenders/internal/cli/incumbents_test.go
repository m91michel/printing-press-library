// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"bytes"
	"errors"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
	"strings"
	"testing"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
)

// TestNovelIncumbentsHelpWires smoke-tests that the incumbents command
// resolves at runtime and renders useful --help output. Catches wiring
// regressions (missing AddCommand, panicking RunE on --help, etc.) before
// review. Keep this smoke test when adding behavior-specific cases.
func TestNovelIncumbentsHelpWires(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"incumbents", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("incumbents --help error = %v (novel command not wired correctly?)", err)
	}
	help := out.String()
	for _, want := range []string{"Usage:", "incumbents"} {
		if !strings.Contains(help, want) {
			t.Fatalf("incumbents --help missing %q in output:\n%s", want, help)
		}
	}
}

func TestIncumbentsFromSameBuyerAndCategory(t *testing.T) {
	db := seedTendersDB(t, []ted.Notice{
		callNotice("100-2026", daysFromToday(-2), "Stadt A", "DEU", "45233120", 800000, daysFromToday(20)),
		awardNotice("101-2026", daysFromToday(-300), "Stadt A", "DEU", "45231000", 200000,
			ted.Winner{Name: "Firma X", Country: "DEU", Email: "x-firma@example.com", LotsWon: 1}),
		awardNotice("102-2026", daysFromToday(-100), "Stadt A", "DEU", "45232000", 300000,
			ted.Winner{Name: "Firma X", Country: "DEU", LotsWon: 1}),
		awardNotice("103-2026", daysFromToday(-50), "Stadt A", "DEU", "45230000", 100000,
			ted.Winner{Name: "Firma Y", Country: "DEU", LotsWon: 1}),
		awardNotice("104-2026", daysFromToday(-50), "Stadt B", "DEU", "45233000", 100000,
			ted.Winner{Name: "Firma Z", Country: "DEU", LotsWon: 1}),
		awardNotice("105-2026", daysFromToday(-50), "Stadt A", "DEU", "72000000", 100000,
			ted.Winner{Name: "IT W", Country: "DEU", LotsWon: 1}),
	})
	var got incumbentsResult
	runTendersJSON(t, &got, "incumbents", "100-2026", "--db", db, "--data-source", "local")
	if got.Tender.ID != "100-2026" || got.Tender.BuyerName != "Stadt A" || got.PriorAwards != 3 {
		t.Fatalf("tender/prior awards wrong: %+v", got)
	}
	if len(got.Incumbents) != 2 {
		t.Fatalf("want 2 incumbents (other buyer and other CPV excluded), got %+v", got.Incumbents)
	}
	x := got.Incumbents[0]
	if x.Name != "Firma X" || x.Wins != 2 || x.TotalValue != 500000 || x.LastWin != daysFromToday(-100) || x.Email != "x-firma@example.com" {
		t.Fatalf("top incumbent wrong: %+v", x)
	}
	if got.Incumbents[1].Name != "Firma Y" {
		t.Fatalf("second incumbent wrong: %+v", got.Incumbents[1])
	}

	var wide incumbentsResult
	runTendersJSON(t, &wide, "incumbents", "100-2026", "--cpv-digits", "2", "--db", db, "--data-source", "local")
	if wide.PriorAwards != 3 {
		t.Fatalf("--cpv-digits 2 should still exclude CPV 72 and other buyers: %+v", wide)
	}
}

func TestIncumbentsMissingNoticeIsNotFound(t *testing.T) {
	db := seedTendersDB(t, []ted.Notice{
		callNotice("100-2026", daysFromToday(-2), "Stadt A", "DEU", "45233120", 0, daysFromToday(20)),
	})
	testenv.Isolate(t)
	cmd := RootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"incumbents", "999-2026", "--db", db, "--data-source", "local", "--json"})
	err := cmd.Execute()
	var ce *cliError
	if !errors.As(err, &ce) || ce.code != 3 {
		t.Fatalf("want notFound exit 3, got %v", err)
	}
	cmd = RootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"incumbents", "not-a-number", "--db", db})
	if err := cmd.Execute(); !errors.As(err, &ce) || ce.code != 2 {
		t.Fatalf("want usage exit 2 for a malformed id, got %v", err)
	}
}
