// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// pp:data-source local

package cli

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/store"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

const (
	hhiBandCompetitive = "competitive"
	hhiBandModerate    = "moderately concentrated"
	hhiBandHigh        = "highly concentrated"
	hhiBandNoData      = "no awarded value"
)

type marketWinner struct {
	Rank     int     `json:"rank"`
	Name     string  `json:"name"`
	Country  string  `json:"country"`
	Wins     int     `json:"wins"`
	Value    float64 `json:"value"`
	SharePct float64 `json:"share_pct"`
}

type concentrationResult struct {
	Country          string         `json:"country"`
	CPV              string         `json:"cpv"`
	Since            string         `json:"since"`
	Awards           int            `json:"awards"`
	WinnersTotal     int            `json:"winners_total"`
	MarketTotalValue float64        `json:"market_total_value"`
	HHI              float64        `json:"hhi"`
	Band             string         `json:"band"`
	Top              []marketWinner `json:"top"`
	HHILast12m       *float64       `json:"hhi_last_12m"`
	HHIPrior12m      *float64       `json:"hhi_prior_12m"`
}

// hhiBand classifies an HHI on the 0-10000 scale.
func hhiBand(hhi, total float64) string {
	switch {
	case total <= 0:
		return hhiBandNoData
	case hhi < 1500:
		return hhiBandCompetitive
	case hhi <= 2500:
		return hhiBandModerate
	default:
		return hhiBandHigh
	}
}

// hhiOf sums squared percentage shares over all values.
func hhiOf(values []float64) (hhi, total float64) {
	for _, v := range values {
		total += v
	}
	if total <= 0 {
		return 0, 0
	}
	for _, v := range values {
		s := v / total * 100
		hhi += s * s
	}
	return round2(hhi), total
}

