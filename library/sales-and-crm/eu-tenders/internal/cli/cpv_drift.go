// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// pp:data-source local

package cli

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/store"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

type cpvDriftRow struct {
	CPV              string             `json:"cpv"`
	Description      string             `json:"description"`
	TotalsByYear     map[string]float64 `json:"totals_by_year"`
	Total            float64            `json:"total"`
	LatestYear       string             `json:"latest_year"`
	Latest           float64            `json:"latest"`
	Previous         float64            `json:"previous"`
	ChangePct        *float64           `json:"change_pct"`
	ShareLatestPct   float64            `json:"share_latest_pct"`
	SharePreviousPct float64            `json:"share_previous_pct"`
}

type cpvYearMetric struct {
	group string
	year  string
	value float64
}

type driftMetric string

const (
	driftMetricCount driftMetric = "count"
	driftMetricValue driftMetric = "value"
)

func cpvDriftMetrics(ctx context.Context, st *store.Store, country, since string, digits int, metric driftMetric) ([]cpvYearMetric, error) {
	agg := "COUNT(*)"
	if metric == driftMetricValue {
		agg = "COALESCE(SUM(CASE WHEN notice_type = ? THEN contract_value ELSE estimated_value END), 0)"
	}
	f := &noticeFilterSQL{}
	f.add("notice_type IN (?, ?)", ted.NoticeTypeCall, ted.NoticeTypeAward)
	f.add("length(cpv_code) >= ?", digits)
	f.add("length(publication_date) >= 4")
	f.country("buyer_country", country)
	f.since("publication_date", since)
	args := make([]any, 0, len(f.args)+2)
	args = append(args, digits)
	if metric == driftMetricValue {
		args = append(args, ted.NoticeTypeAward)
	}
	args = append(args, f.args...)
	rows, err := st.DB().QueryContext(ctx, `SELECT substr(cpv_code, 1, ?) AS grp, substr(publication_date, 1, 4) AS yr, `+agg+`
		FROM notices`+f.where()+` GROUP BY grp, yr`, args...)
	if err != nil {
		return nil, fmt.Errorf("querying CPV drift: %w", err)
	}
	out := make([]cpvYearMetric, 0)
	for rows.Next() {
		var m cpvYearMetric
		if err := rows.Scan(&m.group, &m.year, &m.value); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	return out, rows.Close()
}

// buildCPVDrift folds per-year metrics into per-group rows. latest_year is the
// newest year present across all groups so every row compares the same years;
// shares are computed against all groups, before --top truncation.
func buildCPVDrift(ms []cpvYearMetric, top int) []cpvDriftRow {
	latest := ""
	for _, m := range ms {
		if m.year > latest {
			latest = m.year
		}
	}
	previous := ""
	if y, err := strconv.Atoi(latest); err == nil {
		previous = strconv.Itoa(y - 1)
	}
	byGroup := map[string]*cpvDriftRow{}
	var sumLatest, sumPrevious float64
	for _, m := range ms {
		r, ok := byGroup[m.group]
		if !ok {
			code := ted.NormalizeCPV(m.group)
			r = &cpvDriftRow{CPV: code, Description: cpvDescription(code), TotalsByYear: map[string]float64{}, LatestYear: latest}
			byGroup[m.group] = r
		}
		r.TotalsByYear[m.year] += m.value
		r.Total += m.value
		switch m.year {
		case latest:
			r.Latest += m.value
			sumLatest += m.value
		case previous:
			r.Previous += m.value
			sumPrevious += m.value
		}
	}
	out := make([]cpvDriftRow, 0, len(byGroup))
	for _, r := range byGroup {
		r.ChangePct = pctChange(r.Latest, r.Previous)
		if sumLatest > 0 {
			r.ShareLatestPct = round2(r.Latest / sumLatest * 100)
		}
		if sumPrevious > 0 {
			r.SharePreviousPct = round2(r.Previous / sumPrevious * 100)
		}
		r.Total, r.Latest, r.Previous = round2(r.Total), round2(r.Latest), round2(r.Previous)
		for y, v := range r.TotalsByYear {
			r.TotalsByYear[y] = round2(v)
		}
		out = append(out, *r)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Total != out[b].Total {
			return out[a].Total > out[b].Total
		}
		return out[a].CPV < out[b].CPV
	})
	if top > 0 && len(out) > top {
		out = out[:top]
	}
	return out
}

