// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// cli-printing-press: novel-scaffold-test
// Novel command scaffold tests. Keep the wiring smoke test and add behavior cases as needed.

package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

// TestNovelVelocityHelpWires smoke-tests that the velocity command
// resolves at runtime and renders useful --help output. Catches wiring
// regressions (missing AddCommand, panicking RunE on --help, etc.) before
// review. Keep this smoke test when adding behavior-specific cases.
func TestNovelVelocityHelpWires(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"velocity", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("velocity --help error = %v (novel command not wired correctly?)", err)
	}
	help := out.String()
	for _, want := range []string{"Usage:", "velocity"} {
		if !strings.Contains(help, want) {
			t.Fatalf("velocity --help missing %q in output:\n%s", want, help)
		}
	}
}

func TestVelocityWeeklyBucketsIncludeEmptyWeeks(t *testing.T) {
	calls := []ted.Notice{
		callNotice("400-2026", daysFromToday(-1), "Stadt A", "DEU", "45210000", 0, daysFromToday(20)),
		callNotice("401-2026", daysFromToday(-1), "Stadt A", "DEU", "45210000", 0, daysFromToday(20)),
		awardNotice("402-2026", daysFromToday(-20), "Stadt A", "DEU", "45210000", 1000),
		callNotice("403-2026", daysFromToday(-367), "Stadt A", "DEU", "45210000", 0, daysFromToday(-340)),
		callNotice("404-2026", daysFromToday(-1), "Ville B", "FRA", "45210000", 0, daysFromToday(20)),
	}
	db := seedTendersDB(t, calls)

	var res velocityResult
	runTendersJSON(t, &res, "velocity", "--country", "DEU", "--window", "28d", "--compare", "1y", "--db", db, "--data-source", "local")
	today := time.Now().UTC()
	since := today.AddDate(0, 0, -28)
	wantWeeks := 0
	for w := weekStart(since); !w.After(weekStart(today)); w = w.AddDate(0, 0, 7) {
		wantWeeks++
	}
	if len(res.Weeks) != wantWeeks {
		t.Fatalf("want %d weeks, got %d: %+v", wantWeeks, len(res.Weeks), res.Weeks)
	}
	empty := 0
	for _, w := range res.Weeks {
		if w.Calls == 0 && w.Awards == 0 {
			empty++
		}
	}
	if empty < wantWeeks-2 {
		t.Fatalf("zero-count weeks missing: %+v", res.Weeks)
	}
	if res.TotalCalls != 2 || res.TotalAwards != 1 {
		t.Fatalf("totals wrong: calls=%d awards=%d", res.TotalCalls, res.TotalAwards)
	}
	if res.Trend != "heating" {
		t.Fatalf("2 recent vs 1 early notice should be heating, got %s", res.Trend)
	}
	if res.Compare == nil || res.Compare.TotalCalls != 1 || res.Compare.TotalAwards != 0 {
		t.Fatalf("compare totals wrong: %+v", res.Compare)
	}
	if res.Compare.ChangeCallsPct == nil || *res.Compare.ChangeCallsPct != 100 || res.Compare.ChangeAwardsPct != nil {
		t.Fatalf("compare change wrong: %+v", res.Compare)
	}

	var plain map[string]any
	runTendersJSON(t, &plain, "velocity", "--country", "DEU", "--window", "28d", "--db", db, "--data-source", "local")
	if v, ok := plain["compare"]; !ok || v != nil {
		t.Fatalf("compare should be null without --compare: %v", plain["compare"])
	}
}

func TestVelocityRejectsBadWindow(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"velocity", "--window", "soon", "--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	if ExitCode(err) != 2 {
		t.Fatalf("want usage exit 2, got %v (%d)", err, ExitCode(err))
	}
}
