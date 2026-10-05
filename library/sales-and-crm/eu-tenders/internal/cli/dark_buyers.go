// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// pp:data-source local

package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	darkReasonLowAwardRate = "low_award_rate"
	darkReasonSingleWinner = "single_winner"
	darkBuyerNote          = "heuristic signal, not a finding"
)

type darkBuyer struct {
	BuyerName     string   `json:"buyer_name"`
	BuyerCountry  string   `json:"buyer_country"`
	Calls         int      `json:"calls"`
	Awards        int      `json:"awards"`
	AwardRate     float64  `json:"award_rate"`
	UniqueWinners int      `json:"unique_winners"`
	TopWinner     string   `json:"top_winner"`
	Reasons       []string `json:"reasons"`
	Note          string   `json:"note"`
}

// flagDarkBuyers keeps buyers whose award rate is at or below maxRate, or
// whose three or more awards all went to one company.
func flagDarkBuyers(stats []buyerAwardStat, maxRate float64) []darkBuyer {
	out := make([]darkBuyer, 0)
	for _, b := range stats {
		reasons := make([]string, 0, 2)
		if float64(b.Awards) <= maxRate*float64(b.Calls) {
			reasons = append(reasons, darkReasonLowAwardRate)
		}
		if b.Awards >= 3 && b.UniqueWinners == 1 {
			reasons = append(reasons, darkReasonSingleWinner)
		}
		if len(reasons) == 0 {
			continue
		}
		d := darkBuyer{BuyerName: b.BuyerName, BuyerCountry: b.BuyerCountry, Calls: b.Calls, Awards: b.Awards,
			AwardRate: b.AwardRate, UniqueWinners: b.UniqueWinners, Reasons: reasons, Note: darkBuyerNote}
		if len(b.winners) > 0 {
			d.TopWinner = b.winners[0].Name
		}
		out = append(out, d)
	}
	return out
}

func newNovelDarkBuyersCmd(flags *rootFlags) *cobra.Command {
	var country, cpv, since, dbPath string
	var minCalls int
	var maxAwardRate float64

	cmd := &cobra.Command{
		Use:   "dark-buyers",
		Short: "Flag contracting authorities whose tenders rarely produce public awards or keep going to one company.",
		Long: `Use this command to flag buyers with suspicious award patterns (low award rate, single repeat winner). Do NOT use it for the full per-buyer award-rate table; use 'win-rate' instead.

Results are heuristic signals that need a manual check: a low award rate can also mean
awards are published late, below the EU threshold, or outside the synced
window. A buyer is flagged with reason low_award_rate when awards / calls is
at or below --max-award-rate, and with single_winner when it has 3 or more
awards that all went to one company (grouped by name and country). Only
buyers with at least --min-calls calls for tender are considered.`,
		Example: strings.Trim(`
  eu-tenders-pp-cli dark-buyers --country POL --cpv 45
  eu-tenders-pp-cli dark-buyers --country DEU --cpv 4523 --min-calls 5 --json
  eu-tenders-pp-cli dark-buyers --country FRA --max-award-rate 0.1 --since 365d --agent`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "local",
			"pp:happy-args":  "--country=DEU",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "flag buyers with unusual award patterns")
			}
			if err := requireLocalSource(flags); err != nil {
				return err
			}
			if err := validateTEDFilters(country, cpv); err != nil {
				return err
			}
			if minCalls < 0 {
				return usageErr(fmt.Errorf("--min-calls must not be negative"))
			}
			if maxAwardRate < 0 {
				return usageErr(fmt.Errorf("--max-award-rate must not be negative"))
			}
			sinceDate, err := resolveSinceDate(since, time.Now())
			if err != nil {
				return usageErr(err)
			}
			dbPath = resolveTendersDB(dbPath)
			st, stop, err := openLocalMirror(cmd, flags, dbPath, make([]darkBuyer, 0))
			if stop {
				return err
			}
			defer st.Close()
			hintIfNoNotices(cmd, st, "")

			stats, err := buyerAwardStats(cmd.Context(), st, buyerSlice{Country: country, CPV: cpv, Since: sinceDate, MinCalls: minCalls})
			if err != nil {
				return err
			}
			out := flagDarkBuyers(stats, maxAwardRate)
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), out, flags)
			}
			if len(out) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No buyers flagged in this slice.")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Heuristic signals; check each buyer before acting.")
			tw := newTabWriter(cmd.OutOrStdout())
			fmt.Fprintln(tw, "BUYER\tCOUNTRY\tCALLS\tAWARDS\tRATE\tWINNERS\tTOP WINNER\tREASONS")
			for _, d := range out {
				fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%.2f\t%d\t%s\t%s\n", truncate(d.BuyerName, 40), d.BuyerCountry, d.Calls, d.Awards, d.AwardRate, d.UniqueWinners, truncate(d.TopWinner, 30), strings.Join(d.Reasons, ","))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&country, "country", "", "Buyer country, 3-letter ISO code (e.g. POL)")
	cmd.Flags().StringVar(&cpv, "cpv", "", "CPV code or prefix of the market (e.g. 45 construction)")
	cmd.Flags().IntVar(&minCalls, "min-calls", 3, "Only buyers with at least this many calls for tender in the slice")
	cmd.Flags().StringVar(&since, "since", "", "Only notices published on or after this date (YYYY-MM-DD) or within a duration (e.g. 365d)")
	cmd.Flags().Float64Var(&maxAwardRate, "max-award-rate", 0.25, "Flag buyers whose awards / calls is at or below this rate")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}
