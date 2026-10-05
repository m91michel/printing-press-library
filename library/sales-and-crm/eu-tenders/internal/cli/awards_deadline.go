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

func init() {
	registerNovelCommand(func(root *cobra.Command, flags *rootFlags) {
		addNovelCommandIfAbsent(root, newAwardsCmd(flags))
		addNovelCommandIfAbsent(root, newDeadlineCmd(flags))
	})
}

type awardRow struct {
	NoticeID      string  `json:"notice_id"`
	PublishedDate string  `json:"published_date"`
	WinnerName    string  `json:"winner_name"`
	WinnerCountry string  `json:"winner_country"`
	WinnerValue   float64 `json:"winner_value"`
	NoticeValue   float64 `json:"notice_value"`
	WinnerCount   int     `json:"winner_count"`
	Currency      string  `json:"currency"`
	BuyerName     string  `json:"buyer_name"`
	BuyerCountry  string  `json:"buyer_country"`
	CPVCode       string  `json:"cpv_code"`
	Title         string  `json:"title"`
	TEDURL        string  `json:"ted_url"`
}

func newAwardsCmd(flags *rootFlags) *cobra.Command {
	var country, cpv, year, winner, since, dbPath string
	var limit, maxScan int
	cmd := &cobra.Command{
		Use:   "awards",
		Short: "List contract award notices with winner, buyer and value",
		Long: `List contract award notices (can-standard), one row per winning company.
Filter by buyer country, CPV prefix, publication year and winner name.
Reads the local store when it holds awards, otherwise queries TED live.
For outreach rows with contact data use 'leads' instead.`,
		Example: strings.Trim(`
  eu-tenders-pp-cli awards --country DEU --cpv 72000000
  eu-tenders-pp-cli awards --year 2026 --winner "Strabag" --json
  eu-tenders-pp-cli awards --country POL --since 30d --csv`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "auto",
			"pp:happy-args":  "--country=DEU;--since=14d;--limit=10",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "list award notices")
			}
			q := awardQuery{Country: country, CPV: cpv, Winner: winner, MaxScan: maxScan, DBPath: dbPath}
			if year != "" {
				if _, err := time.Parse("2006", year); err != nil {
					return usageErr(fmt.Errorf("invalid --year %q: use a 4-digit year", year))
				}
				q.Since, q.Until = year+"-01-01", year+"-12-31"
			}
			if since != "" {
				d, err := resolveSinceDate(since, time.Now())
				if err != nil {
					return usageErr(err)
				}
				q.Since = d
			}
			if q.Since == "" {
				q.Since = daysAgo(90, time.Now())
			}
			rows, _, _, err := loadAwardWinners(cmd, flags, q)
			if err != nil {
				return err
			}
			out := make([]awardRow, 0, len(rows))
			for _, r := range rows {
				out = append(out, awardRow{
					NoticeID: r.NoticeID, PublishedDate: r.PublishedDate, WinnerName: r.Winner.Name,
					WinnerCountry: r.Winner.Country, WinnerValue: round2(r.Winner.Value), NoticeValue: round2(r.NoticeValue), WinnerCount: r.WinnerCount,
					Currency: r.Currency, BuyerName: r.BuyerName, BuyerCountry: r.BuyerCountry,
					CPVCode: r.CPVCode, Title: r.Title, TEDURL: r.NoticeURL,
				})
			}
			if limit > 0 && len(out) > limit {
				out = out[:limit]
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), out, flags)
			}
			if len(out) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No matching award notices.")
				return nil
			}
			tw := newTabWriter(cmd.OutOrStdout())
			fmt.Fprintln(tw, "DATE\tWINNER\tVALUE\tBUYER\tTITLE")
			for _, a := range out {
				// The notice total is this winner's value only when it won alone.
				v := a.WinnerValue
				if v == 0 && a.WinnerCount == 1 {
					v = a.NoticeValue
				}
				fmt.Fprintf(tw, "%s\t%s\t%.0f\t%s\t%s\n", a.PublishedDate, truncate(a.WinnerName, 36), v, truncate(a.BuyerName, 36), truncate(a.Title, 50))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&country, "country", "", "Buyer country, 3-letter ISO code (e.g. DEU)")
	cmd.Flags().StringVar(&cpv, "cpv", "", "CPV code or prefix (e.g. 72 for IT services, 45 for construction)")
	cmd.Flags().StringVar(&year, "year", "", "Publication year (e.g. 2026)")
	cmd.Flags().StringVar(&since, "since", "", "Published since this date (YYYY-MM-DD) or duration (30d); default 90 days")
	cmd.Flags().StringVar(&winner, "winner", "", "Winner name contains this text (case-insensitive)")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum rows to return")
	cmd.Flags().IntVar(&maxScan, "max-scan", 500, "Live mode: maximum award notices to read from TED")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}

