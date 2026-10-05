// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// pp:data-source local

package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/store"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

type buyerWinShare struct {
	Name    string `json:"name"`
	Country string `json:"country"`
	Wins    int    `json:"wins"`
}

// buyerAwardStat is one buyer's call/award balance in a market slice.
type buyerAwardStat struct {
	BuyerName       string          `json:"buyer_name"`
	BuyerCountry    string          `json:"buyer_country"`
	Calls           int             `json:"calls"`
	Awards          int             `json:"awards"`
	AwardRate       float64         `json:"award_rate"`
	UniqueWinners   int             `json:"unique_winners"`
	WinnerDiversity float64         `json:"winner_diversity"`
	TopWinners      []buyerWinShare `json:"top_winners,omitempty"`

	winners []buyerWinShare
}

type buyerSlice struct {
	Country, CPV, Since string
	MinCalls            int
}

// buyerAwardStats aggregates calls, awards and distinct winners per buyer
// (name + country) over the slice. Buyers with no calls or fewer than
// MinCalls are dropped. Winners are sorted by wins.
func buyerAwardStats(ctx context.Context, st *store.Store, s buyerSlice) ([]buyerAwardStat, error) {
	f := &noticeFilterSQL{}
	f.country("n.buyer_country", s.Country)
	f.cpv("n.cpv_code", s.CPV)
	f.since("n.publication_date", s.Since)
	rows, err := st.DB().QueryContext(ctx, `SELECT n.buyer_name, n.buyer_country,
		COALESCE(SUM(CASE WHEN n.notice_type = ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN n.notice_type = ? THEN 1 ELSE 0 END), 0)
		FROM notices n`+f.where()+` GROUP BY n.buyer_name, n.buyer_country`,
		append([]any{ted.NoticeTypeCall, ted.NoticeTypeAward}, f.args...)...)
	if err != nil {
		return nil, fmt.Errorf("aggregating buyers: %w", err)
	}
	stats := make([]buyerAwardStat, 0)
	idx := map[buyerKey]int{}
	for rows.Next() {
		var b buyerAwardStat
		if err := rows.Scan(&b.BuyerName, &b.BuyerCountry, &b.Calls, &b.Awards); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if b.BuyerName == "" || b.Calls == 0 || b.Calls < s.MinCalls {
			continue
		}
		b.AwardRate = round2(float64(b.Awards) / float64(b.Calls))
		idx[buyerKey{b.BuyerName, b.BuyerCountry}] = len(stats)
		stats = append(stats, b)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	wf := &noticeFilterSQL{}
	wf.add("n.notice_type = ?", ted.NoticeTypeAward)
	wf.country("n.buyer_country", s.Country)
	wf.cpv("n.cpv_code", s.CPV)
	wf.since("n.publication_date", s.Since)
	wrows, err := st.DB().QueryContext(ctx, `SELECT n.buyer_name, n.buyer_country, MAX(w.name), w.country, COUNT(DISTINCT w.notice_id)
		FROM notice_winners w JOIN notices n ON n.id = w.notice_id`+wf.where()+`
		GROUP BY n.buyer_name, n.buyer_country, w.name_key, w.country`, wf.args...)
	if err != nil {
		return nil, fmt.Errorf("aggregating buyer winners: %w", err)
	}
	for wrows.Next() {
		var bk buyerKey
		var ws buyerWinShare
		if err := wrows.Scan(&bk.name, &bk.country, &ws.Name, &ws.Country, &ws.Wins); err != nil {
			_ = wrows.Close()
			return nil, err
		}
		if i, ok := idx[bk]; ok {
			stats[i].winners = append(stats[i].winners, ws)
		}
	}
	if err := wrows.Err(); err != nil {
		_ = wrows.Close()
		return nil, err
	}
	_ = wrows.Close()

	for i := range stats {
		b := &stats[i]
		sort.SliceStable(b.winners, func(x, y int) bool {
			if b.winners[x].Wins != b.winners[y].Wins {
				return b.winners[x].Wins > b.winners[y].Wins
			}
			return b.winners[x].Name < b.winners[y].Name
		})
		b.UniqueWinners = len(b.winners)
		if b.Awards > 0 {
			b.WinnerDiversity = round2(float64(b.UniqueWinners) / float64(b.Awards))
		}
	}
	sort.SliceStable(stats, func(x, y int) bool {
		if stats[x].Calls != stats[y].Calls {
			return stats[x].Calls > stats[y].Calls
		}
		return stats[x].BuyerName < stats[y].BuyerName
	})
	return stats, nil
}

func newNovelWinRateCmd(flags *rootFlags) *cobra.Command {
	var country, cpv, since, dbPath string
	var minCalls int
	var showWinners bool

	cmd := &cobra.Command{
		Use:   "win-rate",
		Short: "See per buyer how many calls for tender end in published awards and how many distinct companies win.",
		Long: `Use this command to list award rate and winner diversity for every buyer in a market. Do NOT use it to flag only anomalous buyers; use 'dark-buyers' instead.

One row per buyer (name + country) with at least --min-calls calls for tender
in the slice: calls, awards, award_rate (awards / calls in the same window;
calls and awards are not matched one to one, so rates above 1 are possible),
unique_winners (distinct companies by name and country) and winner_diversity
(unique_winners / awards). --show-winners adds the top 3 winning companies.
Ordered by calls, most first.`,
		Example: strings.Trim(`
  eu-tenders-pp-cli win-rate --country DEU --cpv 45 --min-calls 3
  eu-tenders-pp-cli win-rate --country POL --cpv 4523 --since 365d --show-winners --json
  eu-tenders-pp-cli win-rate --country FRA --cpv 72 --min-calls 5 --agent`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "local",
			"pp:happy-args":  "--country=DEU;--min-calls=1",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "compute buyer award rates")
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
			sinceDate, err := resolveSinceDate(since, time.Now())
			if err != nil {
				return usageErr(err)
			}
			dbPath = resolveTendersDB(dbPath)
			st, stop, err := openLocalMirror(cmd, flags, dbPath, make([]buyerAwardStat, 0))
			if stop {
				return err
			}
			defer st.Close()
			hintIfNoNotices(cmd, st, "")

			stats, err := buyerAwardStats(cmd.Context(), st, buyerSlice{Country: country, CPV: cpv, Since: sinceDate, MinCalls: minCalls})
			if err != nil {
				return err
			}
			if showWinners {
				for i := range stats {
					top := stats[i].winners
					if len(top) > 3 {
						top = top[:3]
					}
					stats[i].TopWinners = top
				}
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), stats, flags)
			}
			if len(stats) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No buyers with enough calls in this slice. Lower --min-calls or run sync.")
				return nil
			}
			tw := newTabWriter(cmd.OutOrStdout())
			fmt.Fprintln(tw, "BUYER\tCOUNTRY\tCALLS\tAWARDS\tRATE\tWINNERS\tDIVERSITY\tTOP WINNERS")
			for _, b := range stats {
				names := make([]string, 0, len(b.TopWinners))
				for _, w := range b.TopWinners {
					names = append(names, truncate(w.Name, 25))
				}
				fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%.2f\t%d\t%.2f\t%s\n", truncate(b.BuyerName, 40), b.BuyerCountry, b.Calls, b.Awards, b.AwardRate, b.UniqueWinners, b.WinnerDiversity, strings.Join(names, "; "))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&country, "country", "", "Buyer country, 3-letter ISO code (e.g. DEU)")
	cmd.Flags().StringVar(&cpv, "cpv", "", "CPV code or prefix of the market (e.g. 45 construction, 72 IT services)")
	cmd.Flags().IntVar(&minCalls, "min-calls", 3, "Only buyers with at least this many calls for tender in the slice")
	cmd.Flags().BoolVar(&showWinners, "show-winners", false, "Include each buyer's top 3 winning companies")
	cmd.Flags().StringVar(&since, "since", "", "Only notices published on or after this date (YYYY-MM-DD) or within a duration (e.g. 365d)")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}
