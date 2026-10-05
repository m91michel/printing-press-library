// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// pp:data-source auto

package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type scoreRow struct {
	Rank               int      `json:"rank"`
	Score              float64  `json:"score"`
	UrgencyPts         float64  `json:"urgency_pts"`
	ValuePts           float64  `json:"value_pts"`
	KeywordPts         float64  `json:"keyword_pts"`
	MatchedKeywords    []string `json:"matched_keywords"`
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
	Note               string   `json:"note,omitempty"`
}

const scoreNoKeywordsNote = "no --keywords given: every call gets 15 of 30 keyword points"

func newNovelScoreCmd(flags *rootFlags) *cobra.Command {
	var country, cpv, keywords, dbPath string
	var limit, maxDays, maxScan int
	var minValue float64

	cmd := &cobra.Command{
		Use:   "score",
		Short: "Rank open tenders by deadline urgency, contract value and keyword fit into a prioritized bid shortlist.",
		Long: `Use this command to rank open tenders by fit to your keywords plus value and urgency. Do NOT use it to find tenders closing soon in under-competed markets; use 'deadline-heat' instead.

Each open call for tender whose deadline falls within --max-days gets up to
100 points:
  urgency  40 pts  40 x (1 - days_left / max_days)
  value    30 pts  log-scaled estimated value against the largest in the set (0 when unknown)
  keywords 30 pts  share of --keywords found in the title (15 pts each when no keywords given)

Reads the local store when it holds calls for tender (run sync first),
otherwise queries TED live (--data-source live forces the API).

Output fields: rank, score, urgency_pts, value_pts, keyword_pts,
matched_keywords, notice_id, title, buyer_name, buyer_country,
estimated_value, currency, submission_deadline, days_left, cpv_code, ted_url.`,
		Example: strings.Trim(`
  eu-tenders-pp-cli score --country DEU --cpv 45 --keywords Hochbau,Neubau --agent
  eu-tenders-pp-cli score --country FRA --cpv 72 --keywords cloud --max-days 30 --json
  eu-tenders-pp-cli score --country DEU --keywords Neubau,Schule --min-value 500000 --limit 10`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "auto",
			"pp:happy-args":  "--country=DEU;--cpv=45;--keywords=Neubau,Hochbau",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "rank open tenders by bid fit")
			}
			if maxDays <= 0 {
				return usageErr(fmt.Errorf("--max-days must be positive"))
			}
			if err := validateTEDFilters(country, cpv); err != nil {
				return err
			}
			calls, err := loadOpenCalls(cmd, flags, country, cpv, maxDays, dbPath, maxScan)
			if err != nil {
				return err
			}
			kw := parseCSVList(keywords)
			now := time.Now()
			kept, maxValue := rankableCalls(calls, now, maxDays, minValue)
			out := make([]scoreRow, 0, len(kept))
			for _, c := range kept {
				days := daysUntil(c.SubmissionDeadline, now)
				p := scoreCall(days, maxDays, c.EstimatedValue, maxValue, c.Title, kw)
				row := scoreRow{
					Score: p.Total, UrgencyPts: p.Urgency, ValuePts: p.Value, KeywordPts: p.Keyword,
					MatchedKeywords: p.Matched, NoticeID: c.ID, Title: c.Title, BuyerName: c.BuyerName,
					BuyerCountry: c.BuyerCountry, EstimatedValue: round2(c.EstimatedValue), Currency: c.Currency,
					SubmissionDeadline: c.SubmissionDeadline, DaysLeft: days, CPVCode: c.CPVCode, TEDURL: c.NoticeURL,
				}
				if len(kw) == 0 {
					row.Note = scoreNoKeywordsNote
				}
				out = append(out, row)
			}
			sort.SliceStable(out, func(a, b int) bool {
				if out[a].Score != out[b].Score {
					return out[a].Score > out[b].Score
				}
				return out[a].DaysLeft < out[b].DaysLeft
			})
			if limit > 0 && len(out) > limit {
				out = out[:limit]
			}
			for i := range out {
				out[i].Rank = i + 1
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), out, flags)
			}
			if len(out) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No open tenders match. Raise --max-days, drop --min-value, or run sync --type call.")
				return nil
			}
			if len(kw) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Note: "+scoreNoKeywordsNote+".")
			}
			tw := newTabWriter(cmd.OutOrStdout())
			fmt.Fprintln(tw, "RANK\tSCORE\tDAYS\tVALUE\tMATCHED\tBUYER\tTITLE")
			for _, r := range out {
				fmt.Fprintf(tw, "%d\t%.1f\t%d\t%.0f\t%s\t%s\t%s\n", r.Rank, r.Score, r.DaysLeft, r.EstimatedValue,
					strings.Join(r.MatchedKeywords, ","), truncate(r.BuyerName, 32), truncate(r.Title, 50))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&country, "country", "", "Buyer country, 3-letter ISO code (e.g. DEU)")
	cmd.Flags().StringVar(&cpv, "cpv", "", "CPV code or prefix (e.g. 45 for construction, 72 for IT services)")
	cmd.Flags().StringVar(&keywords, "keywords", "", "Comma-separated keywords matched in the title (e.g. \"Neubau,Hochbau\")")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum rows to return")
	cmd.Flags().IntVar(&maxDays, "max-days", 60, "Only calls whose submission deadline is within the next N days")
	cmd.Flags().Float64Var(&minValue, "min-value", 0, "Minimum estimated value in EUR (0 keeps calls without a reported value)")
	cmd.Flags().IntVar(&maxScan, "max-scan", 500, "Live mode: maximum calls for tender to read from TED")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}
