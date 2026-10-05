// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// pp:data-source auto

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

func newNovelLeadsCmd(flags *rootFlags) *cobra.Command {
	var (
		country, cpv, keywords, region, groupBy, dbPath string
		days, limit, maxScan                            int
		minValue                                        float64
		newOnly                                         bool
	)
	cmd := &cobra.Command{
		Use:   "leads",
		Short: "Find companies that recently won construction contracts, with contact data for outreach",
		Long: `Use this command to get a contactable outreach list of companies that recently won contracts.
Do NOT use it to profile one known company's history; use 'winner' instead.
Do NOT use it for a plain award table without contacts; use 'awards' instead.

One row per winning company per award notice: company name, email, phone,
city, post code, NUTS region, VAT/HRB identifier, company size, the project
title, project location, contract value and the TED link. TED lists contacts
in organisation-name-tenderer order, which differs from the per-lot winner
list; this command matches them by company so each row carries the right
contact data. Values are the company's own lot values when TED reports them,
otherwise the notice total when the company is the notice's only winner,
else 0.

Reads the local store when it holds award notices (run sync first), otherwise
queries TED live (--data-source live forces the API).

TED titles describe what is built (Neubau, Rohbau, Brücke), not which
equipment is needed. Use project-type keywords that imply heavy machinery:
  Neubau, Rohbau, Hochbau, Stahlbeton, Brücke, Tunnel, Krankenhaus, Schulbau,
  Industriebau, Generalunternehmer

CPV codes by project type:
  45500000  Hire of construction machinery with operator (direct equipment buyers)
  45000000  All construction work (broadest net, default --cpv 45)
  45200000  Civil engineering (roads, bridges, infrastructure)
  45210000  Building construction (Hochbau, hospitals, schools)
  45230000  Pipelines, power lines, roads (wind/grid cabling)
  45310000  Electrical installation (covers PV/solar farm construction)

--group-by company folds rows into one per company with win count and total
value. --new-only skips companies returned by earlier --new-only runs and
records the ones it returns, for a weekly "only new leads" digest. Each
company is claimed atomically, so concurrent --new-only runs on one store
never return the same company twice.`,
		Example: strings.Trim(`
  eu-tenders-pp-cli leads --country DEU --days 7 --json
  eu-tenders-pp-cli leads --country DEU --cpv 45310000 --days 30 --json
  eu-tenders-pp-cli leads --country DEU --keywords "Neubau,Rohbau,Hochbau" --days 90
  eu-tenders-pp-cli leads --country DEU --cpv 45 --days 30 --region DE2 --group-by company
  eu-tenders-pp-cli leads --country DEU --cpv 45 --days 7 --new-only --agent --select winner_name,winner_email,winner_phone,contract_value,title`, "\n"),
		Annotations: map[string]string{
			"pp:data-source": "auto",
			// The happy path only reads TED and the sandbox store; without
			// --new-only it writes nothing, so live dogfood may run it for real.
			"pp:live-happy-path": "true",
			"pp:happy-args":      "--country=DEU;--days=14;--limit=10",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "find award-winner leads")
			}
			if days <= 0 {
				return usageErr(fmt.Errorf("--days must be positive"))
			}
			switch groupBy {
			case "", "none", "company":
			default:
				return usageErr(fmt.Errorf("invalid --group-by %q: use company or none", groupBy))
			}
			since := daysAgo(days, time.Now())
			rows, source, scanned, err := loadAwardWinners(cmd, flags, awardQuery{
				Country: country, CPV: cpv, Since: since, MaxScan: maxScan, DBPath: dbPath,
			})
			if err != nil {
				return err
			}
			leads := filterLeads(rows, leadFilter{Keywords: parseCSVList(keywords), MinValue: minValue, Region: region})

			skipped := 0
			if newOnly {
				leads, skipped, err = dropSeenLeads(cmd, resolveTendersDB(dbPath), leads)
				if err != nil {
					return err
				}
			}
			if source == sourceLive && !flags.quiet {
				fmt.Fprintf(cmd.ErrOrStderr(), "scanned %d live award notices since %s (raise --max-scan to widen)\n", scanned, since)
			}
			if skipped > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "skipped %d leads already returned by earlier --new-only runs\n", skipped)
			}

			// release gives back --new-only claims when the leads never reach
			// the output, so a failed digest does not hide them from later runs.
			release := func() {}
			deliver := func(err error) error {
				if err != nil {
					release()
				}
				return err
			}
			if groupBy == "company" {
				grouped := groupLeadsByCompany(leads)
				if newOnly {
					var lost int
					grouped, lost, release, err = claimUpTo(cmd, resolveTendersDB(dbPath), grouped, limit, func(c companyLead) store.LeadKey {
						return leadStoreKey(c.WinnerName, c.WinnerCountry)
					})
					if err != nil {
						return err
					}
					noteConcurrentClaims(cmd, lost)
				} else if limit > 0 && len(grouped) > limit {
					grouped = grouped[:limit]
				}
				if !wantsHumanTable(cmd.OutOrStdout(), flags) {
					return deliver(printJSONFiltered(cmd.OutOrStdout(), grouped, flags))
				}
				if len(grouped) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No matching award winners.")
					return nil
				}
				tw := newTabWriter(cmd.OutOrStdout())
				fmt.Fprintln(tw, "COMPANY\tCITY\tWINS\tTOTAL VALUE\tEMAIL\tPHONE\tLATEST")
				for _, c := range grouped {
					fmt.Fprintf(tw, "%s\t%s\t%d\t%.0f %s\t%s\t%s\t%s\n", truncate(c.WinnerName, 40), c.WinnerCity, c.Wins, c.TotalValue, c.Currency, c.WinnerEmail, c.WinnerPhone, c.LatestWin)
				}
				return deliver(tw.Flush())
			}
			if newOnly {
				var lost int
				leads, lost, release, err = claimUpTo(cmd, resolveTendersDB(dbPath), leads, limit, func(l leadRow) store.LeadKey {
					return leadStoreKey(l.WinnerName, l.WinnerCountry)
				})
				if err != nil {
					return err
				}
				noteConcurrentClaims(cmd, lost)
			} else if limit > 0 && len(leads) > limit {
				leads = leads[:limit]
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return deliver(printJSONFiltered(cmd.OutOrStdout(), leads, flags))
			}
			if len(leads) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No matching award winners. Widen --days, drop --keywords, or run sync.")
				return nil
			}
			tw := newTabWriter(cmd.OutOrStdout())
			fmt.Fprintln(tw, "DATE\tCOMPANY\tCITY\tVALUE\tEMAIL\tPHONE\tPROJECT")
			for _, l := range leads {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%.0f\t%s\t%s\t%s\n", l.PublishedDate, truncate(l.WinnerName, 36), l.WinnerCity, l.ContractValue, l.WinnerEmail, l.WinnerPhone, truncate(l.Title, 50))
			}
			return deliver(tw.Flush())
		},
	}
	cmd.Flags().StringVar(&country, "country", "", "Buyer country, 3-letter ISO code (e.g. DEU)")
	cmd.Flags().StringVar(&cpv, "cpv", "45", "CPV code or prefix (default 45 = all construction work)")
	cmd.Flags().IntVar(&days, "days", 90, "Look back N days for award notices")
	cmd.Flags().StringVar(&keywords, "keywords", "", "Comma-separated project-type keywords matched in the title, OR-combined (e.g. \"Neubau,Brücke\")")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum rows to return")
	cmd.Flags().Float64Var(&minValue, "min-value", 0, "Minimum contract value in EUR (0 also keeps notices without a reported value)")
	cmd.Flags().StringVar(&region, "region", "", "NUTS region prefix of the project location (e.g. DE2 for Bavaria, DE21 for Upper Bavaria)")
	cmd.Flags().StringVar(&groupBy, "group-by", "", "Set to company for one row per company with win count and total value")
	cmd.Flags().BoolVar(&newOnly, "new-only", false, "Skip companies returned by earlier --new-only runs and remember the ones returned now")
	cmd.Flags().IntVar(&maxScan, "max-scan", 500, "Live mode: maximum award notices to read from TED")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}

