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

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/store"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

type winnerBuyer struct {
	BuyerName    string `json:"buyer_name"`
	BuyerCountry string `json:"buyer_country"`
	Wins         int    `json:"wins"`
}

type winnerAward struct {
	NoticeID      string  `json:"notice_id"`
	PublishedDate string  `json:"published_date"`
	Title         string  `json:"title"`
	Value         float64 `json:"value"`
	BuyerName     string  `json:"buyer_name"`
	TEDURL        string  `json:"ted_url"`
}

type winnerProfile struct {
	Name         string        `json:"name"`
	Country      string        `json:"country"`
	City         string        `json:"city"`
	PostCode     string        `json:"post_code"`
	NUTS         string        `json:"nuts"`
	Email        string        `json:"email"`
	Phone        string        `json:"phone"`
	Identifier   string        `json:"identifier"`
	Size         string        `json:"size"`
	Wins         int           `json:"wins"`
	LotsWon      int           `json:"lots_won"`
	TotalValue   float64       `json:"total_value"`
	FirstWin     string        `json:"first_win"`
	LastWin      string        `json:"last_win"`
	Buyers       []winnerBuyer `json:"buyers"`
	CPVMix       []cpvCount    `json:"cpv_mix"`
	Regions      []string      `json:"regions"`
	RecentAwards []winnerAward `json:"recent_awards"`
}