func newNovelCpvDriftCmd(flags *rootFlags) *cobra.Command {
	var country, metric, since, dbPath string
	var top, digits int

	cmd := &cobra.Command{
		Use:   "cpv-drift",
		Short: "See which procurement categories grow or shrink year over year in a country's spending mix.",
		Long: `See which CPV categories are growing or shrinking in a country's procurement mix year over year.

Groups calls for tender and award notices by CPV prefix (--cpv-digits 2 =
division, e.g. 45000000) and publication year. --metric count counts notices;
--metric value sums contract_value for awards and estimated_value for calls.
change_pct compares the newest year in the data with the year before (null
when the earlier year is 0); the newest year is usually still in progress.
share_latest_pct is the group's share of all groups in the newest year.
Reads the local store only; run sync first.

Output fields: cpv, description, totals_by_year, total, latest_year, latest,
previous, change_pct, share_latest_pct, share_previous_pct.`,
		Example: strings.Trim(`
  eu-tenders-pp-cli cpv-drift --country FRA --metric value --top 20 --agent
  eu-tenders-pp-cli cpv-drift --country DEU --cpv-digits 3 --since 2023-01-01 --json
  eu-tenders-pp-cli cpv-drift --country DEU --metric count --top 10`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "local",
			"pp:happy-args":  "--country=DEU;--metric=count;--top=10",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "compare CPV mix year over year")
			}
			if err := requireLocalSource(flags); err != nil {
				return err
			}
			if err := validateTEDFilters(country, ""); err != nil {
				return err
			}
			m := driftMetric(strings.ToLower(strings.TrimSpace(metric)))
			if m != driftMetricCount && m != driftMetricValue {
				return usageErr(fmt.Errorf("invalid --metric %q: use count or value", metric))
			}
			if digits < 2 || digits > 8 {
				return usageErr(fmt.Errorf("--cpv-digits must be between 2 and 8"))
			}
			now := time.Now().UTC()
			sinceDate := fmt.Sprintf("%d-01-01", now.Year()-4)
			if strings.TrimSpace(since) != "" {
				d, err := resolveSinceDate(since, now)
				if err != nil {
					return usageErr(err)
				}
				sinceDate = d
			}
			dbPath = resolveTendersDB(dbPath)
			st, stop, err := openLocalMirror(cmd, flags, dbPath, make([]cpvDriftRow, 0))
			if stop {
				return err
			}
			defer st.Close()
			hintIfNoNotices(cmd, st, "")

			ms, err := cpvDriftMetrics(cmd.Context(), st, country, sinceDate, digits, m)
			if err != nil {
				return err
			}
			out := buildCPVDrift(ms, top)
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), out, flags)
			}
			if len(out) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No notices with CPV codes in that range. Widen --since or run sync.")
				return nil
			}
			tw := newTabWriter(cmd.OutOrStdout())
			fmt.Fprintf(tw, "CPV\tTOTAL\t%s\t%s\tCHANGE\tSHARE\tDESCRIPTION\n", out[0].LatestYear, "PREV")
			for _, r := range out {
				fmt.Fprintf(tw, "%s\t%.0f\t%.0f\t%.0f\t%s\t%.1f%%\t%s\n", r.CPV, r.Total, r.Latest, r.Previous,
					fmtPct(r.ChangePct), r.ShareLatestPct, truncate(r.Description, 50))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&country, "country", "", "Buyer country, 3-letter ISO code (e.g. DEU)")
	cmd.Flags().StringVar(&metric, "metric", "count", "What to compare: count (notices) or value (award contract value plus call estimated value)")
	cmd.Flags().StringVar(&since, "since", "", "Published since this date (YYYY-MM-DD) or duration (e.g. 730d); default January 1st four years ago")
	cmd.Flags().IntVar(&top, "top", 20, "Number of CPV groups to return, largest total first")
	cmd.Flags().IntVar(&digits, "cpv-digits", 2, "CPV prefix length to group by: 2 = division (45000000), 3 = group, 4 = class")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}
