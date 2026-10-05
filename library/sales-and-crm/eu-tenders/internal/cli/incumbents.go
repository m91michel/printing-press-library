// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// pp:data-source auto

package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/store"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

type incumbentTender struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	BuyerName          string `json:"buyer_name"`
	BuyerCountry       string `json:"buyer_country"`
	CPVCode            string `json:"cpv_code"`
	SubmissionDeadline string `json:"submission_deadline"`
	NoticeType         string `json:"notice_type"`
}

type incumbent struct {
	Name       string  `json:"name"`
	Country    string  `json:"country"`
	Wins       int     `json:"wins"`
	TotalValue float64 `json:"total_value"`
	LastWin    string  `json:"last_win"`
	Email      string  `json:"email"`
	Phone      string  `json:"phone"`
	City       string  `json:"city"`
}

type incumbentsResult struct {
	Tender      incumbentTender `json:"tender"`
	PriorAwards int             `json:"prior_awards"`
	Incumbents  []incumbent     `json:"incumbents"`
}

func newNovelIncumbentsCmd(flags *rootFlags) *cobra.Command {
	var dbPath string
	var cpvDigits, limit, maxScan int

	cmd := &cobra.Command{
		Use:   "incumbents [publication-number]",
		Short: "For one open tender, see who previously won contracts from the same buyer in the same category.",
		Long: `Use this command to see who previously won from the same buyer for one specific open tender. Do NOT use it for a buyer's full profile; use 'buyer' instead.

Prior awards are award notices from the same buyer name and country whose CPV
code shares the first --cpv-digits digits with the tender, plus the notice the
tender names as its previous notice. Winners are grouped by company name and
country, ordered by wins and then by latest win, with the latest contact data.

Reads the local store first; when the tender is not synced (or with
--data-source live) it loads the tender and the buyer's prior awards from TED.

Exit codes: 0 found, 3 notice not in the local store`,
		Example: strings.Trim(`
  eu-tenders-pp-cli incumbents 680471-2026
  eu-tenders-pp-cli incumbents 680471-2026 --cpv-digits 2 --limit 5 --json
  eu-tenders-pp-cli incumbents 612345-2026 --cpv-digits 4 --agent`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":       "true",
			"pp:data-source":      "auto",
			"pp:happy-args":       "publication-number=680471-2026",
			"pp:typed-exit-codes": "0,3",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "find incumbents for a tender")
			}
			if len(args) == 0 {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("a publication number is required, e.g. incumbents 680471-2026"))
			}
			id := strings.TrimSpace(args[0])
			if !publicationNumberRE.MatchString(id) {
				return usageErr(fmt.Errorf("invalid publication number %q: expected digits-year, e.g. 680471-2026", id))
			}
			if cpvDigits < 2 || cpvDigits > 8 {
				return usageErr(fmt.Errorf("--cpv-digits must be between 2 and 8, got %d", cpvDigits))
			}
			if limit <= 0 {
				return usageErr(fmt.Errorf("--limit must be positive"))
			}
			var res incumbentsResult
			found := false
			dbPath = resolveTendersDB(dbPath)
			if activeSource(flags) != sourceLive {
				recordSource(flags, sourceLocal)
				st, synced, err := openTendersForRead(cmd.Context(), cmd.ErrOrStderr(), dbPath)
				if err != nil {
					return err
				}
				if synced {
					res, found, err = loadIncumbents(cmd.Context(), st, id, cpvDigits, limit)
					_ = st.Close()
					if err != nil {
						return err
					}
				}
				if !found && activeSource(flags) == sourceLocal {
					return notFoundErr(fmt.Errorf("notice %s is not in the local store; run %s sync or use --data-source live", id, tendersCLIName))
				}
			}
			if !found {
				recordSource(flags, sourceLive)
				ctx, cancel := boundCtx(cmd.Context(), flags)
				resp, err := tedSearch(ctx, flags, ted.SearchRequest{Query: ted.PublicationQuery(id), Fields: ted.SyncFields, Limit: 1})
				cancel()
				if err != nil {
					return err
				}
				if len(resp.Notices) == 0 {
					return notFoundErr(fmt.Errorf("TED has no notice %s", id))
				}
				tenderRaw := resp.Notices[0]
				tender := ted.Extract(tenderRaw)
				prefix := tender.CPVCode
				if len(prefix) > cpvDigits {
					prefix = prefix[:cpvDigits]
				}
				// The tender's country and CPV come from TED and are spliced
				// into the next expert query, so they get the same shape check
				// as user flags. A malformed value skips the prior-award
				// search, which yields zero incumbents.
				var st *store.Store
				cleanup := func() {}
				scanned := 0
				if err := validateTEDFilters(tender.BuyerCountry, prefix); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "note: notice %s has an unusable buyer country or CPV (%q, %q); skipping the prior-award search\n", id, tender.BuyerCountry, prefix)
					st, cleanup, err = openScratchStore(cmd)
					if err != nil {
						return err
					}
				} else {
					q := ted.BuildQuery(ted.Filter{
						Query:       "buyer-name~" + tedQuoted(tender.BuyerName),
						Country:     tender.BuyerCountry,
						CPV:         prefix,
						NoticeTypes: []string{ted.NoticeTypeAward},
					})
					st, cleanup, scanned, err = liveScratchStore(cmd, flags, q, maxScan)
					if err != nil {
						return err
					}
				}
				// The tender comes from its own lookup above. Searching for it
				// again inside the capped prior-award scan could drop it once
				// the buyer has more awards than --max-scan.
				if err := st.UpsertNotices(cmd.Context(), []ted.Notice{tender}, []map[string]any{tenderRaw}); err != nil {
					cleanup()
					return err
				}
				res, found, err = loadIncumbents(cmd.Context(), st, id, cpvDigits, limit)
				cleanup()
				if err != nil {
					return err
				}
				if !found {
					return notFoundErr(fmt.Errorf("TED has no notice %s", id))
				}
				if !flags.quiet {
					fmt.Fprintf(cmd.ErrOrStderr(), "checked %d live TED notices for prior awards (raise --max-scan to widen)\n", scanned)
				}
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), res, flags)
			}
			w := cmd.OutOrStdout()
			t := res.Tender
			fmt.Fprintf(w, "%s %s\n%s (%s), CPV %s, deadline %s\n%d prior awards\n", t.ID, t.Title, t.BuyerName, t.BuyerCountry, t.CPVCode, t.SubmissionDeadline, res.PriorAwards)
			if len(res.Incumbents) == 0 {
				fmt.Fprintln(w, "No prior winners from this buyer in this category.")
				return nil
			}
			tw := newTabWriter(w)
			fmt.Fprintln(tw, "COMPANY\tCOUNTRY\tWINS\tVALUE\tLAST WIN\tEMAIL\tPHONE")
			for _, i := range res.Incumbents {
				fmt.Fprintf(tw, "%s\t%s\t%d\t%.0f\t%s\t%s\t%s\n", truncate(i.Name, 40), i.Country, i.Wins, i.TotalValue, i.LastWin, i.Email, i.Phone)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().IntVar(&maxScan, "max-scan", 250, "Live fallback: maximum prior award notices to read from TED")
	cmd.Flags().IntVar(&cpvDigits, "cpv-digits", 3, "Leading CPV digits a prior award must share with the tender (2-8; 2 = same division)")
	cmd.Flags().IntVar(&limit, "limit", 10, "Maximum incumbents to return")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}

