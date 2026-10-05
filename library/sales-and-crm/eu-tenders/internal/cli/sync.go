// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// pp:data-source live

package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/store"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

func init() {
	registerNovelCommand(func(root *cobra.Command, flags *rootFlags) {
		addNovelCommandIfAbsent(root, newTendersSyncCmd(flags))
	})
}

// maxSyncPages bounds --max-pages to the pages --limit's own cap could
// fill, so the page cap never allows more notices than the notice cap.
const maxSyncPages = syncLimitCap / tedPageSize

type syncResult struct {
	Synced        int    `json:"synced"`
	Awards        int    `json:"awards"`
	Calls         int    `json:"calls"`
	Winners       int    `json:"winners"`
	TotalMatching int    `json:"total_matching"`
	Query         string `json:"query"`
	Since         string `json:"since"`
	DB            string `json:"db"`
	Truncated     bool   `json:"truncated"`
}

func newTendersSyncCmd(flags *rootFlags) *cobra.Command {
	var (
		country, cpv, query, since, until, noticeType, dbPath string
		params                                                []string
		limit, maxPages                                       int
	)
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync TED notices (calls for tender and awards) into the local SQLite store",
		Long: `Pull TED notices matching your filters into the local store so leads,
analytics, search and sql can run offline. Notices are fetched newest first
with ITERATION pagination; winners are stored one row per company with the
contact fields TED publishes (email, phone, city, VAT/HRB id, size).

Filters come from flags or from --param key=value (keys: country, cpv, type,
query). --since accepts a duration (30d, 2w) or a date (YYYY-MM-DD).`,
		Example: strings.Trim(`
  eu-tenders-pp-cli sync --since 30d --param country=DEU --param cpv=45
  eu-tenders-pp-cli sync --country DEU --cpv 45 --since 90d --type award
  eu-tenders-pp-cli sync --query "buyer-name~Berlin" --since 2026-01-01 --limit 2000`, "\n"),
		Annotations: map[string]string{
			"pp:data-source": "live",
			"pp:happy-args":  "--since=7d;--param=country=LUX",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "sync TED notices")
			}
			if activeSource(flags) == sourceLocal {
				return usageErr(fmt.Errorf("sync always reads the live TED API; --data-source local has no local source"))
			}
			for _, p := range params {
				k, v, ok := strings.Cut(p, "=")
				if !ok {
					return usageErr(fmt.Errorf("--param %q must be key=value", p))
				}
				switch strings.ToLower(strings.TrimSpace(k)) {
				case "country", "buyer-country":
					country = v
				case "cpv", "classification-cpv":
					cpv = v
				case "type", "notice-type":
					noticeType = v
				case "query":
					query = v
				default:
					return usageErr(fmt.Errorf("unknown --param key %q (use country, cpv, type, query)", k))
				}
			}
			if err := validateTEDFilters(country, cpv); err != nil {
				return err
			}
			if limit < 0 || maxPages < 0 {
				return usageErr(fmt.Errorf("--limit and --max-pages must be 0 (no cap) or positive"))
			}
			if limit > syncLimitCap {
				limit = syncLimitCap
			}
			if maxPages > maxSyncPages {
				maxPages = maxSyncPages
			}
			types, err := parseNoticeTypeFlag(noticeType)
			if err != nil {
				return usageErr(err)
			}
			now := time.Now()
			sinceDate, err := resolveSinceDate(since, now)
			if err != nil {
				return usageErr(err)
			}
			untilDate, err := resolveSinceDate(until, now)
			if err != nil {
				return usageErr(err)
			}
			q := ted.BuildQuery(ted.Filter{Query: query, Country: country, CPV: cpv, Since: sinceDate, Until: untilDate, NoticeTypes: types})
			if q == "" {
				return usageErr(fmt.Errorf("sync needs at least one filter: --since, --country, --cpv or --query"))
			}
			pageSize := tedPageSize
			if cliutil.IsDogfoodEnv() {
				pageSize, maxPages = 50, 1
			}
			ctx, cancel := boundCtx(cmd.Context(), flags)
			defer cancel()
			dbPath = resolveTendersDB(dbPath)
			st, err := store.OpenWithContext(ctx, dbPath)
			if err != nil {
				return fmt.Errorf("opening local store %s: %w", dbPath, err)
			}
			defer st.Close()

			res := syncResult{Query: q, Since: sinceDate, DB: dbPath}
			total, truncated, err := pageTED(ctx, flags, q, ted.SyncFields, pageSize, limit, maxPages, func(resp ted.SearchResponse) error {
				notices := make([]ted.Notice, 0, len(resp.Notices))
				for _, raw := range resp.Notices {
					n := ted.Extract(raw)
					notices = append(notices, n)
					switch n.NoticeType {
					case ted.NoticeTypeAward:
						res.Awards++
					case ted.NoticeTypeCall:
						res.Calls++
					}
					res.Winners += len(n.Winners)
				}
				// Each page is committed as it arrives, so a timeout keeps
				// everything synced so far.
				if err := st.UpsertNotices(ctx, notices, resp.Notices); err != nil {
					return err
				}
				res.Synced += len(notices)
				if !flags.quiet && !flags.agent {
					fmt.Fprintf(cmd.ErrOrStderr(), "synced %d/%d notices\n", res.Synced, resp.TotalNoticeCount)
				}
				return nil
			})
			res.TotalMatching, res.Truncated = total, truncated
			if err != nil {
				return err
			}
			if err := st.SetTEDSyncState(ctx, "last_sync", time.Now().UTC().Format(time.RFC3339)); err != nil {
				return fmt.Errorf("recording sync state: %w", err)
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), res, flags)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Synced %d notices (%d awards, %d calls, %d winners) of %d matching -> %s\n",
				res.Synced, res.Awards, res.Calls, res.Winners, res.TotalMatching, dbPath)
			if res.Truncated {
				fmt.Fprintln(cmd.OutOrStdout(), "More notices match; raise --limit or --max-pages to sync the rest.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&country, "country", "", "Buyer country, 3-letter ISO code (e.g. DEU, FRA, POL)")
	cmd.Flags().StringVar(&cpv, "cpv", "", "CPV code or prefix (e.g. 45 for all construction, 45210000 for building works)")
	cmd.Flags().StringVar(&query, "query", "", "Extra TED expert-query clause ANDed with the other filters (e.g. buyer-name~Berlin)")
	cmd.Flags().StringVar(&since, "since", "30d", "Only notices published since this date (YYYY-MM-DD) or duration (30d, 2w)")
	cmd.Flags().StringVar(&until, "until", "", "Only notices published up to this date (YYYY-MM-DD) or duration ago")
	cmd.Flags().StringVar(&noticeType, "type", "all", "Notice type: award (can-standard), call (cn-standard) or all")
	cmd.Flags().StringArrayVar(&params, "param", nil, "Filter as key=value (keys: country, cpv, type, query); repeatable")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum notices to sync (0 = every matching notice; capped at 100000)")
	cmd.Flags().IntVar(&maxPages, "max-pages", 0, fmt.Sprintf("Maximum %d-notice pages to fetch (0 = no page cap; capped at %d)", tedPageSize, maxSyncPages))
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}

// parseNoticeTypeFlag maps award/call/all (or raw TED notice types) to TED values.
func parseNoticeTypeFlag(s string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "all":
		return []string{ted.NoticeTypeCall, ted.NoticeTypeAward}, nil
	case "award", "awards", "can", ted.NoticeTypeAward:
		return []string{ted.NoticeTypeAward}, nil
	case "call", "calls", "cn", "tender", ted.NoticeTypeCall:
		return []string{ted.NoticeTypeCall}, nil
	}
	return nil, fmt.Errorf("invalid --type %q: use award, call or all", s)
}
