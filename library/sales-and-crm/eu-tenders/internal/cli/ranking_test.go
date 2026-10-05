// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"testing"
	"time"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

func TestRankingScoreCall(t *testing.T) {
	cases := []struct {
		name                  string
		daysLeft, maxDays     int
		value, maxValue       float64
		title                 string
		keywords              []string
		urgency, val, kw, tot float64
	}{
		{"today, top value, all keywords", 0, 60, 1e6, 1e6, "Neubau Schule", []string{"neubau", "schule"}, 40, 30, 30, 100},
		{"edge of window, no value", 60, 60, 0, 1e6, "Neubau", []string{"neubau"}, 0, 0, 30, 30},
		{"half keywords", 30, 60, 1e6, 1e6, "Neubau Kita", []string{"neubau", "schule"}, 20, 30, 15, 65},
		{"no keywords is neutral", 30, 60, 0, 0, "Sanierung", nil, 20, 0, 15, 35},
		{"case-insensitive", 30, 60, 0, 0, "NEUBAU", []string{"neubau"}, 20, 0, 30, 50},
		{"past deadline clamps", -5, 60, 0, 0, "x", []string{"y"}, 40, 0, 0, 40},
		{"beyond window clamps", 90, 60, 0, 0, "x", []string{"y"}, 0, 0, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := scoreCall(c.daysLeft, c.maxDays, c.value, c.maxValue, c.title, c.keywords)
			if p.Urgency != c.urgency || p.Value != c.val || p.Keyword != c.kw || p.Total != c.tot {
				t.Fatalf("got %+v, want urgency=%v value=%v kw=%v total=%v", p, c.urgency, c.val, c.kw, c.tot)
			}
		})
	}
}

func TestRankingValueShareIsLogScaled(t *testing.T) {
	half := valueShare(1e3, 1e6)
	if half < 0.49 || half > 0.51 {
		t.Fatalf("log10 scaling: 1e3 vs 1e6 should be ~0.5, got %v", half)
	}
	if valueShare(-1, 1e6) != 0 || valueShare(5, 0) != 0 {
		t.Fatal("unknown values must score 0")
	}
}

func TestRankingHeatScore(t *testing.T) {
	cases := []struct {
		name       string
		left, days int
		v, maxV    float64
		avg        float64
		hist       bool
		heat, comp float64
	}{
		{"all max, solo winner", 0, 14, 1e6, 1e6, 0, true, 100, 1},
		{"one winner on average", 0, 14, 1e6, 1e6, 1, true, 90, 0.5},
		{"four winners", 0, 14, 1e6, 1e6, 4, true, 84, 0.2},
		{"no history neutral", 14, 14, 0, 0, 7, false, 10, 0.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := heatScore(c.left, c.days, c.v, c.maxV, c.avg, c.hist)
			if h.Heat != c.heat || h.Competition != c.comp {
				t.Fatalf("got %+v, want heat=%v competition=%v", h, c.heat, c.comp)
			}
		})
	}
}

func TestRankingParseWindow(t *testing.T) {
	cases := map[string]time.Duration{
		"90d": 90 * 24 * time.Hour,
		"2w":  14 * 24 * time.Hour,
		"1y":  365 * 24 * time.Hour,
		"48h": 48 * time.Hour,
	}
	for in, want := range cases {
		got, err := parseWindow("window", in)
		if err != nil || got != want {
			t.Fatalf("parseWindow(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "soon", "0d", "-1y", "xy"} {
		if _, err := parseWindow("window", bad); err == nil {
			t.Fatalf("parseWindow(%q) should fail", bad)
		}
	}
}

func TestRankingWeekStartAndTrend(t *testing.T) {
	// 2026-10-01 is a Thursday; its ISO week starts Monday 2026-09-28.
	thu := time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)
	if got := weekStart(thu).Format("2006-01-02"); got != "2026-09-28" {
		t.Fatalf("weekStart(Thu) = %s", got)
	}
	sun := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	if got := weekStart(sun).Format("2006-01-02"); got != "2026-09-28" {
		t.Fatalf("weekStart(Sun) = %s", got)
	}
	for _, c := range []struct {
		first, second float64
		want          velocityTrend
	}{{10, 12, "heating"}, {10, 8, "cooling"}, {10, 10.5, "flat"}, {0, 0, "no_data"}, {0, 3, "heating"}} {
		if got := trendLabel(c.first, c.second); got != c.want {
			t.Fatalf("trendLabel(%v,%v) = %s, want %s", c.first, c.second, got, c.want)
		}
	}
	if p := pctChange(5, 0); p != nil {
		t.Fatalf("pctChange with zero base should be nil, got %v", *p)
	}
	if p := pctChange(1, 3); p == nil || *p != -66.67 {
		t.Fatalf("pctChange(1,3) wrong: %v", p)
	}
	if s := sparkline([]int{0, 4, 8}); s != "▁▅█" {
		t.Fatalf("sparkline = %q", s)
	}
}

func TestRankableCallsWindowAndMinValue(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	day := func(n int) string { return now.AddDate(0, 0, n).Format("2006-01-02") }
	calls := []ted.Notice{
		{ID: "today", SubmissionDeadline: day(0), EstimatedValue: 9e9},
		{ID: "past", SubmissionDeadline: day(-3), EstimatedValue: 9e9},
		{ID: "in", SubmissionDeadline: day(5), EstimatedValue: 2000},
		{ID: "edge", SubmissionDeadline: day(10), EstimatedValue: 0},
		{ID: "late", SubmissionDeadline: day(11), EstimatedValue: 9e9},
		{ID: "cheap", SubmissionDeadline: day(3), EstimatedValue: 10},
	}
	kept, maxValue := rankableCalls(calls, now, 10, 0)
	if len(kept) != 3 || kept[0].ID != "in" || kept[1].ID != "edge" || kept[2].ID != "cheap" || maxValue != 2000 {
		t.Fatalf("kept %+v max %v", kept, maxValue)
	}
	kept, _ = rankableCalls(calls, now, 10, 100)
	if len(kept) != 1 || kept[0].ID != "in" {
		t.Fatalf("min-value 100 kept %+v", kept)
	}
	if calls[0].ID != "today" {
		t.Fatal("rankableCalls must not reorder its input")
	}
}

func TestCompanyKeyFoldsCaseSpaceAndCountry(t *testing.T) {
	if companyKey("  Bau   GmbH ", "deu") != companyKey("bau gmbh", "DEU") {
		t.Fatal("same company should share a key")
	}
	if companyKey("Bau GmbH", "DEU") == companyKey("Bau GmbH", "AUT") {
		t.Fatal("same name in another country is another company")
	}
}
