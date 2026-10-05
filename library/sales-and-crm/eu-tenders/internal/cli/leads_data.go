// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

// awardWinnerRow is one awarded company on one award notice.
type awardWinnerRow struct {
	NoticeID        string
	PublishedDate   string
	Title           string
	CPVCode         string
	BuyerName       string
	BuyerCountry    string
	BuyerCity       string
	PlaceNUTS       string
	PerformanceCity string
	NoticeValue     float64
	Currency        string
	NoticeURL       string
	// WinnerCount is the number of winners on the notice. The notice total
	// stands in for a missing winner value only when it is 1; otherwise each
	// winner would claim the whole notice.
	WinnerCount int
	Winner      ted.Winner
}

type awardQuery struct {
	Country string
	CPV     string
	Since   string
	Until   string
	Winner  string
	MaxScan int
	DBPath  string
}

// loadAwardWinners returns award-winner rows from the local store or, when the
// store has no awards or none matching q (auto) or --data-source live is set,
// from the TED API. A store synced for other filters must not hide TED
// matches in auto mode. The returned source is local or live; scanned counts
// live notices read.
func loadAwardWinners(cmd *cobra.Command, flags *rootFlags, q awardQuery) ([]awardWinnerRow, dataSource, int, error) {
	if err := validateTEDFilters(q.Country, q.CPV); err != nil {
		return nil, "", 0, err
	}
	ctx, cancel := boundCtx(cmd.Context(), flags)
	defer cancel()
	if activeSource(flags) != sourceLive {
		rows, ok, err := localAwardWinners(ctx, cmd.ErrOrStderr(), q)
		if err != nil {
			return nil, "", 0, err
		}
		if (ok && len(rows) > 0) || activeSource(flags) == sourceLocal {
			if !ok {
				fmt.Fprintf(cmd.ErrOrStderr(), "hint: the local store has no award notices; run %s sync --since 90d --param country=DEU --param cpv=45\n", tendersCLIName)
			}
			recordSource(flags, sourceLocal)
			return rows, sourceLocal, 0, nil
		}
		if ok && !flags.quiet {
			fmt.Fprintln(cmd.ErrOrStderr(), "no local matches; querying TED live")
		}
	}
	recordSource(flags, sourceLive)
	maxScan := capMaxScan(q.MaxScan)
	query := ted.BuildQuery(ted.Filter{
		Country: q.Country, CPV: q.CPV, Since: q.Since, Until: q.Until,
		NoticeTypes: []string{ted.NoticeTypeAward},
	})
	raws, _, err := tedSearchNotices(ctx, flags, query, ted.SyncFields, maxScan)
	if err != nil {
		return nil, "", 0, err
	}
	out := make([]awardWinnerRow, 0)
	winnerTerm := ted.NormalizeName(q.Winner)
	for _, raw := range raws {
		n := ted.Extract(raw)
		if !ted.PrimaryCPVMatches(n, q.CPV) {
			continue
		}
		for _, w := range n.Winners {
			if winnerTerm != "" && !strings.Contains(ted.NormalizeName(w.Name), winnerTerm) {
				continue
			}
			out = append(out, awardWinnerRow{
				NoticeID: n.ID, PublishedDate: n.PublicationDate, Title: n.Title, CPVCode: n.CPVCode,
				BuyerName: n.BuyerName, BuyerCountry: n.BuyerCountry, BuyerCity: n.BuyerCity,
				PlaceNUTS: n.PlaceOfPerformance, PerformanceCity: n.PerformanceCity,
				NoticeValue: n.ContractValue, Currency: n.Currency, NoticeURL: n.NoticeURL,
				WinnerCount: len(n.Winners), Winner: w,
			})
		}
	}
	return out, sourceLive, len(raws), nil
}

