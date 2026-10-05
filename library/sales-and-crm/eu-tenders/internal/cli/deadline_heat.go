// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// pp:data-source auto

package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

type heatRow struct {
	Rank               int      `json:"rank"`
	Heat               float64  `json:"heat"`
	UrgencyNorm        float64  `json:"urgency"`
	ValueNorm          float64  `json:"value"`
	CompetitionNorm    float64  `json:"competition"`
	CompetitionSource  string   `json:"competition_source"`
	AvgWinners         *float64 `json:"avg_winners"`
	HistoryAwards      int      `json:"history_awards"`
	NoticeID           string   `json:"notice_id"`
	Title              string   `json:"title"`
	BuyerName          string   `json:"buyer_name"`
	BuyerCountry       string   `json:"buyer_country"`
	EstimatedValue     float64  `json:"estimated_value"`
	Currency           string   `json:"currency"`
	SubmissionDeadline string   `json:"submission_deadline"`
	DaysLeft           int      `json:"days_left"`
	CPVCode            string   `json:"cpv_code"`
	TEDURL             string   `json:"ted_url"`
}

type competitionSource string

const (
	competitionFromBuyer competitionSource = "buyer"
	competitionFromCPV   competitionSource = "cpv"
	competitionNone      competitionSource = "none"
)

type winnerStat struct {
	avg    float64
	awards int
}

// competitionHistory holds average distinct winners per award, keyed by
// buyer (country|exact name) and by CPV division (country|2-digit division).
type competitionHistory struct {
	byBuyer map[string]winnerStat
	byCPV   map[string]winnerStat
}

func buyerHistoryKey(country, buyer string) string {
	// Exact names on both sides: SQLite lower() folds ASCII only, so mixing it
	// with Go's Unicode ToLower would miss buyers such as "Universität ...".
	return strings.ToUpper(strings.TrimSpace(country)) + "|" + strings.TrimSpace(buyer)
}

func cpvHistoryKey(country, cpv string) string {
	return strings.ToUpper(strings.TrimSpace(country)) + "|" + ted.NormalizeCPV(cpv)[:2]
}

func (h competitionHistory) lookup(country, buyer, cpv string) (winnerStat, competitionSource) {
	if s, ok := h.byBuyer[buyerHistoryKey(country, buyer)]; ok {
		return s, competitionFromBuyer
	}
	if strings.TrimSpace(cpv) != "" {
		if s, ok := h.byCPV[cpvHistoryKey(country, cpv)]; ok {
			return s, competitionFromCPV
		}
	}
	return winnerStat{}, competitionNone
}