func leadStoreKey(name, country string) store.LeadKey {
	return store.LeadKey{NameKey: ted.NormalizeName(name), Country: strings.ToUpper(country)}
}

// dropSeenLeads removes companies recorded by earlier --new-only runs.
func dropSeenLeads(cmd *cobra.Command, dbPath string, leads []leadRow) ([]leadRow, int, error) {
	st, err := store.OpenWithContext(cmd.Context(), dbPath)
	if err != nil {
		return nil, 0, fmt.Errorf("opening local store for --new-only: %w", err)
	}
	defer st.Close()
	keys := make([]store.LeadKey, 0, len(leads))
	for _, l := range leads {
		keys = append(keys, leadStoreKey(l.WinnerName, l.WinnerCountry))
	}
	seen, err := st.SeenLeads(cmd.Context(), keys)
	if err != nil {
		return nil, 0, fmt.Errorf("reading lead seen-state: %w", err)
	}
	out := make([]leadRow, 0, len(leads))
	skipped := 0
	for i, l := range leads {
		if seen[keys[i]] {
			skipped++
			continue
		}
		out = append(out, l)
	}
	return out, skipped, nil
}

// claimUpTo walks the unseen candidates in order and claims their companies
// in batches until limit rows are kept (limit <= 0 keeps every candidate).
// Rows of a company another --new-only run claimed first are dropped and the
// freed slots are refilled from the next candidates, so concurrent digests
// split the companies without under-filling each other. Rows of a company
// this run already claimed are kept without a second claim.
func claimUpTo[T any](cmd *cobra.Command, dbPath string, candidates []T, limit int, key func(T) store.LeadKey) ([]T, int, func(), error) {
	st, err := store.OpenWithContext(cmd.Context(), dbPath)
	if err != nil {
		return nil, 0, func() {}, fmt.Errorf("opening local store for --new-only: %w", err)
	}
	defer st.Close()
	kept := make([]T, 0, len(candidates))
	mine := map[store.LeadKey]bool{}
	release := func() { releaseClaims(dbPath, mine) }
	lostKeys := map[store.LeadKey]bool{}
	next := 0
	for next < len(candidates) && (limit <= 0 || len(kept) < limit) {
		want := len(candidates) - next
		if limit > 0 && limit-len(kept) < want {
			want = limit - len(kept)
		}
		batch := candidates[next : next+want]
		next += want
		toClaim := make([]store.LeadKey, 0, len(batch))
		for _, c := range batch {
			if k := key(c); !mine[k] && !lostKeys[k] {
				toClaim = append(toClaim, k)
			}
		}
		claimed, err := st.ClaimLeads(cmd.Context(), toClaim)
		if err != nil {
			release()
			return nil, 0, func() {}, fmt.Errorf("recording lead seen-state: %w", err)
		}
		for _, k := range toClaim {
			if claimed[k] {
				mine[k] = true
			} else {
				lostKeys[k] = true
			}
		}
		for _, c := range batch {
			if mine[key(c)] && (limit <= 0 || len(kept) < limit) {
				kept = append(kept, c)
			}
		}
	}
	return kept, len(lostKeys), release, nil
}

// releaseClaims gives back every company this run claimed. It opens its own
// handle with a fresh context because the command context may already be
// cancelled when a digest fails.
func releaseClaims(dbPath string, mine map[store.LeadKey]bool) {
	if len(mine) == 0 {
		return
	}
	keys := make([]store.LeadKey, 0, len(mine))
	for k := range mine {
		keys = append(keys, k)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := store.OpenWithContext(ctx, dbPath)
	if err != nil {
		return
	}
	defer st.Close()
	_ = st.ReleaseLeads(ctx, keys)
}

func noteConcurrentClaims(cmd *cobra.Command, n int) {
	if n > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "dropped %d companies another --new-only run returned first\n", n)
	}
}