type deadlineRow struct {
	NoticeID           string  `json:"notice_id"`
	SubmissionDeadline string  `json:"submission_deadline"`
	DaysLeft           int     `json:"days_left"`
	Title              string  `json:"title"`
	BuyerName          string  `json:"buyer_name"`
	BuyerCountry       string  `json:"buyer_country"`
	EstimatedValue     float64 `json:"estimated_value"`
	Currency           string  `json:"currency"`
	CPVCode            string  `json:"cpv_code"`
	ProcedureType      string  `json:"procedure_type"`
	Location           string  `json:"location"`
	TEDURL             string  `json:"ted_url"`
}

// loadOpenCalls returns calls for tender whose deadline falls in [today, today+days].
// Auto mode reads the store when it holds calls matching the filters and
// queries TED otherwise.
func loadOpenCalls(cmd *cobra.Command, flags *rootFlags, country, cpv string, days int, dbPath string, maxScan int) ([]ted.Notice, error) {
	if err := validateTEDFilters(country, cpv); err != nil {
		return nil, err
	}
	ctx, cancel := boundCtx(cmd.Context(), flags)
	defer cancel()
	today := time.Now().UTC().Format("2006-01-02")
	end := time.Now().UTC().AddDate(0, 0, days).Format("2006-01-02")
	if activeSource(flags) != sourceLive {
		localOnly := activeSource(flags) == sourceLocal
		st, synced, err := openTendersForRead(ctx, cmd.ErrOrStderr(), resolveTendersDB(dbPath))
		if err != nil {
			return nil, err
		}
		n := 0
		if synced {
			defer st.Close()
			if n, err = st.NoticeCount(ctx, ted.NoticeTypeCall); err != nil {
				return nil, err
			}
		}
		if n > 0 {
			out, err := localOpenCalls(ctx, st, country, cpv, today, end)
			if err != nil {
				return nil, err
			}
			// A store synced for other filters must not hide TED matches
			// in auto mode.
			if len(out) > 0 || localOnly {
				recordSource(flags, sourceLocal)
				return out, nil
			}
			if !flags.quiet {
				fmt.Fprintln(cmd.ErrOrStderr(), "no local matches; querying TED live")
			}
		} else if localOnly {
			recordSource(flags, sourceLocal)
			fmt.Fprintf(cmd.ErrOrStderr(), "hint: the local store has no calls for tender; run %s sync --type call\n", tendersCLIName)
			return []ted.Notice{}, nil
		}
	}
	recordSource(flags, sourceLive)
	maxScan = capMaxScan(maxScan)
	extra := fmt.Sprintf("deadline-receipt-tender-date-lot>=%s AND deadline-receipt-tender-date-lot<=%s",
		strings.ReplaceAll(today, "-", ""), strings.ReplaceAll(end, "-", ""))
	query := ted.BuildQuery(ted.Filter{Query: extra, Country: country, CPV: cpv, NoticeTypes: []string{ted.NoticeTypeCall}})
	raws, _, err := tedSearchNotices(ctx, flags, query, ted.SyncFields, maxScan)
	if err != nil {
		return nil, err
	}
	out := make([]ted.Notice, 0, len(raws))
	for _, raw := range raws {
		n := ted.Extract(raw)
		if !ted.PrimaryCPVMatches(n, cpv) {
			continue
		}
		if n.SubmissionDeadline >= today && n.SubmissionDeadline <= end {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].SubmissionDeadline < out[b].SubmissionDeadline })
	return out, nil
}