// localAwardWinners reads matching award winners from the store. ok reports
// that the store holds award notices at all, whether or not any match q.
func localAwardWinners(ctx context.Context, stderr io.Writer, q awardQuery) ([]awardWinnerRow, bool, error) {
	st, synced, err := openTendersForRead(ctx, stderr, resolveTendersDB(q.DBPath))
	if err != nil || !synced {
		return []awardWinnerRow{}, false, err
	}
	defer st.Close()
	if n, err := st.NoticeCount(ctx, ted.NoticeTypeAward); err != nil || n == 0 {
		return []awardWinnerRow{}, false, err
	}
	f := &noticeFilterSQL{}
	f.add("n.notice_type = ?", ted.NoticeTypeAward)
	f.country("n.buyer_country", q.Country)
	f.cpv("n.cpv_code", q.CPV)
	f.since("n.publication_date", q.Since)
	if q.Until != "" {
		f.add("n.publication_date <= ?", q.Until)
	}
	if q.Winner != "" {
		f.add(`w.name_key LIKE ? ESCAPE '\'`, likeSubstring(ted.NormalizeName(q.Winner)))
	}
	rows, err := st.DB().QueryContext(ctx, `SELECT n.id, n.publication_date, n.title, n.cpv_code, n.buyer_name,
		n.buyer_country, n.buyer_city, n.place_of_performance, n.performance_city, n.contract_value, n.currency, n.notice_url,
		n.winner_count, w.name, w.country, w.city, w.post_code, w.nuts, w.email, w.phone, w.identifier, w.size, w.lots_won, w.value
		FROM notice_winners w JOIN notices n ON n.id = w.notice_id`+f.where()+`
		ORDER BY n.publication_date DESC, n.id`, f.args...)
	if err != nil {
		return nil, false, fmt.Errorf("querying award winners: %w", err)
	}
	out := make([]awardWinnerRow, 0)
	for rows.Next() {
		var r awardWinnerRow
		w := &r.Winner
		if err := rows.Scan(&r.NoticeID, &r.PublishedDate, &r.Title, &r.CPVCode, &r.BuyerName, &r.BuyerCountry,
			&r.BuyerCity, &r.PlaceNUTS, &r.PerformanceCity, &r.NoticeValue, &r.Currency, &r.NoticeURL,
			&r.WinnerCount, &w.Name, &w.Country, &w.City, &w.PostCode, &w.NUTS, &w.Email, &w.Phone, &w.Identifier, &w.Size, &w.LotsWon, &w.Value); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, false, err
	}
	return out, true, rows.Close()
}

// leadRow is one outreach row: a company that won a contract.
type leadRow struct {
	WinnerName       string  `json:"winner_name"`
	WinnerCountry    string  `json:"winner_country"`
	WinnerCity       string  `json:"winner_city"`
	WinnerPostCode   string  `json:"winner_post_code"`
	WinnerNUTS       string  `json:"winner_nuts"`
	WinnerEmail      string  `json:"winner_email"`
	WinnerPhone      string  `json:"winner_phone"`
	WinnerIdentifier string  `json:"winner_identifier"`
	WinnerSize       string  `json:"winner_size"`
	Title            string  `json:"title"`
	CPVDescription   string  `json:"cpv_description"`
	ContractValue    float64 `json:"contract_value"`
	Currency         string  `json:"currency"`
	Location         string  `json:"location"`
	PublishedDate    string  `json:"published_date"`
	CPVCode          string  `json:"cpv_code"`
	BuyerName        string  `json:"buyer_name"`
	BuyerCountry     string  `json:"buyer_country"`
	TEDURL           string  `json:"ted_url"`
	NoticeID         string  `json:"notice_id"`
	LotsWon          int     `json:"lots_won"`
}

// companyLead aggregates leads per company for --group-by company.
type companyLead struct {
	WinnerName       string   `json:"winner_name"`
	WinnerCountry    string   `json:"winner_country"`
	WinnerCity       string   `json:"winner_city"`
	WinnerEmail      string   `json:"winner_email"`
	WinnerPhone      string   `json:"winner_phone"`
	WinnerIdentifier string   `json:"winner_identifier"`
	WinnerSize       string   `json:"winner_size"`
	Wins             int      `json:"wins"`
	TotalValue       float64  `json:"total_value"`
	Currency         string   `json:"currency"`
	LatestWin        string   `json:"latest_win"`
	Locations        []string `json:"locations"`
	Projects         []string `json:"projects"`
	Buyers           int      `json:"buyers"`
	TEDURLs          []string `json:"ted_urls"`
}