// loadCompetitionHistory reads award winner counts from the local store. A
// missing store yields empty history so live-only users still get a ranking.
func loadCompetitionHistory(ctx context.Context, dbPath, country string) (competitionHistory, error) {
	h := competitionHistory{byBuyer: map[string]winnerStat{}, byCPV: map[string]winnerStat{}}
	st, synced, err := openTendersIfSynced(ctx, resolveTendersDB(dbPath))
	if err != nil || !synced {
		return h, err
	}
	defer st.Close()
	f := &noticeFilterSQL{}
	f.add("n.notice_type = ?", ted.NoticeTypeAward)
	f.country("n.buyer_country", country)
	perAward := `SELECT n.buyer_country AS country, n.buyer_name AS buyer, substr(n.cpv_code, 1, 2) AS division,
		(SELECT COUNT(DISTINCT w.name_key) FROM notice_winners w WHERE w.notice_id = n.id) AS winners
		FROM notices n` + f.where()
	type agg struct {
		key    string
		avg    float64
		awards int
	}
	query := func(sqlText string, args []any) ([]agg, error) {
		rows, err := st.DB().QueryContext(ctx, sqlText, args...)
		if err != nil {
			return nil, fmt.Errorf("querying award history: %w", err)
		}
		out := make([]agg, 0)
		for rows.Next() {
			var a agg
			if err := rows.Scan(&a.key, &a.avg, &a.awards); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out = append(out, a)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		return out, rows.Close()
	}
	buyers, err := query(`SELECT upper(country) || '|' || trim(buyer), COALESCE(AVG(winners), 0), COUNT(*)
		FROM (`+perAward+`) WHERE winners > 0 AND buyer != '' GROUP BY 1`, f.args)
	if err != nil {
		return h, err
	}
	for _, a := range buyers {
		h.byBuyer[a.key] = winnerStat{avg: a.avg, awards: a.awards}
	}
	divisions, err := query(`SELECT upper(country) || '|' || division, COALESCE(AVG(winners), 0), COUNT(*)
		FROM (`+perAward+`) WHERE winners > 0 AND length(division) = 2 GROUP BY 1`, f.args)
	if err != nil {
		return h, err
	}
	for _, a := range divisions {
		h.byCPV[a.key] = winnerStat{avg: a.avg, awards: a.awards}
	}
	return h, nil
}

func newNovelDeadlineHeatCmd(flags *rootFlags) *cobra.Command {
	var country, cpv, dbPath string
	var days, limit, maxScan int
	var minValue float64

	cmd := &cobra.Command{
		Use:   "deadline-heat",
		Short: "Rank open tenders closing within N days by urgency, contract value and how few competitors usually bid.",
		Long: `Use this command to find open tenders closing within days where few competitors usually bid. Do NOT use it for keyword-fit ranking; use 'score' instead.

heat = 100 x (0.5 x urgency + 0.3 x value + 0.2 x competition), each in [0,1]:
  urgency      1 - days_left / --days
  value        log-scaled estimated value against the largest in the set
  competition  1 / (1 + avg_winners), where avg_winners is the average number
               of distinct winners per award for the same buyer, falling back
               to the same 2-digit CPV division in the same country; 0.5 when
               no award history exists (competition_source "none")

Open calls come from the local store when it holds calls for tender,
otherwise from TED live. Award history is read from the local store only
(run sync --type award to populate it).`,
		Example: strings.Trim(`
  eu-tenders-pp-cli deadline-heat --country DEU --cpv 45 --days 14 --agent
  eu-tenders-pp-cli deadline-heat --country FRA --cpv 72 --days 21 --min-value 200000 --json
  eu-tenders-pp-cli deadline-heat --country DEU --days 7 --limit 10`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "auto",
			"pp:happy-args":  "--country=DEU;--cpv=45;--days=14",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "rank closing tenders by deadline heat")
			}
			if days <= 0 {
				return usageErr(fmt.Errorf("--days must be positive"))
			}
			if err := validateTEDFilters(country, cpv); err != nil {
				return err
			}
			calls, err := loadOpenCalls(cmd, flags, country, cpv, days, dbPath, maxScan)
			if err != nil {
				return err
			}
			hist, err := loadCompetitionHistory(cmd.Context(), dbPath, country)
			if err != nil {
				return err
			}
			now := time.Now()
			kept, maxValue := rankableCalls(calls, now, days, minValue)
			out := make([]heatRow, 0, len(kept))
			withHistory := 0
			for _, c := range kept {
				left := daysUntil(c.SubmissionDeadline, now)
				stat, src := hist.lookup(c.BuyerCountry, c.BuyerName, c.CPVCode)
				hp := heatScore(left, days, c.EstimatedValue, maxValue, stat.avg, src != competitionNone)
				row := heatRow{
					Heat: hp.Heat, UrgencyNorm: hp.Urgency, ValueNorm: hp.Value, CompetitionNorm: hp.Competition,
					CompetitionSource: string(src), HistoryAwards: stat.awards, NoticeID: c.ID, Title: c.Title,
					BuyerName: c.BuyerName, BuyerCountry: c.BuyerCountry, EstimatedValue: round2(c.EstimatedValue),
					Currency: c.Currency, SubmissionDeadline: c.SubmissionDeadline, DaysLeft: left,
					CPVCode: c.CPVCode, TEDURL: c.NoticeURL,
				}
				if src != competitionNone {
					avg := round2(stat.avg)
					row.AvgWinners = &avg
					withHistory++
				}
				out = append(out, row)
			}
			sort.SliceStable(out, func(a, b int) bool {
				if out[a].Heat != out[b].Heat {
					return out[a].Heat > out[b].Heat
				}
				return out[a].DaysLeft < out[b].DaysLeft
			})
			if limit > 0 && len(out) > limit {
				out = out[:limit]
			}
			for i := range out {
				out[i].Rank = i + 1
			}
			if len(kept) > 0 && withHistory == 0 && !flags.quiet {
				fmt.Fprintf(cmd.ErrOrStderr(), "hint: no award history for these buyers; competition is neutral (0.5). Run %s sync --type award to add it\n", tendersCLIName)
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), out, flags)
			}
			if len(out) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No open tenders close in that window. Raise --days or run sync --type call.")
				return nil
			}
			tw := newTabWriter(cmd.OutOrStdout())
			fmt.Fprintln(tw, "RANK\tHEAT\tDAYS\tVALUE\tAVG WINNERS\tSOURCE\tBUYER\tTITLE")
			for _, r := range out {
				avg := "-"
				if r.AvgWinners != nil {
					avg = fmt.Sprintf("%.1f", *r.AvgWinners)
				}
				fmt.Fprintf(tw, "%d\t%.1f\t%d\t%.0f\t%s\t%s\t%s\t%s\n", r.Rank, r.Heat, r.DaysLeft, r.EstimatedValue,
					avg, r.CompetitionSource, truncate(r.BuyerName, 32), truncate(r.Title, 50))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&country, "country", "", "Buyer country, 3-letter ISO code (e.g. DEU)")
	cmd.Flags().StringVar(&cpv, "cpv", "", "CPV code or prefix (e.g. 45 for construction, 72 for IT services)")
	cmd.Flags().IntVar(&days, "days", 14, "Only calls whose submission deadline is within the next N days")
	cmd.Flags().Float64Var(&minValue, "min-value", 0, "Minimum estimated value in EUR (0 keeps calls without a reported value)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum rows to return")
	cmd.Flags().IntVar(&maxScan, "max-scan", 500, "Live mode: maximum calls for tender to read from TED")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}
