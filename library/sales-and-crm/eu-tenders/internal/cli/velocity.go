// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// pp:data-source local

package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/store"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

type velocityWeek struct {
	WeekStart string `json:"week_start"`
	Calls     int    `json:"calls"`
	Awards    int    `json:"awards"`
}

type velocityCompare struct {
	Since           string   `json:"since"`
	Until           string   `json:"until"`
	TotalCalls      int      `json:"total_calls"`
	TotalAwards     int      `json:"total_awards"`
	ChangeCallsPct  *float64 `json:"change_calls_pct"`
	ChangeAwardsPct *float64 `json:"change_awards_pct"`
}

type velocityResult struct {
	Window      string           `json:"window"`
	Since       string           `json:"since"`
	Until       string           `json:"until"`
	Weeks       []velocityWeek   `json:"weeks"`
	TotalCalls  int              `json:"total_calls"`
	TotalAwards int              `json:"total_awards"`
	Compare     *velocityCompare `json:"compare"`
	Trend       velocityTrend    `json:"trend"`
}

type datedNotice struct {
	date       string
	noticeType string
}

// velocityNotices returns publication dates and types of calls and awards in [since, until].
func velocityNotices(ctx context.Context, st *store.Store, country, cpv, since, until string) ([]datedNotice, error) {
	f := &noticeFilterSQL{}
	f.add("notice_type IN (?, ?)", ted.NoticeTypeCall, ted.NoticeTypeAward)
	f.add("publication_date >= ?", since)
	f.add("publication_date <= ?", until)
	f.country("buyer_country", country)
	f.cpv("cpv_code", cpv)
	rows, err := st.DB().QueryContext(ctx, `SELECT publication_date, notice_type FROM notices`+f.where(), f.args...)
	if err != nil {
		return nil, fmt.Errorf("querying notice velocity: %w", err)
	}
	out := make([]datedNotice, 0)
	for rows.Next() {
		var d datedNotice
		if err := rows.Scan(&d.date, &d.noticeType); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	return out, rows.Close()
}

func countByType(ns []datedNotice) (calls, awards int) {
	for _, n := range ns {
		if n.noticeType == ted.NoticeTypeCall {
			calls++
		} else {
			awards++
		}
	}
	return calls, awards
}

// buildVelocity buckets notices into Monday-start weeks covering [since, today],
// including weeks with no notices, and labels the trend by comparing the
// second half of the window with the first half.
func buildVelocity(window string, since, today time.Time, ns []datedNotice) velocityResult {
	res := velocityResult{
		Window: window,
		Since:  since.Format("2006-01-02"),
		Until:  today.Format("2006-01-02"),
		Weeks:  make([]velocityWeek, 0),
	}
	idx := map[string]int{}
	for w := weekStart(since); !w.After(today); w = w.AddDate(0, 0, 7) {
		key := w.Format("2006-01-02")
		idx[key] = len(res.Weeks)
		res.Weeks = append(res.Weeks, velocityWeek{WeekStart: key})
	}
	mid := since.Add(today.Sub(since) / 2).Format("2006-01-02")
	var first, second float64
	for _, n := range ns {
		d, err := time.Parse("2006-01-02", n.date)
		if err != nil {
			continue
		}
		i, ok := idx[weekStart(d).Format("2006-01-02")]
		if !ok {
			continue
		}
		if n.noticeType == ted.NoticeTypeCall {
			res.Weeks[i].Calls++
			res.TotalCalls++
		} else {
			res.Weeks[i].Awards++
			res.TotalAwards++
		}
		if n.date < mid {
			first++
		} else {
			second++
		}
	}
	res.Trend = trendLabel(first, second)
	return res
}

func newNovelVelocityCmd(flags *rootFlags) *cobra.Command {
	var country, cpv, window, compare, dbPath string

	cmd := &cobra.Command{
		Use:   "velocity",
		Short: "See whether a procurement market is heating up or cooling off with weekly notice counts versus the same window last year",
		Long: `See whether a procurement market is heating up or cooling off: weekly counts of calls for tender and awards over a rolling window, optionally compared with the same window a year earlier.

Weeks start on Monday and every week in the window is listed, including
weeks with no notices. trend compares notices published in the second half
of the window with the first half: above +10% is "heating", below -10% is
"cooling", otherwise "flat"; "no_data" when the window holds no
notices at all. --compare 1y shifts the same window back one
year and reports totals plus percent change (null when the earlier window
had no notices). Reads the local store only; run sync first.

Output fields: window, since, until, weeks[{week_start, calls, awards}],
total_calls, total_awards, compare (or null), trend.`,
		Example: strings.Trim(`
  eu-tenders-pp-cli velocity --country DEU --cpv 45 --window 90d --compare 1y --agent
  eu-tenders-pp-cli velocity --country FRA --cpv 72 --window 12w --json
  eu-tenders-pp-cli velocity --country DEU --window 30d --human-friendly`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "local",
			"pp:happy-args":  "--country=DEU;--cpv=45;--window=30d",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "count weekly notice velocity")
			}
			if err := requireLocalSource(flags); err != nil {
				return err
			}
			if err := validateTEDFilters(country, cpv); err != nil {
				return err
			}
			win, err := parseWindow("window", window)
			if err != nil {
				return usageErr(err)
			}
			var shift time.Duration
			if strings.TrimSpace(compare) != "" {
				if shift, err = parseWindow("compare", compare); err != nil {
					return usageErr(err)
				}
			}
			dbPath = resolveTendersDB(dbPath)
			now := time.Now().UTC()
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
			since := today.Add(-win)
			st, stop, err := openLocalMirror(cmd, flags, dbPath, buildVelocity(window, since, today, nil))
			if stop {
				return err
			}
			defer st.Close()
			hintIfNoNotices(cmd, st, "")

			ns, err := velocityNotices(cmd.Context(), st, country, cpv, since.Format("2006-01-02"), today.Format("2006-01-02"))
			if err != nil {
				return err
			}
			res := buildVelocity(window, since, today, ns)
			if shift > 0 {
				cs, cu := since.Add(-shift).Format("2006-01-02"), today.Add(-shift).Format("2006-01-02")
				prev, err := velocityNotices(cmd.Context(), st, country, cpv, cs, cu)
				if err != nil {
					return err
				}
				pc, pa := countByType(prev)
				res.Compare = &velocityCompare{
					Since: cs, Until: cu, TotalCalls: pc, TotalAwards: pa,
					ChangeCallsPct:  pctChange(float64(res.TotalCalls), float64(pc)),
					ChangeAwardsPct: pctChange(float64(res.TotalAwards), float64(pa)),
				}
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), res, flags)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%s to %s (%s): %d calls, %d awards, trend %s\n", res.Since, res.Until, res.Window, res.TotalCalls, res.TotalAwards, res.Trend)
			if humanFriendly {
				totals := make([]int, len(res.Weeks))
				for i, wk := range res.Weeks {
					totals[i] = wk.Calls + wk.Awards
				}
				fmt.Fprintf(w, "weekly notices %s\n", sparkline(totals))
			}
			if res.Compare != nil {
				fmt.Fprintf(w, "compare %s to %s: %d calls (%s), %d awards (%s)\n", res.Compare.Since, res.Compare.Until,
					res.Compare.TotalCalls, fmtPct(res.Compare.ChangeCallsPct), res.Compare.TotalAwards, fmtPct(res.Compare.ChangeAwardsPct))
			}
			tw := newTabWriter(w)
			fmt.Fprintln(tw, "WEEK\tCALLS\tAWARDS")
			for _, wk := range res.Weeks {
				fmt.Fprintf(tw, "%s\t%d\t%d\n", wk.WeekStart, wk.Calls, wk.Awards)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&country, "country", "", "Buyer country, 3-letter ISO code (e.g. DEU)")
	cmd.Flags().StringVar(&cpv, "cpv", "", "CPV code or prefix (e.g. 45 for construction, 72 for IT services)")
	cmd.Flags().StringVar(&window, "window", "90d", "Rolling window ending today (e.g. 30d, 12w, 1y)")
	cmd.Flags().StringVar(&compare, "compare", "", "Also count the same window shifted back by this duration (e.g. 1y); empty skips the comparison")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}

func fmtPct(p *float64) string {
	if p == nil {
		return "n/a"
	}
	return fmt.Sprintf("%+.1f%%", *p)
}