func loadIncumbents(ctx context.Context, st *store.Store, id string, cpvDigits, limit int) (incumbentsResult, bool, error) {
	res := incumbentsResult{Incumbents: make([]incumbent, 0)}
	var prev string
	err := st.DB().QueryRowContext(ctx, `SELECT id, title, buyer_name, buyer_country, cpv_code, submission_deadline,
		notice_type, previous_notice_id FROM notices WHERE id = ?`, id).Scan(&res.Tender.ID, &res.Tender.Title,
		&res.Tender.BuyerName, &res.Tender.BuyerCountry, &res.Tender.CPVCode, &res.Tender.SubmissionDeadline,
		&res.Tender.NoticeType, &prev)
	if errors.Is(err, sql.ErrNoRows) {
		return res, false, nil
	}
	if err != nil {
		return res, false, fmt.Errorf("reading notice %s: %w", id, err)
	}
	prefix := strings.TrimSpace(res.Tender.CPVCode)
	if len(prefix) > cpvDigits {
		prefix = prefix[:cpvDigits]
	}
	cond := `n.notice_type = ? AND n.id <> ? AND ((n.buyer_name = ? AND n.buyer_country = ? AND n.cpv_code LIKE ?) OR (n.id = ? AND ? <> ''))`
	condArgs := []any{ted.NoticeTypeAward, id, res.Tender.BuyerName, res.Tender.BuyerCountry, prefix + "%", prev, prev}

	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM notices n WHERE `+cond, condArgs...).Scan(&res.PriorAwards); err != nil {
		return res, true, fmt.Errorf("counting prior awards: %w", err)
	}
	rows, err := st.DB().QueryContext(ctx, `SELECT w.name_key, w.country, w.name, w.email, w.phone, w.city,
		n.publication_date, `+wonValueSQL+`
		FROM notice_winners w JOIN notices n ON n.id = w.notice_id WHERE `+cond+`
		ORDER BY n.publication_date DESC, n.id DESC`, condArgs...)
	if err != nil {
		return res, true, fmt.Errorf("reading prior winners: %w", err)
	}
	idx := map[string]int{}
	for rows.Next() {
		var nameKey, country, name, email, phone, city, date string
		var value float64
		if err := rows.Scan(&nameKey, &country, &name, &email, &phone, &city, &date, &value); err != nil {
			_ = rows.Close()
			return res, true, err
		}
		k := companyKey(nameKey, country)
		i, ok := idx[k]
		if !ok {
			i = len(res.Incumbents)
			idx[k] = i
			res.Incumbents = append(res.Incumbents, incumbent{Name: name, Country: country, LastWin: date})
		}
		inc := &res.Incumbents[i]
		inc.Wins++
		inc.TotalValue = round2(inc.TotalValue + value)
		inc.Email = firstNonEmpty(inc.Email, email)
		inc.Phone = firstNonEmpty(inc.Phone, phone)
		inc.City = firstNonEmpty(inc.City, city)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return res, true, err
	}
	_ = rows.Close()
	sort.SliceStable(res.Incumbents, func(a, b int) bool {
		if res.Incumbents[a].Wins != res.Incumbents[b].Wins {
			return res.Incumbents[a].Wins > res.Incumbents[b].Wins
		}
		return res.Incumbents[a].LastWin > res.Incumbents[b].LastWin
	})
	if len(res.Incumbents) > limit {
		res.Incumbents = res.Incumbents[:limit]
	}
	return res, true, nil
}