// marketWinners aggregates award winners by name and country over a slice;
// until is exclusive and optional.
func marketWinners(ctx context.Context, st *store.Store, country, cpv, since, until string) ([]marketWinner, int, error) {
	f := &noticeFilterSQL{}
	f.add("n.notice_type = ?", ted.NoticeTypeAward)
	f.country("n.buyer_country", country)
	f.cpv("n.cpv_code", cpv)
	f.since("n.publication_date", since)
	if until != "" {
		f.add("n.publication_date < ?", until)
	}
	var awards int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM notices n`+f.where(), f.args...).Scan(&awards); err != nil {
		return nil, 0, fmt.Errorf("counting awards: %w", err)
	}
	rows, err := st.DB().QueryContext(ctx, `SELECT MAX(w.name), w.country, COUNT(DISTINCT w.notice_id), COALESCE(SUM(`+wonValueSQL+`), 0)
		FROM notice_winners w JOIN notices n ON n.id = w.notice_id`+f.where()+`
		GROUP BY w.name_key, w.country`, f.args...)
	if err != nil {
		return nil, 0, fmt.Errorf("aggregating winners: %w", err)
	}
	out := make([]marketWinner, 0)
	for rows.Next() {
		var m marketWinner
		var nm sql.NullString
		if err := rows.Scan(&nm, &m.Country, &m.Wins, &m.Value); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		m.Name = nm.String
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, 0, err
	}
	_ = rows.Close()
	return out, awards, nil
}

func windowHHI(ctx context.Context, st *store.Store, country, cpv, since, until string) (*float64, error) {
	ws, _, err := marketWinners(ctx, st, country, cpv, since, until)
	if err != nil {
		return nil, err
	}
	vals := make([]float64, 0, len(ws))
	for _, w := range ws {
		vals = append(vals, w.Value)
	}
	hhi, total := hhiOf(vals)
	if total <= 0 {
		return nil, nil
	}
	return &hhi, nil
}

func newNovelConcentrationCmd(flags *rootFlags) *cobra.Command {
	var country, cpv, since, dbPath string
	var top int

	cmd := &cobra.Command{
		Use:   "concentration",
		Short: "See which companies capture what share of awarded value in a sector and country, with an HHI concentration score.",
		Long: `Use this command for market-level winner share and HHI in a country/CPV slice. Do NOT use it for one buyer's winners; use 'buyer' instead. Do NOT use it for one company's wins; use 'winner' instead.

Each winner (grouped by company name and country) is credited its own lot
value, or the notice total when it was the sole winner. share_pct is its share
of market_total_value; hhi is the sum of squared shares over all winners
(0-10000): below 1500 competitive, 1500-2500 moderately concentrated, above
2500 highly concentrated. Winners without a reported value count toward
winners_total but not toward shares. hhi_last_12m and hhi_prior_12m compare
the last 12 months with the 12 months before; they are null when the window
has no awarded value or --since cuts into it.`,
		Example: strings.Trim(`
  eu-tenders-pp-cli concentration --country DEU --cpv 45
  eu-tenders-pp-cli concentration --country POL --cpv 4523 --top 5 --json
  eu-tenders-pp-cli concentration --country FRA --cpv 72 --since 365d --agent`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "local",
			"pp:happy-args":  "--country=DEU;--cpv=45",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "compute market concentration")
			}
			if err := requireLocalSource(flags); err != nil {
				return err
			}
			if err := validateTEDFilters(country, cpv); err != nil {
				return err
			}
			if top <= 0 {
				return usageErr(fmt.Errorf("--top must be positive"))
			}
			now := time.Now()
			sinceDate, err := resolveSinceDate(since, now)
			if err != nil {
				return usageErr(err)
			}
			dbPath = resolveTendersDB(dbPath)
			res := concentrationResult{Country: strings.ToUpper(strings.TrimSpace(country)), CPV: cpv, Since: sinceDate,
				Band: hhiBandNoData, Top: make([]marketWinner, 0)}
			st, stop, err := openLocalMirror(cmd, flags, dbPath, res)
			if stop {
				return err
			}
			defer st.Close()
			hintIfNoNotices(cmd, st, ted.NoticeTypeAward)

			ws, awards, err := marketWinners(cmd.Context(), st, country, cpv, sinceDate, "")
			if err != nil {
				return err
			}
			res.Awards = awards
			res.WinnersTotal = len(ws)
			vals := make([]float64, 0, len(ws))
			for _, w := range ws {
				vals = append(vals, w.Value)
			}
			var total float64
			res.HHI, total = hhiOf(vals)
			res.MarketTotalValue = round2(total)
			res.Band = hhiBand(res.HHI, total)
			sort.SliceStable(ws, func(a, b int) bool {
				if ws[a].Value != ws[b].Value {
					return ws[a].Value > ws[b].Value
				}
				if ws[a].Wins != ws[b].Wins {
					return ws[a].Wins > ws[b].Wins
				}
				return ws[a].Name < ws[b].Name
			})
			for i, w := range ws {
				if len(res.Top) >= top || w.Value <= 0 {
					break
				}
				w.Rank = i + 1
				w.SharePct = round2(w.Value / total * 100)
				w.Value = round2(w.Value)
				res.Top = append(res.Top, w)
			}

			lastStart := now.AddDate(-1, 0, 0).UTC().Format("2006-01-02")
			priorStart := now.AddDate(-2, 0, 0).UTC().Format("2006-01-02")
			if sinceDate == "" || sinceDate <= lastStart {
				if res.HHILast12m, err = windowHHI(cmd.Context(), st, country, cpv, lastStart, ""); err != nil {
					return err
				}
			}
			if sinceDate == "" || sinceDate <= priorStart {
				if res.HHIPrior12m, err = windowHHI(cmd.Context(), st, country, cpv, priorStart, lastStart); err != nil {
					return err
				}
			}

			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), res, flags)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%d awards, %d winners, %.0f awarded; HHI %.0f (%s)\n", res.Awards, res.WinnersTotal, res.MarketTotalValue, res.HHI, res.Band)
			if res.HHILast12m != nil && res.HHIPrior12m != nil {
				fmt.Fprintf(w, "HHI last 12 months %.0f vs prior 12 months %.0f\n", *res.HHILast12m, *res.HHIPrior12m)
			}
			if len(res.Top) == 0 {
				fmt.Fprintln(w, "No winners with a reported value in this slice.")
				return nil
			}
			tw := newTabWriter(w)
			fmt.Fprintln(tw, "RANK\tCOMPANY\tCOUNTRY\tWINS\tVALUE\tSHARE %")
			for _, m := range res.Top {
				fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%.0f\t%.2f\n", m.Rank, truncate(m.Name, 40), m.Country, m.Wins, m.Value, m.SharePct)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&country, "country", "", "Buyer country, 3-letter ISO code (e.g. DEU)")
	cmd.Flags().StringVar(&cpv, "cpv", "", "CPV code or prefix of the market (e.g. 45 construction, 4523 roads and pipelines)")
	cmd.Flags().StringVar(&since, "since", "", "Only awards published on or after this date (YYYY-MM-DD) or within a duration (e.g. 365d)")
	cmd.Flags().IntVar(&top, "top", 5, "Number of top winners to list")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}
