// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// cli-printing-press: novel-scaffold-test
// Novel command scaffold tests. Keep the wiring smoke test and add behavior cases as needed.

package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil/testenv"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

// TestNovelCpvDriftHelpWires smoke-tests that the cpv-drift command
// resolves at runtime and renders useful --help output. Catches wiring
// regressions (missing AddCommand, panicking RunE on --help, etc.) before
// review. Keep this smoke test when adding behavior-specific cases.
func TestNovelCpvDriftHelpWires(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"cpv-drift", "--help"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cpv-drift --help error = %v (novel command not wired correctly?)", err)
	}
	help := out.String()
	for _, want := range []string{"Usage:", "cpv-drift"} {
		if !strings.Contains(help, want) {
			t.Fatalf("cpv-drift --help missing %q in output:\n%s", want, help)
		}
	}
}

func TestCpvDriftGrowthAndShare(t *testing.T) {
	y := time.Now().UTC().Year()
	prev := fmt.Sprintf("%d-03-01", y-1)
	cur := fmt.Sprintf("%d-01-15", y)
	var ns []ted.Notice
	add := func(n int, cpv, date string) {
		for i := 0; i < n; i++ {
			ns = append(ns, awardNotice(fmt.Sprintf("%s-%s-%d", cpv[:2], date, i), date, "Stadt A", "DEU", cpv, 1000))
		}
	}
	add(2, "45210000", prev)
	add(4, "45230000", cur)
	add(3, "72000000", prev)
	add(1, "72200000", cur)
	ns = append(ns, awardNotice("fr-1", cur, "Ville B", "FRA", "45210000", 1000))
	db := seedTendersDB(t, ns)

	var rows []cpvDriftRow
	runTendersJSON(t, &rows, "cpv-drift", "--country", "DEU", "--db", db, "--data-source", "local")
	if len(rows) != 2 || rows[0].CPV != "45000000" || rows[1].CPV != "72000000" {
		t.Fatalf("want divisions 45 then 72, got %+v", rows)
	}
	c, it := rows[0], rows[1]
	if c.Total != 6 || c.Latest != 4 || c.Previous != 2 || c.ChangePct == nil || *c.ChangePct != 100 {
		t.Fatalf("construction growth wrong: %+v", c)
	}
	if it.ChangePct == nil || *it.ChangePct >= 0 || it.ShareLatestPct != 20 || c.ShareLatestPct != 80 {
		t.Fatalf("IT shrink/share wrong: %+v / %+v", it, c)
	}
	if c.TotalsByYear[fmt.Sprint(y)] != 4 || c.LatestYear != fmt.Sprint(y) {
		t.Fatalf("totals_by_year wrong: %+v", c.TotalsByYear)
	}

	var value []cpvDriftRow
	runTendersJSON(t, &value, "cpv-drift", "--country", "DEU", "--metric", "value", "--cpv-digits", "3", "--db", db, "--data-source", "local")
	if len(value) != 3 || value[0].CPV != "45200000" || value[0].Total != 6000 {
		t.Fatalf("value metric / 3-digit grouping wrong: %+v", value)
	}
}

func TestCpvDriftRejectsInvalidMetric(t *testing.T) {
	testenv.Isolate(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"cpv-drift", "--metric", "volume", "--json"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	if ExitCode(err) != 2 {
		t.Fatalf("want usage exit 2, got %v (%d)", err, ExitCode(err))
	}
}