func newNovelWinnerCmd(flags *rootFlags) *cobra.Command {
	var country, since, dbPath string
	var maxScan int

	cmd := &cobra.Command{
		Use:   "winner [name]",
		Short: "Profile one company that wins public contracts: win timeline, total value, buyers, regions and latest contact data.",
		Long: `Use this command to profile one winning company. Do NOT use it for a list of new leads; use 'leads' instead. Do NOT use it for an authority; use 'buyer' instead.

The name is a partial, case-insensitive match. Companies are kept apart by
name and country, so a same-name firm in another country is a separate
profile (up to 10, most wins first). Contact fields (city, post code, NUTS,
email, phone, identifier, size) come from the most recent award that reports
them. Each profile lists wins, lots won, total value (the company's own lot
value, or the notice total when it was the sole winner), first and last win,
top 5 buyers, top 5 CPV divisions, up to 10 project regions (NUTS) and the 5
most recent awards with TED links.

Reads the local store first; when it has no matching awards (or with
--data-source live) it profiles the company from live TED award notices.`,
		Example: strings.Trim(`
  eu-tenders-pp-cli winner 'Johann Bunte' --country DEU
  eu-tenders-pp-cli winner Bunte --since 365d --json
  eu-tenders-pp-cli winner Budimex --country POL --agent`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "auto",
			"pp:happy-args":  "name=Bunte",
			// A name with no award history is an empty profile list, exit 0.
			"pp:no-error-path-probe": "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "profile a winning company")
			}
			term := ted.NormalizeName(strings.Join(args, " "))
			if term == "" {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("a company name is required, e.g. winner 'Johann Bunte'"))
			}
			if err := validateTEDFilters(country, ""); err != nil {
				return err
			}
			sinceDate, err := resolveSinceDate(since, time.Now())
			if err != nil {
				return usageErr(err)
			}
			profiles := make([]winnerProfile, 0)
			found := false
			dbPath = resolveTendersDB(dbPath)
			if activeSource(flags) != sourceLive {
				recordSource(flags, sourceLocal)
				st, synced, err := openTendersForRead(cmd.Context(), cmd.ErrOrStderr(), dbPath)
				if err != nil {
					return err
				}
				if synced {
					profiles, err = loadWinnerProfiles(cmd.Context(), st, term, country, sinceDate)
					_ = st.Close()
					if err != nil {
						return err
					}
					found = len(profiles) > 0
				}
				if !found && activeSource(flags) == sourceLocal {
					fmt.Fprintf(cmd.ErrOrStderr(), "hint: no local awards for %q; run %s sync or use --data-source live\n", term, tendersCLIName)
				}
			}
			if !found && activeSource(flags) != sourceLocal {
				recordSource(flags, sourceLive)
				q := ted.BuildQuery(ted.Filter{
					Query: "winner-name~" + tedQuoted(strings.Join(args, " ")),
					Since: sinceDate, NoticeTypes: []string{ted.NoticeTypeAward},
				})
				st, cleanup, scanned, err := liveScratchStore(cmd, flags, q, maxScan)
				if err != nil {
					return err
				}
				profiles, err = loadWinnerProfiles(cmd.Context(), st, term, country, sinceDate)
				cleanup()
				if err != nil {
					return err
				}
				if !flags.quiet {
					fmt.Fprintf(cmd.ErrOrStderr(), "profiled from %d live TED award notices (raise --max-scan to widen)\n", scanned)
				}
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), profiles, flags)
			}
			if len(profiles) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No matching winners.")
				return nil
			}
			w := cmd.OutOrStdout()
			for i, p := range profiles {
				if i > 0 {
					fmt.Fprintln(w)
				}
				fmt.Fprintf(w, "%s (%s %s, %s) %s %s\n", p.Name, p.PostCode, p.City, p.Country, p.Email, p.Phone)
				fmt.Fprintf(w, "  %d wins, %d lots, %.0f total, %s to %s\n", p.Wins, p.LotsWon, p.TotalValue, p.FirstWin, p.LastWin)
				for _, b := range p.Buyers {
					fmt.Fprintf(w, "  buyer %s (%s) x%d\n", b.BuyerName, b.BuyerCountry, b.Wins)
				}
				if len(p.Regions) > 0 {
					fmt.Fprintf(w, "  regions %s\n", strings.Join(p.Regions, ", "))
				}
				tw := newTabWriter(w)
				fmt.Fprintln(tw, "  DATE\tVALUE\tBUYER\tTITLE")
				for _, a := range p.RecentAwards {
					fmt.Fprintf(tw, "  %s\t%.0f\t%s\t%s\n", a.PublishedDate, a.Value, truncate(a.BuyerName, 30), truncate(a.Title, 50))
				}
				if err := tw.Flush(); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&country, "country", "", "Company country, 3-letter ISO code (e.g. DEU); keeps same-name firms elsewhere out")
	cmd.Flags().StringVar(&since, "since", "", "Only awards published on or after this date (YYYY-MM-DD) or within a duration (e.g. 365d)")
	cmd.Flags().IntVar(&maxScan, "max-scan", 250, "Live fallback: maximum award notices to read from TED")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}