// localOpenCalls reads stored calls for tender whose deadline is in [today, end].
func localOpenCalls(ctx context.Context, st *store.Store, country, cpv, today, end string) ([]ted.Notice, error) {
	f := &noticeFilterSQL{}
	f.add("notice_type = ?", ted.NoticeTypeCall)
	f.add("submission_deadline >= ?", today)
	f.add("submission_deadline <= ?", end)
	f.country("buyer_country", country)
	f.cpv("cpv_code", cpv)
	rows, err := st.DB().QueryContext(ctx, `SELECT id, publication_date, title, buyer_name, buyer_country,
		estimated_value, currency, cpv_code, procedure_type, submission_deadline,
		place_of_performance, performance_city, notice_url FROM notices`+f.where()+` ORDER BY submission_deadline`, f.args...)
	if err != nil {
		return nil, fmt.Errorf("querying open calls: %w", err)
	}
	out := make([]ted.Notice, 0)
	for rows.Next() {
		var c ted.Notice
		if err := rows.Scan(&c.ID, &c.PublicationDate, &c.Title, &c.BuyerName, &c.BuyerCountry, &c.EstimatedValue,
			&c.Currency, &c.CPVCode, &c.ProcedureType, &c.SubmissionDeadline, &c.PlaceOfPerformance, &c.PerformanceCity, &c.NoticeURL); err != nil {
			_ = rows.Close()
			return nil, err
		}
		c.NoticeType = ted.NoticeTypeCall
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	return out, rows.Close()
}

// daysUntil counts whole days from today to a YYYY-MM-DD date.
func daysUntil(date string, now time.Time) int {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0
	}
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return int(d.Sub(today).Hours() / 24)
}

func newDeadlineCmd(flags *rootFlags) *cobra.Command {
	var country, cpv, dbPath string
	var days, limit, maxScan int
	var minValue float64
	cmd := &cobra.Command{
		Use:   "deadline",
		Short: "List open calls for tender whose submission deadline is within N days",
		Long: `List open calls for tender (cn-standard) sorted by closest deadline.
Default (auto) reads the local store when it holds calls, otherwise queries
TED live; --data-source live always queries TED without a sync.`,
		Example: strings.Trim(`
  eu-tenders-pp-cli deadline --country DEU --cpv 45500000 --data-source live
  eu-tenders-pp-cli deadline --days 14 --min-value 100000 --json
  eu-tenders-pp-cli deadline --country DEU --cpv 72000000`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "auto",
			"pp:happy-args":  "--country=DEU;--days=14;--limit=10",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "list open tender deadlines")
			}
			if days <= 0 {
				return usageErr(fmt.Errorf("--days must be positive"))
			}
			calls, err := loadOpenCalls(cmd, flags, country, cpv, days, dbPath, maxScan)
			if err != nil {
				return err
			}
			now := time.Now()
			out := make([]deadlineRow, 0, len(calls))
			for _, c := range calls {
				if minValue > 0 && c.EstimatedValue < minValue {
					continue
				}
				out = append(out, deadlineRow{
					NoticeID: c.ID, SubmissionDeadline: c.SubmissionDeadline, DaysLeft: daysUntil(c.SubmissionDeadline, now),
					Title: c.Title, BuyerName: c.BuyerName, BuyerCountry: c.BuyerCountry, EstimatedValue: round2(c.EstimatedValue),
					Currency: c.Currency, CPVCode: c.CPVCode, ProcedureType: c.ProcedureType,
					Location: strings.TrimSpace(c.PerformanceCity + " " + c.PlaceOfPerformance), TEDURL: c.NoticeURL,
				})
			}
			if limit > 0 && len(out) > limit {
				out = out[:limit]
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), out, flags)
			}
			if len(out) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No open tenders close in that window.")
				return nil
			}
			tw := newTabWriter(cmd.OutOrStdout())
			fmt.Fprintln(tw, "DEADLINE\tDAYS\tVALUE\tBUYER\tTITLE")
			for _, r := range out {
				fmt.Fprintf(tw, "%s\t%d\t%.0f\t%s\t%s\n", r.SubmissionDeadline, r.DaysLeft, r.EstimatedValue, truncate(r.BuyerName, 36), truncate(r.Title, 55))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&country, "country", "", "Buyer country, 3-letter ISO code (e.g. DEU)")
	cmd.Flags().StringVar(&cpv, "cpv", "", "CPV code or prefix (e.g. 45500000 for machinery hire)")
	cmd.Flags().IntVar(&days, "days", 30, "Deadlines within the next N days")
	cmd.Flags().Float64Var(&minValue, "min-value", 0, "Minimum estimated value in EUR")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum rows to return")
	cmd.Flags().IntVar(&maxScan, "max-scan", 500, "Live mode: maximum notices to read from TED")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}
