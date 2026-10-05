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

// wonValueSQL is a company's value on one award: its own lot value when TED
// reported it, the notice total only when it is the sole winner, else 0.
// It expects notice_winners aliased w and notices aliased n.
const wonValueSQL = `(CASE WHEN w.value > 0 THEN w.value WHEN n.winner_count = 1 THEN n.contract_value ELSE 0 END)`

// cpvDivisionCode folds a CPV code to its division (first two digits).
func cpvDivisionCode(code string) string {
	code = strings.TrimSpace(code)
	if len(code) < 2 {
		return ""
	}
	return code[:2] + "000000"
}

// medianFloat returns the median of vals, or 0 for an empty slice.
func medianFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

// activeMonths is the span between two YYYY-MM-DD dates in months, at least 1.
func activeMonths(first, last string) float64 {
	a, errA := time.Parse("2006-01-02", first)
	b, errB := time.Parse("2006-01-02", last)
	if errA != nil || errB != nil {
		return 1
	}
	m := b.Sub(a).Hours() / 24 / 30.44
	if m < 1 {
		return 1
	}
	return m
}

type cpvCount struct {
	CPV         string `json:"cpv"`
	Count       int    `json:"count"`
	Description string `json:"description"`
}

type buyerWinner struct {
	Name       string  `json:"name"`
	Country    string  `json:"country"`
	Wins       int     `json:"wins"`
	TotalValue float64 `json:"total_value"`
}

type buyerProfile struct {
	BuyerName         string        `json:"buyer_name"`
	BuyerCountry      string        `json:"buyer_country"`
	BuyerCity         string        `json:"buyer_city"`
	BuyerEmail        string        `json:"buyer_email"`
	Calls             int           `json:"calls"`
	Awards            int           `json:"awards"`
	FirstNotice       string        `json:"first_notice"`
	LastNotice        string        `json:"last_notice"`
	NoticesPerMonth   float64       `json:"notices_per_month"`
	TotalAwardedValue float64       `json:"total_awarded_value"`
	MedianAwardValue  float64       `json:"median_award_value"`
	AvgEstimatedValue float64       `json:"avg_estimated_value"`
	TopCPV            []cpvCount    `json:"top_cpv"`
	TopWinners        []buyerWinner `json:"top_winners,omitempty"`
}

type buyerKey struct{ name, country string }