func toLeadRow(r awardWinnerRow) leadRow {
	value := r.Winner.Value
	if value == 0 && r.WinnerCount == 1 {
		value = r.NoticeValue
	}
	loc := strings.TrimSpace(strings.Join(nonEmpty(r.PerformanceCity, r.PlaceNUTS), " "))
	if loc == "" {
		loc = strings.TrimSpace(strings.Join(nonEmpty(r.BuyerCity, r.BuyerCountry), " "))
	}
	return leadRow{
		WinnerName: r.Winner.Name, WinnerCountry: r.Winner.Country, WinnerCity: r.Winner.City,
		WinnerPostCode: r.Winner.PostCode, WinnerNUTS: r.Winner.NUTS, WinnerEmail: r.Winner.Email,
		WinnerPhone: r.Winner.Phone, WinnerIdentifier: r.Winner.Identifier, WinnerSize: r.Winner.Size,
		Title: r.Title, CPVDescription: cpvDescription(r.CPVCode), ContractValue: round2(value),
		Currency: r.Currency, Location: loc, PublishedDate: r.PublishedDate, CPVCode: r.CPVCode,
		BuyerName: r.BuyerName, BuyerCountry: r.BuyerCountry, TEDURL: r.NoticeURL, NoticeID: r.NoticeID,
		LotsWon: r.Winner.LotsWon,
	}
}

func nonEmpty(vals ...string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

type leadFilter struct {
	Keywords []string
	MinValue float64
	Region   string
}

// filterLeads applies keyword (title, OR), minimum value and NUTS region
// filters. A minimum of 0 keeps leads whose value TED did not report.
func filterLeads(rows []awardWinnerRow, f leadFilter) []leadRow {
	region := strings.ToUpper(strings.TrimSpace(f.Region))
	out := make([]leadRow, 0, len(rows))
	for _, r := range rows {
		if !containsAny(r.Title, f.Keywords) {
			continue
		}
		lead := toLeadRow(r)
		if f.MinValue > 0 && lead.ContractValue < f.MinValue {
			continue
		}
		if region != "" {
			nuts := r.PlaceNUTS
			if nuts == "" || len(nuts) <= 3 {
				nuts = r.Winner.NUTS
			}
			if !strings.HasPrefix(strings.ToUpper(nuts), region) {
				continue
			}
		}
		out = append(out, lead)
	}
	return out
}

// groupLeadsByCompany folds leads into one row per company (name + country),
// ordered by total awarded value.
func groupLeadsByCompany(leads []leadRow) []companyLead {
	idx := map[string]int{}
	buyers := map[string]map[string]bool{}
	out := make([]companyLead, 0)
	for _, l := range leads {
		k := companyKey(l.WinnerName, l.WinnerCountry)
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			buyers[k] = map[string]bool{}
			out = append(out, companyLead{WinnerName: l.WinnerName, WinnerCountry: l.WinnerCountry, Currency: l.Currency,
				Locations: []string{}, Projects: []string{}, TEDURLs: []string{}})
		}
		c := &out[i]
		c.Wins++
		c.TotalValue = round2(c.TotalValue + l.ContractValue)
		newer := l.PublishedDate >= c.LatestWin
		if newer {
			c.LatestWin = l.PublishedDate
		}
		mergeContacts(c, l, newer)
		c.Locations = appendUnique(c.Locations, l.Location, 5)
		c.Projects = appendUnique(c.Projects, l.Title, 5)
		c.TEDURLs = appendUnique(c.TEDURLs, l.TEDURL, 10)
		buyers[k][l.BuyerName] = true
		c.Buyers = len(buyers[k])
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].TotalValue != out[b].TotalValue {
			return out[a].TotalValue > out[b].TotalValue
		}
		return out[a].Wins > out[b].Wins
	})
	return out
}

// mergeContacts fills dst's contact fields from l. With preferNew, l's
// non-empty values replace dst's (l is the latest win); otherwise l only
// fills fields dst is missing.
func mergeContacts(dst *companyLead, l leadRow, preferNew bool) {
	pick := func(cur, next string) string {
		if preferNew {
			return firstNonEmpty(next, cur)
		}
		return firstNonEmpty(cur, next)
	}
	dst.WinnerCity = pick(dst.WinnerCity, l.WinnerCity)
	dst.WinnerEmail = pick(dst.WinnerEmail, l.WinnerEmail)
	dst.WinnerPhone = pick(dst.WinnerPhone, l.WinnerPhone)
	dst.WinnerIdentifier = pick(dst.WinnerIdentifier, l.WinnerIdentifier)
	dst.WinnerSize = pick(dst.WinnerSize, l.WinnerSize)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func appendUnique(list []string, v string, max int) []string {
	if strings.TrimSpace(v) == "" || len(list) >= max {
		return list
	}
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}
