// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

// Package ted turns raw TED Search API v3 notices into flat notice and
// winner records. TED returns eForms fields with several JSON shapes
// (scalar, array, map[lang]string, map[lang][]string), and the per-company
// contact arrays are index-aligned with organisation-name-tenderer while
// winner-name and tender-value are per-lot lists in a different order.
package ted

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SearchPath is the only public read endpoint of the TED Search API.
const SearchPath = "/v3/notices/search"

// PaginationIteration is TED's token-based pagination mode, which keeps
// paging stable while new notices are published.
const PaginationIteration = "ITERATION"

// PublicationQuery is the expert query selecting one notice by its
// publication number (e.g. 680471-2026).
func PublicationQuery(id string) string {
	return "publication-number=" + id
}

// Notice types used throughout the CLI.
const (
	NoticeTypeCall  = "cn-standard"
	NoticeTypeAward = "can-standard"
)

// SyncFields is the field list requested for every synced notice.
var SyncFields = []string{
	"publication-number",
	"notice-type",
	"publication-date",
	"buyer-name",
	"buyer-country",
	"buyer-city",
	"buyer-email",
	"classification-cpv",
	"estimated-value-proc",
	"estimated-value-cur-proc",
	"estimated-value-lot",
	"result-value-notice",
	"result-value-cur-notice",
	"total-value",
	"procedure-type",
	"deadline-receipt-tender-date-lot",
	"title-proc",
	"title-lot",
	"notice-title",
	"previous-notice-id-proc",
	"place-of-performance",
	"place-of-performance-city-proc",
	"winner-name",
	"tender-value",
	"organisation-name-tenderer",
	"organisation-country-tenderer",
	"winner-country",
	"winner-city",
	"winner-post-code",
	"winner-country-sub",
	"winner-email",
	"organisation-tel-tenderer",
	"winner-identifier",
	"winner-size",
}

// languagePreference orders languages when a multilingual field has several.
var languagePreference = []string{"eng", "deu", "fra", "nld", "mul"}

// Notice is one flattened TED notice.
type Notice struct {
	ID                 string   `json:"id"`
	NoticeType         string   `json:"notice_type"`
	PublicationDate    string   `json:"publication_date"`
	BuyerName          string   `json:"buyer_name"`
	BuyerCountry       string   `json:"buyer_country"`
	BuyerCity          string   `json:"buyer_city"`
	BuyerEmail         string   `json:"buyer_email"`
	CPVCode            string   `json:"cpv_code"`
	CPVCodes           []string `json:"cpv_codes"`
	EstimatedValue     float64  `json:"estimated_value"`
	ContractValue      float64  `json:"contract_value"`
	Currency           string   `json:"currency"`
	ProcedureType      string   `json:"procedure_type"`
	SubmissionDeadline string   `json:"submission_deadline"`
	Title              string   `json:"title"`
	PlaceOfPerformance string   `json:"place_of_performance"`
	PerformanceCity    string   `json:"performance_city"`
	PreviousNoticeID   string   `json:"previous_notice_id"`
	NoticeURL          string   `json:"notice_url"`
	Winners            []Winner `json:"winners"`
}

// Winner is one awarded company on a notice with its contact touchpoints.
type Winner struct {
	Name       string  `json:"name"`
	Country    string  `json:"country"`
	City       string  `json:"city"`
	PostCode   string  `json:"post_code"`
	NUTS       string  `json:"nuts"`
	Email      string  `json:"email"`
	Phone      string  `json:"phone"`
	Identifier string  `json:"identifier"`
	Size       string  `json:"size"`
	LotsWon    int     `json:"lots_won"`
	Value      float64 `json:"value"`
}

// NoticeURL returns the human TED page for a publication number.
func NoticeURL(id string) string {
	if id == "" {
		return ""
	}
	return "https://ted.europa.eu/en/notice/-/detail/" + id
}

// Extract flattens one raw TED notice.
func Extract(raw map[string]any) Notice {
	n := Notice{
		ID:              Text(raw["publication-number"]),
		NoticeType:      Text(raw["notice-type"]),
		PublicationDate: DateOnly(Text(raw["publication-date"])),
		BuyerName:       Text(raw["buyer-name"]),
		BuyerCountry:    Text(raw["buyer-country"]),
		BuyerCity:       Text(raw["buyer-city"]),
		BuyerEmail:      Text(raw["buyer-email"]),
		ProcedureType:   Text(raw["procedure-type"]),
		PerformanceCity: Text(raw["place-of-performance-city-proc"]),
		Currency:        "EUR",
	}
	n.CPVCodes = uniqueStrings(List(raw["classification-cpv"]))
	if len(n.CPVCodes) > 0 {
		n.CPVCode = n.CPVCodes[0]
	}
	n.EstimatedValue = Number(raw["estimated-value-proc"])
	if n.EstimatedValue == 0 {
		n.EstimatedValue = sumPositive(List(raw["estimated-value-lot"]))
	}
	n.ContractValue = Number(raw["result-value-notice"])
	if n.ContractValue == 0 {
		n.ContractValue = Number(raw["total-value"])
	}
	for _, f := range []string{"result-value-cur-notice", "estimated-value-cur-proc"} {
		if cur := Text(raw[f]); cur != "" {
			n.Currency = cur
			break
		}
	}
	n.SubmissionDeadline = earliestDate(List(raw["deadline-receipt-tender-date-lot"]))
	n.Title = ResolveTitle(raw)
	n.PlaceOfPerformance = firstNUTS(List(raw["place-of-performance"]))
	n.PreviousNoticeID = Text(raw["previous-notice-id-proc"])
	n.NoticeURL = NoticeURL(n.ID)
	n.Winners = ExtractWinners(raw)
	return n
}