func newNovelBuyerCmd(flags *rootFlags) *cobra.Command {
	var name, country, since, dbPath string
	var showWinners bool

	cmd := &cobra.Command{
		Use:   "buyer",
		Short: "Profile a contracting authority: publishing cadence, CPV mix, typical contract values and repeat winners.",
		Long: `Use this command to profile one contracting authority. Do NOT use it for market-wide share; use 'concentration' instead. Do NOT use it to profile a winning company; use 'winner' instead.

--name is a partial, case-insensitive match on the buyer name. When several
authorities match, one profile is returned per buyer name and country (up to
10, most active first). Each profile has calls and awards counts, first and
last notice date, notices per month over the active span, total and median
awarded value, the average estimated value of its calls, the top 5 CPV
divisions and, with --show-winners, the top 10 winning companies.`,
		Example: strings.Trim(`
  eu-tenders-pp-cli buyer --name 'Stadt München' --show-winners
  eu-tenders-pp-cli buyer --name Berlin --country DEU --since 365d --json
  eu-tenders-pp-cli buyer --name Warszawa --country POL --agent`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "local",
			"pp:happy-args":  "--name=Berlin",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "profile a contracting authority")
			}
			if err := requireLocalSource(flags); err != nil {
				return err
			}
			if err := validateTEDFilters(country, ""); err != nil {
				return err
			}
			term := strings.ToLower(strings.TrimSpace(name))
			if term == "" {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("--name is required, e.g. buyer --name 'Stadt München'"))
			}
			sinceDate, err := resolveSinceDate(since, time.Now())
			if err != nil {
				return usageErr(err)
			}
			dbPath = resolveTendersDB(dbPath)
			st, stop, err := openLocalMirror(cmd, flags, dbPath, make([]buyerProfile, 0))
			if stop {
				return err
			}
			defer st.Close()
			hintIfNoNotices(cmd, st, "")

			profiles, err := loadBuyerProfiles(cmd.Context(), st, term, country, sinceDate, showWinners)
			if err != nil {
				return err
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), profiles, flags)
			}
			if len(profiles) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No matching buyers.")
				return nil
			}
			w := cmd.OutOrStdout()
			for i, p := range profiles {
				if i > 0 {
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "%s (%s, %s) %s\n", p.BuyerName, p.BuyerCity, p.BuyerCountry, p.BuyerEmail)
				fmt.Fprintf(w, "  calls %d, awards %d, %s to %s, %.2f notices/month\n", p.Calls, p.Awards, p.FirstNotice, p.LastNotice, p.NoticesPerMonth)
				fmt.Fprintf(w, "  awarded %.0f total, median %.0f; avg estimate %.0f\n", p.TotalAwardedValue, p.MedianAwardValue, p.AvgEstimatedValue)
				for _, c := range p.TopCPV {
					fmt.Fprintf(w, "  CPV %s x%d %s\n", c.CPV, c.Count, c.Description)
				}
				if len(p.TopWinners) > 0 {
					tw := newTabWriter(w)
					fmt.Fprintln(tw, "  WINNER\tCOUNTRY\tWINS\tVALUE")
					for _, win := range p.TopWinners {
						fmt.Fprintf(tw, "  %s\t%s\t%d\t%.0f\n", truncate(win.Name, 40), win.Country, win.Wins, win.TotalValue)
					}
					if err := tw.Flush(); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Buyer name or part of it, case-insensitive (e.g. 'Stadt München', Berlin)")
	cmd.Flags().BoolVar(&showWinners, "show-winners", false, "Include the top 10 companies that won this buyer's awards")
	cmd.Flags().StringVar(&since, "since", "", "Only notices published on or after this date (YYYY-MM-DD) or within a duration (e.g. 365d)")
	cmd.Flags().StringVar(&country, "country", "", "Buyer country, 3-letter ISO code (e.g. DEU)")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}

func loadBuyerProfiles(ctx context.Context, st *store.Store, term, country, since string, showWinners bool) ([]buyerProfile, error) {
	f := &noticeFilterSQL{}
	f.country("buyer_country", country)
	f.since("publication_date", since)
	rows, err := st.DB().QueryContext(ctx, `SELECT buyer_name, buyer_country, COUNT(*) FROM notices`+f.where()+`
		GROUP BY buyer_name, buyer_country`, f.args...)
	if err != nil {
		return nil, fmt.Errorf("listing buyers: %w", err)
	}
	type cand struct {
		key buyerKey
		n   int
	}
	cands := make([]cand, 0)
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.key.name, &c.key.country, &c.n); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if c.key.name != "" && strings.Contains(strings.ToLower(c.key.name), term) {
			cands = append(cands, c)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	sort.SliceStable(cands, func(a, b int) bool {
		if cands[a].n != cands[b].n {
			return cands[a].n > cands[b].n
		}
		return cands[a].key.name < cands[b].key.name
	})
	if len(cands) > 10 {
		cands = cands[:10]
	}
	out := make([]buyerProfile, 0, len(cands))
	for _, c := range cands {
		p, err := buildBuyerProfile(ctx, st, c.key, since, showWinners)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func buildBuyerProfile(ctx context.Context, st *store.Store, k buyerKey, since string, showWinners bool) (buyerProfile, error) {
	p := buyerProfile{BuyerName: k.name, BuyerCountry: k.country, TopCPV: make([]cpvCount, 0)}
	f := &noticeFilterSQL{}
	f.add("n.buyer_name = ?", k.name)
	f.add("n.buyer_country = ?", k.country)
	f.since("n.publication_date", since)

	rows, err := st.DB().QueryContext(ctx, `SELECT n.notice_type, n.publication_date, n.buyer_city, n.buyer_email,
		n.estimated_value, n.contract_value, n.cpv_code FROM notices n`+f.where()+`
		ORDER BY n.publication_date DESC, n.id DESC`, f.args...)
	if err != nil {
		return p, fmt.Errorf("reading buyer notices: %w", err)
	}
	var awardValues, estimates []float64
	cpvCounts := map[string]int{}
	for rows.Next() {
		var typ, date, city, email, cpv string
		var est, val float64
		if err := rows.Scan(&typ, &date, &city, &email, &est, &val, &cpv); err != nil {
			_ = rows.Close()
			return p, err
		}
		// Rows arrive newest first, so the first non-empty contact wins.
		p.BuyerCity = firstNonEmpty(p.BuyerCity, city)
		p.BuyerEmail = firstNonEmpty(p.BuyerEmail, email)
		if date != "" {
			if p.LastNotice == "" || date > p.LastNotice {
				p.LastNotice = date
			}
			if p.FirstNotice == "" || date < p.FirstNotice {
				p.FirstNotice = date
			}
		}
		switch typ {
		case ted.NoticeTypeCall:
			p.Calls++
			if est > 0 {
				estimates = append(estimates, est)
			}
		case ted.NoticeTypeAward:
			p.Awards++
			if val > 0 {
				awardValues = append(awardValues, val)
				p.TotalAwardedValue += val
			}
		}
		if d := cpvDivisionCode(cpv); d != "" {
			cpvCounts[d]++
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return p, err
	}
	_ = rows.Close()

	p.TotalAwardedValue = round2(p.TotalAwardedValue)
	p.MedianAwardValue = round2(medianFloat(awardValues))
	if len(estimates) > 0 {
		sum := 0.0
		for _, e := range estimates {
			sum += e
		}
		p.AvgEstimatedValue = round2(sum / float64(len(estimates)))
	}
	if p.FirstNotice != "" {
		p.NoticesPerMonth = round2(float64(p.Calls+p.Awards) / activeMonths(p.FirstNotice, p.LastNotice))
	}
	p.TopCPV = topCPVCounts(cpvCounts, 5)

	if showWinners {
		p.TopWinners = make([]buyerWinner, 0)
		wf := &noticeFilterSQL{}
		wf.add("n.notice_type = ?", ted.NoticeTypeAward)
		wf.add("n.buyer_name = ?", k.name)
		wf.add("n.buyer_country = ?", k.country)
		wf.since("n.publication_date", since)
		wrows, err := st.DB().QueryContext(ctx, `SELECT MAX(w.name), w.country, COUNT(DISTINCT w.notice_id), COALESCE(SUM(`+wonValueSQL+`), 0)
			FROM notice_winners w JOIN notices n ON n.id = w.notice_id`+wf.where()+`
			GROUP BY w.name_key, w.country ORDER BY 3 DESC, 4 DESC, 1 LIMIT 10`, wf.args...)
		if err != nil {
			return p, fmt.Errorf("reading buyer winners: %w", err)
		}
		for wrows.Next() {
			var bw buyerWinner
			var nm sql.NullString
			if err := wrows.Scan(&nm, &bw.Country, &bw.Wins, &bw.TotalValue); err != nil {
				_ = wrows.Close()
				return p, err
			}
			bw.Name = nm.String
			bw.TotalValue = round2(bw.TotalValue)
			p.TopWinners = append(p.TopWinners, bw)
		}
		if err := wrows.Err(); err != nil {
			_ = wrows.Close()
			return p, err
		}
		_ = wrows.Close()
	}
	return p, nil
}

// topCPVCounts orders CPV division counts and keeps the top n with labels.
func topCPVCounts(counts map[string]int, n int) []cpvCount {
	out := make([]cpvCount, 0, len(counts))
	for code, c := range counts {
		out = append(out, cpvCount{CPV: code, Count: c, Description: cpvDescription(code)})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Count != out[b].Count {
			return out[a].Count > out[b].Count
		}
		return out[a].CPV < out[b].CPV
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
