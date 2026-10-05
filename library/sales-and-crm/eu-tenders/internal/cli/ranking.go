// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

const (
	scoreUrgencyMax = 40.0
	scoreValueMax   = 30.0
	scoreKeywordMax = 30.0
	// scoreKeywordNeutral is the keyword share every call gets when no
	// keywords are given, so ranking falls back to urgency and value.
	scoreKeywordNeutral = scoreKeywordMax / 2

	heatUrgencyWeight     = 0.5
	heatValueWeight       = 0.3
	heatCompetitionWeight = 0.2
	// heatCompetitionNeutral is used when no award history exists.
	heatCompetitionNeutral = 0.5
)

func clamp01(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// urgencyShare returns 1 for a deadline today and 0 at the window edge.
func urgencyShare(daysLeft, windowDays int) float64 {
	if windowDays <= 0 {
		return 0
	}
	return clamp01(1 - float64(daysLeft)/float64(windowDays))
}

// valueShare scales a value logarithmically against the largest value in the
// set, so one outsized tender does not flatten every other value to zero.
func valueShare(value, maxValue float64) float64 {
	if value <= 0 || maxValue <= 0 {
		return 0
	}
	return clamp01(math.Log10(1+value) / math.Log10(1+maxValue))
}

// competitionShare is high when few companies usually win per award.
func competitionShare(avgWinners float64) float64 {
	if avgWinners < 0 {
		avgWinners = 0
	}
	return 1 / (1 + avgWinners)
}

// keywordMatches returns the keywords found in the title, case-insensitively.
func keywordMatches(title string, keywords []string) []string {
	out := make([]string, 0, len(keywords))
	lower := strings.ToLower(title)
	for _, k := range keywords {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" && strings.Contains(lower, k) {
			out = append(out, k)
		}
	}
	return out
}

type scoreParts struct {
	Urgency float64
	Value   float64
	Keyword float64
	Matched []string
	Total   float64
}

// scoreCall computes the 40/30/30 bid-fit score of one open call.
func scoreCall(daysLeft, maxDays int, value, maxValue float64, title string, keywords []string) scoreParts {
	p := scoreParts{
		Urgency: scoreUrgencyMax * urgencyShare(daysLeft, maxDays),
		Value:   scoreValueMax * valueShare(value, maxValue),
		Matched: keywordMatches(title, keywords),
	}
	if len(keywords) == 0 {
		p.Keyword = scoreKeywordNeutral
	} else {
		p.Keyword = scoreKeywordMax * float64(len(p.Matched)) / float64(len(keywords))
	}
	p.Total = round1(p.Urgency + p.Value + p.Keyword)
	p.Urgency, p.Value, p.Keyword = round1(p.Urgency), round1(p.Value), round1(p.Keyword)
	return p
}

type heatParts struct {
	Urgency     float64
	Value       float64
	Competition float64
	Heat        float64
}

// heatScore combines normalized urgency, value and competition into 0-100.
func heatScore(daysLeft, windowDays int, value, maxValue float64, avgWinners float64, hasHistory bool) heatParts {
	h := heatParts{
		Urgency:     urgencyShare(daysLeft, windowDays),
		Value:       valueShare(value, maxValue),
		Competition: heatCompetitionNeutral,
	}
	if hasHistory {
		h.Competition = competitionShare(avgWinners)
	}
	h.Heat = round1(100 * (heatUrgencyWeight*h.Urgency + heatValueWeight*h.Value + heatCompetitionWeight*h.Competition))
	h.Urgency, h.Value, h.Competition = round2(h.Urgency), round2(h.Value), round2(h.Competition)
	return h
}

// rankableCalls keeps open calls closing within windowDays whose estimated
// value reaches minValue (0 keeps unknown values), and returns the largest
// kept value for log scaling. A deadline today may already have passed, so
// ranking starts at one day left.
func rankableCalls(calls []ted.Notice, now time.Time, windowDays int, minValue float64) (kept []ted.Notice, maxValue float64) {
	kept = make([]ted.Notice, 0, len(calls))
	for _, c := range calls {
		if d := daysUntil(c.SubmissionDeadline, now); d < 1 || d > windowDays {
			continue
		}
		if minValue > 0 && c.EstimatedValue < minValue {
			continue
		}
		kept = append(kept, c)
		if c.EstimatedValue > maxValue {
			maxValue = c.EstimatedValue
		}
	}
	return kept, maxValue
}

// maxWindowYears bounds --window and --compare. Two centuries covers every
// TED notice, and keeps the nanosecond time.Duration far from overflow.
const maxWindowYears = 200

const maxWindow = maxWindowYears * 365 * 24 * time.Hour

// windowUnitDays maps the day-based suffixes parseWindow handles itself.
// time.ParseDuration has no unit ending in d, w or y.
var windowUnitDays = map[byte]int64{'d': 1, 'w': 7, 'y': 365, 'Y': 365}

// parseWindow accepts durations like 90d, 12w, 1y (365 days per year) or Go
// durations, up to maxWindowYears.
func parseWindow(flag, s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	invalid := fmt.Errorf("invalid --%s %q: use a duration like 90d, 12w or 1y", flag, s)
	tooLong := fmt.Errorf("invalid --%s %q: the window can be at most %d years", flag, s, maxWindowYears)
	if n := len(s); n >= 2 {
		if days, ok := windowUnitDays[s[n-1]]; ok {
			count, err := strconv.ParseInt(s[:n-1], 10, 64)
			if err != nil || count <= 0 {
				return 0, invalid
			}
			if count > maxWindowYears*365/days {
				return 0, tooLong
			}
			return time.Duration(count*days) * 24 * time.Hour, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, invalid
	}
	if d > maxWindow {
		return 0, tooLong
	}
	return d, nil
}

// weekStart returns the Monday (UTC midnight) of the ISO week holding t.
func weekStart(t time.Time) time.Time {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	offset := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -offset)
}

// pctChange returns the percent change from prev to cur, or nil when prev is 0.
func pctChange(cur, prev float64) *float64 {
	if prev == 0 {
		return nil
	}
	v := round2((cur - prev) / prev * 100)
	return &v
}

// velocityTrend labels a market's second-half vs first-half notice change.
type velocityTrend string

const (
	trendHeating velocityTrend = "heating"
	trendCooling velocityTrend = "cooling"
	trendFlat    velocityTrend = "flat"
	trendNoData  velocityTrend = "no_data"
)

// trendLabel classifies a second-half vs first-half change.
func trendLabel(first, second float64) velocityTrend {
	switch {
	case first == 0 && second == 0:
		return trendNoData
	case first == 0:
		return trendHeating
	}
	change := (second - first) / first * 100
	switch {
	case change > 10:
		return trendHeating
	case change < -10:
		return trendCooling
	}
	return trendFlat
}

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// sparkline renders values as block characters scaled to the maximum.
func sparkline(values []int) string {
	maxV := 0
	for _, v := range values {
		if v > maxV {
			maxV = v
		}
	}
	var b strings.Builder
	for _, v := range values {
		idx := 0
		if maxV > 0 {
			idx = int(math.Round(float64(v) / float64(maxV) * float64(len(sparkRunes)-1)))
		}
		b.WriteRune(sparkRunes[idx])
	}
	return b.String()
}