// ResolveTitle prefers the procedure title, then the lot title, then the
// generated notice-title with its "Country – Category – " prefix removed.
// Many buyers fill title-lot with a lot reference code, so title-proc wins.
func ResolveTitle(raw map[string]any) string {
	for _, f := range []string{"title-proc", "title-lot"} {
		if t := strings.TrimSpace(Text(raw[f])); t != "" {
			return t
		}
	}
	t := strings.TrimSpace(Text(raw["notice-title"]))
	if parts := strings.SplitN(t, " – ", 3); len(parts) == 3 {
		return strings.TrimSpace(parts[2])
	}
	return t
}

// ExtractWinners zips the per-company arrays against organisation-name-tenderer
// and attributes per-lot values from winner-name/tender-value to each company.
func ExtractWinners(raw map[string]any) []Winner {
	orgs := List(raw["organisation-name-tenderer"])
	lotWinners := List(raw["winner-name"])
	lotValues := List(raw["tender-value"])

	if len(orgs) == 0 {
		orgs = uniqueStrings(lotWinners)
	}
	if len(orgs) == 0 {
		return []Winner{}
	}
	aligned := func(field string) []string {
		vals := List(raw[field])
		if len(vals) != len(orgs) {
			return nil
		}
		return vals
	}
	countries := aligned("organisation-country-tenderer")
	if countries == nil {
		countries = aligned("winner-country")
	}
	cities := aligned("winner-city")
	postCodes := aligned("winner-post-code")
	nuts := aligned("winner-country-sub")
	emails := aligned("winner-email")
	phones := aligned("organisation-tel-tenderer")
	ids := aligned("winner-identifier")
	sizes := aligned("winner-size")
	at := func(vals []string, i int) string {
		if vals == nil {
			return ""
		}
		return strings.TrimSpace(vals[i])
	}

	winnerSet := map[string]bool{}
	for _, w := range lotWinners {
		winnerSet[NormalizeName(w)] = true
	}
	valuesAligned := len(lotValues) == len(lotWinners)

	out := make([]Winner, 0, len(orgs))
	seen := map[string]bool{}
	for i, name := range orgs {
		name = strings.TrimSpace(name)
		key := NormalizeName(name)
		if name == "" || seen[key] {
			continue
		}
		if len(winnerSet) > 0 && !winnerSet[key] {
			continue
		}
		seen[key] = true
		w := Winner{
			Name:       name,
			Country:    at(countries, i),
			City:       at(cities, i),
			PostCode:   at(postCodes, i),
			NUTS:       at(nuts, i),
			Email:      at(emails, i),
			Phone:      at(phones, i),
			Identifier: at(ids, i),
			Size:       at(sizes, i),
		}
		for j, lw := range lotWinners {
			if NormalizeName(lw) != key {
				continue
			}
			w.LotsWon++
			if valuesAligned {
				if v := parseNumber(lotValues[j]); v > 0 {
					w.Value += v
				}
			}
		}
		if w.LotsWon == 0 && len(lotWinners) == 0 {
			w.LotsWon = 1
		}
		out = append(out, w)
	}
	if len(out) == 1 && out[0].Value == 0 {
		out[0].Value = Number(raw["result-value-notice"])
		if out[0].Value == 0 {
			out[0].Value = Number(raw["total-value"])
		}
	}
	return out
}

// NormalizeName folds case and whitespace so the same company matches across lists.
func NormalizeName(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// Text resolves any TED field shape to one string, preferring English.
func Text(v any) string {
	vals := List(v)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// List resolves any TED field shape to a list of strings. Language maps pick
// the preferred language, then the alphabetically first one.
func List(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case float64:
		return []string{strconv.FormatFloat(t, 'f', -1, 64)}
	case json.Number:
		return []string{t.String()}
	case bool:
		return []string{strconv.FormatBool(t)}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			out = append(out, List(item)...)
		}
		return out
	case map[string]any:
		for _, lang := range languagePreference {
			if lv, ok := t[lang]; ok {
				if vals := List(lv); len(vals) > 0 {
					return vals
				}
			}
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if vals := List(t[k]); len(vals) > 0 {
				return vals
			}
		}
	}
	return nil
}