func loadWinnerProfiles(ctx context.Context, st *store.Store, term, country, since string) ([]winnerProfile, error) {
	f := &noticeFilterSQL{}
	f.add("n.notice_type = ?", ted.NoticeTypeAward)
	f.add(`w.name_key LIKE ? ESCAPE '\'`, likeSubstring(term))
	f.country(`w."country"`, country)
	f.since("n.publication_date", since)
	rows, err := st.DB().QueryContext(ctx, `SELECT w.name_key, w.country, COUNT(DISTINCT w.notice_id)
		FROM notice_winners w JOIN notices n ON n.id = w.notice_id`+f.where()+`
		GROUP BY w.name_key, w.country ORDER BY 3 DESC, 1, 2 LIMIT 10`, f.args...)
	if err != nil {
		return nil, fmt.Errorf("matching winners: %w", err)
	}
	type key struct{ nameKey, country string }
	keys := make([]key, 0)
	for rows.Next() {
		var k key
		var wins int
		if err := rows.Scan(&k.nameKey, &k.country, &wins); err != nil {
			_ = rows.Close()
			return nil, err
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	out := make([]winnerProfile, 0, len(keys))
	for _, k := range keys {
		p, err := buildWinnerProfile(ctx, st, k.nameKey, k.country, since)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func buildWinnerProfile(ctx context.Context, st *store.Store, nameKey, country, since string) (winnerProfile, error) {
	p := winnerProfile{Country: country, Buyers: make([]winnerBuyer, 0), CPVMix: make([]cpvCount, 0),
		Regions: make([]string, 0), RecentAwards: make([]winnerAward, 0)}
	f := &noticeFilterSQL{}
	f.add("n.notice_type = ?", ted.NoticeTypeAward)
	f.add("w.name_key = ?", nameKey)
	f.add(`w."country" = ?`, country)
	f.since("n.publication_date", since)
	rows, err := st.DB().QueryContext(ctx, `SELECT n.id, n.publication_date, n.title, n.buyer_name, n.buyer_country,
		n.cpv_code, n.place_of_performance, n.notice_url, w.name, w.city, w.post_code, w.nuts, w.email, w.phone,
		w.identifier, w.size, w.lots_won, `+wonValueSQL+`
		FROM notice_winners w JOIN notices n ON n.id = w.notice_id`+f.where()+`
		ORDER BY n.publication_date DESC, n.id DESC`, f.args...)
	if err != nil {
		return p, fmt.Errorf("reading winner awards: %w", err)
	}
	buyerIdx := map[buyerKey]int{}
	cpvCounts := map[string]int{}
	for rows.Next() {
		var a winnerAward
		var buyerCountry, cpv, place, name, city, post, nuts, email, phone, ident, size string
		var lots int
		if err := rows.Scan(&a.NoticeID, &a.PublishedDate, &a.Title, &a.BuyerName, &buyerCountry, &cpv, &place, &a.TEDURL,
			&name, &city, &post, &nuts, &email, &phone, &ident, &size, &lots, &a.Value); err != nil {
			_ = rows.Close()
			return p, err
		}
		// Newest first: the first non-empty value of each contact field is the latest.
		p.Name = firstNonEmpty(p.Name, name)
		p.City = firstNonEmpty(p.City, city)
		p.PostCode = firstNonEmpty(p.PostCode, post)
		p.NUTS = firstNonEmpty(p.NUTS, nuts)
		p.Email = firstNonEmpty(p.Email, email)
		p.Phone = firstNonEmpty(p.Phone, phone)
		p.Identifier = firstNonEmpty(p.Identifier, ident)
		p.Size = firstNonEmpty(p.Size, size)

		p.Wins++
		p.LotsWon += lots
		p.TotalValue += a.Value
		if p.LastWin == "" {
			p.LastWin = a.PublishedDate
		}
		p.FirstWin = a.PublishedDate

		bk := buyerKey{a.BuyerName, buyerCountry}
		if i, ok := buyerIdx[bk]; ok {
			p.Buyers[i].Wins++
		} else {
			buyerIdx[bk] = len(p.Buyers)
			p.Buyers = append(p.Buyers, winnerBuyer{BuyerName: a.BuyerName, BuyerCountry: buyerCountry, Wins: 1})
		}
		if d := cpvDivisionCode(cpv); d != "" {
			cpvCounts[d]++
		}
		p.Regions = appendUnique(p.Regions, place, 10)
		if len(p.RecentAwards) < 5 {
			a.Value = round2(a.Value)
			if a.TEDURL == "" {
				a.TEDURL = ted.NoticeURL(a.NoticeID)
			}
			p.RecentAwards = append(p.RecentAwards, a)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return p, err
	}
	_ = rows.Close()

	p.TotalValue = round2(p.TotalValue)
	sort.SliceStable(p.Buyers, func(a, b int) bool { return p.Buyers[a].Wins > p.Buyers[b].Wins })
	if len(p.Buyers) > 5 {
		p.Buyers = p.Buyers[:5]
	}
	p.CPVMix = topCPVCounts(cpvCounts, 5)
	return p, nil
}