// Number returns the first parseable value greater than zero in a field, or 0
// when none is.
func Number(v any) float64 {
	for _, s := range List(v) {
		if f := parseNumber(s); f > 0 {
			return f
		}
	}
	return 0
}

func parseNumber(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 0 {
		return 0
	}
	return f
}

func sumPositive(vals []string) float64 {
	var total float64
	for _, s := range vals {
		total += parseNumber(s)
	}
	return total
}

// DateOnly trims a TED date such as "2026-04-08+02:00" to "2026-04-08".
func DateOnly(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func earliestDate(vals []string) string {
	best := ""
	for _, v := range vals {
		d := DateOnly(v)
		if len(d) != 10 {
			continue
		}
		if best == "" || d < best {
			best = d
		}
	}
	return best
}

// firstNUTS returns the most specific NUTS code (longest) in place-of-performance.
func firstNUTS(vals []string) string {
	best := ""
	for _, v := range vals {
		if len(v) > len(best) {
			best = v
		}
	}
	return best
}

func uniqueStrings(vals []string) []string {
	out := make([]string, 0, len(vals))
	seen := map[string]bool{}
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// NormalizeCPV pads a CPV prefix such as "45" to the 8-digit code "45000000".
func NormalizeCPV(cpv string) string {
	cpv = strings.TrimSpace(cpv)
	if i := strings.Index(cpv, "-"); i >= 0 {
		cpv = cpv[:i]
	}
	if len(cpv) < 8 {
		return cpv + strings.Repeat("0", 8-len(cpv))
	}
	return cpv
}

// CPVPrefix strips trailing zeros so "45000000" matches every 45xxxxxx code.
func CPVPrefix(cpv string) string {
	p := strings.TrimRight(NormalizeCPV(cpv), "0")
	if len(p) < 2 {
		p = NormalizeCPV(cpv)[:2]
	}
	return p
}

// Filter describes a TED expert query built from CLI flags.
type Filter struct {
	Query       string
	Country     string
	CPV         string
	Since       string
	Until       string
	NoticeTypes []string
}

// BuildQuery renders a TED expert query, always sorted newest first.
func BuildQuery(f Filter) string {
	var parts []string
	if q := strings.TrimSpace(f.Query); q != "" {
		parts = append(parts, "("+q+")")
	}
	if f.Country != "" {
		parts = append(parts, "buyer-country="+strings.ToUpper(strings.TrimSpace(f.Country)))
	}
	if f.CPV != "" {
		parts = append(parts, "classification-cpv="+NormalizeCPV(f.CPV))
	}
	if len(f.NoticeTypes) == 1 {
		parts = append(parts, "notice-type="+f.NoticeTypes[0])
	} else if len(f.NoticeTypes) > 1 {
		parts = append(parts, "notice-type IN ("+strings.Join(f.NoticeTypes, " ")+")")
	}
	if f.Since != "" {
		parts = append(parts, "publication-date>="+strings.ReplaceAll(f.Since, "-", ""))
	}
	if f.Until != "" {
		parts = append(parts, "publication-date<="+strings.ReplaceAll(f.Until, "-", ""))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " AND ") + " SORT BY publication-date DESC"
}

// SearchRequest is the TED Search API request body.
type SearchRequest struct {
	Query              string   `json:"query"`
	Fields             []string `json:"fields"`
	Limit              int      `json:"limit"`
	PaginationMode     string   `json:"paginationMode,omitempty"`
	Scope              string   `json:"scope,omitempty"`
	Page               int      `json:"page,omitempty"`
	IterationNextToken string   `json:"iterationNextToken,omitempty"`
}

// SearchResponse is the subset of the TED Search API response the CLI reads.
type SearchResponse struct {
	Notices            []map[string]any `json:"notices"`
	TotalNoticeCount   int              `json:"totalNoticeCount"`
	IterationNextToken string           `json:"iterationNextToken"`
	TimedOut           bool             `json:"timedOut"`
}

// ParseSearchResponse decodes a raw search response body.
func ParseSearchResponse(data []byte) (SearchResponse, error) {
	var resp SearchResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return resp, fmt.Errorf("parse TED search response: %w", err)
	}
	return resp, nil
}

// PrimaryCPVMatches reports whether the notice's main CPV code falls under the
// filter prefix. TED's classification-cpv query also matches additional codes,
// so a fuel-card framework with one construction code would otherwise pass a
// construction filter; local queries match the main code the same way.
func PrimaryCPVMatches(n Notice, filter string) bool {
	if strings.TrimSpace(filter) == "" {
		return true
	}
	return strings.HasPrefix(NormalizeCPV(n.CPVCode), CPVPrefix(filter))
}
