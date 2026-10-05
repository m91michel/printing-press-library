// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.

package cli

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/cliutil"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/store"
	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

const tendersCLIName = "eu-tenders-pp-cli"

// dataSource is a --data-source value.
type dataSource string

const (
	sourceAuto  dataSource = "auto"
	sourceLive  dataSource = "live"
	sourceLocal dataSource = "local"
)

func activeSource(flags *rootFlags) dataSource { return dataSource(flags.dataSource) }

// recordSource stamps the branch an auto-routed command actually used, so
// --agent output reports local or live per call. The root hook can only
// guess from --data-source before RunE decides.
func recordSource(flags *rootFlags, src dataSource) { flags.agentSource = string(src) }

const (
	// dogfoodScanCap keeps live scans inside the dogfood per-command timeout.
	dogfoodScanCap = 50
	// maxScanHardCap bounds one live scan so a typo cannot page TED for hours.
	maxScanHardCap = 10000
	// syncLimitCap bounds --limit; 0 still means every matching notice,
	// which --timeout bounds instead.
	syncLimitCap = 100000
	tedPageSize  = 250
)

// capMaxScan clamps a live-scan size. n <= 0 is left for the caller's
// default, except under dogfood where every scan is bounded.
func capMaxScan(n int) int {
	if cliutil.IsDogfoodEnv() && (n <= 0 || n > dogfoodScanCap) {
		return dogfoodScanCap
	}
	if n > maxScanHardCap {
		return maxScanHardCap
	}
	return n
}

var (
	countryFlagRE = regexp.MustCompile(`^[A-Za-z]{3}$`)
	cpvFlagRE     = regexp.MustCompile(`^\d{1,8}(-\d)?$`)
)

// validateTEDFilters checks --country and --cpv before they reach a TED
// expert query or a SQL LIKE pattern. Both values are spliced into the TED
// query text, so anything beyond the documented shapes could change its
// meaning. Empty values mean "no filter".
func validateTEDFilters(country, cpv string) error {
	if c := strings.TrimSpace(country); c != "" && !countryFlagRE.MatchString(c) {
		return usageErr(fmt.Errorf("invalid --country %q: use a 3-letter ISO code such as DEU, FRA or POL", country))
	}
	if c := strings.TrimSpace(cpv); c != "" && !cpvFlagRE.MatchString(c) {
		return usageErr(fmt.Errorf("invalid --cpv %q: use 1-8 digits with an optional check digit, e.g. 45, 45210000 or 45210000-2", cpv))
	}
	return nil
}

// The generator registers --data-source only for spec-derived store reads;
// this CLI's store is hand-built, so the hook adds the root flag itself.
func init() {
	registerNovelCommand(func(root *cobra.Command, flags *rootFlags) {
		if root.PersistentFlags().Lookup("data-source") == nil {
			root.PersistentFlags().StringVar(&flags.dataSource, "data-source", string(sourceAuto),
				"Data source for read commands: auto (live with local fallback where supported), live (TED API only), local (synced store only)")
		}
	})
}

// tedSearch runs one TED search request. TED search is a read that rides
// POST, so it uses the read path of the generated client.
func tedSearch(ctx context.Context, flags *rootFlags, req ted.SearchRequest) (ted.SearchResponse, error) {
	c, err := flags.newClient()
	if err != nil {
		return ted.SearchResponse{}, err
	}
	data, _, err := c.PostQueryWithParams(ctx, ted.SearchPath, nil, req)
	if err != nil {
		return ted.SearchResponse{}, classifyAPIErrorOnly(err)
	}
	return ted.ParseSearchResponse(data)
}

// pageTED walks TED results with ITERATION pagination, handing each page to
// onPage. maxNotices and maxPages <= 0 mean no cap. truncated reports that
// a cap stopped the walk while TED still had matching notices.
func pageTED(ctx context.Context, flags *rootFlags, query string, fields []string, pageSize, maxNotices, maxPages int,
	onPage func(resp ted.SearchResponse) error) (total int, truncated bool, err error) {
	if pageSize <= 0 {
		pageSize = tedPageSize
	}
	token := ""
	fetched := 0
	warned := false
	for page := 1; ; page++ {
		batch := pageSize
		if maxNotices > 0 {
			rem := maxNotices - fetched
			if rem <= 0 {
				return total, fetched < total, nil
			}
			if rem < batch {
				batch = rem
			}
		}
		resp, err := tedSearch(ctx, flags, ted.SearchRequest{
			Query:              query,
			Fields:             fields,
			Limit:              batch,
			PaginationMode:     ted.PaginationIteration,
			IterationNextToken: token,
		})
		if err != nil {
			return total, false, err
		}
		total = resp.TotalNoticeCount
		if resp.TimedOut && !warned {
			fmt.Fprintln(os.Stderr, "warning: TED reported a partial (timed-out) result")
			warned = true
		}
		if err := onPage(resp); err != nil {
			return total, false, err
		}
		fetched += len(resp.Notices)
		if resp.IterationNextToken == "" || len(resp.Notices) < batch {
			return total, false, nil
		}
		if maxPages > 0 && page >= maxPages {
			// TED returns a next token even after an exactly full last
			// page, so the token alone does not prove more notices exist.
			return total, fetched < total, nil
		}
		token = resp.IterationNextToken
	}
}

// tedSearchNotices collects up to maxNotices raw notices (one page when <= 0).
func tedSearchNotices(ctx context.Context, flags *rootFlags, query string, fields []string, maxNotices int) ([]map[string]any, int, error) {
	if maxNotices <= 0 {
		maxNotices = tedPageSize
	}
	out := make([]map[string]any, 0)
	total, _, err := pageTED(ctx, flags, query, fields, tedPageSize, maxNotices, 0, func(resp ted.SearchResponse) error {
		out = append(out, resp.Notices...)
		return nil
	})
	return out, total, err
}

func resolveTendersDB(dbPath string) string {
	if dbPath != "" {
		return dbPath
	}
	return defaultDBPath(tendersCLIName)
}

// openLocalMirror opens the synced store query-only for a local-only read.
// When the store or its TED tables do not exist yet it prints the sync hint
// and an empty result, and stop is true. Otherwise st is open and the caller
// must close it.
func openLocalMirror(cmd *cobra.Command, flags *rootFlags, dbPath string, empty any) (st *store.Store, stop bool, err error) {
	st, ok, err := openTendersIfSynced(cmd.Context(), dbPath)
	if err != nil {
		return nil, true, err
	}
	if ok {
		return st, false, nil
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "no synced TED notices at %s\nrun: %s sync --since 90d --param country=DEU --param cpv=45 --db %s\n", dbPath, tendersCLIName, dbPath)
	if !wantsHumanTable(cmd.OutOrStdout(), flags) {
		return nil, true, printJSONFiltered(cmd.OutOrStdout(), empty, flags)
	}
	return nil, true, nil
}

// openTendersRead opens the local store query-only. Read commands must never
// migrate or write the file: it may belong to a concurrent sync, or be a
// database the user pointed --db at for inspection.
func openTendersRead(ctx context.Context, dbPath string) (*store.Store, error) {
	st, err := store.OpenQueryOnly(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening local store %s: %w", dbPath, err)
	}
	return st, nil
}

// openTendersIfSynced opens the store query-only when the file and its TED
// tables exist. ok is false otherwise, and callers treat that as an empty
// store.
func openTendersIfSynced(ctx context.Context, dbPath string) (st *store.Store, ok bool, err error) {
	exists, err := fileExists(dbPath)
	if err != nil || !exists {
		return nil, false, err
	}
	st, err = openTendersRead(ctx, dbPath)
	if err != nil {
		return nil, false, err
	}
	has, err := st.HasNoticesTable(ctx)
	if err != nil || !has {
		_ = st.Close()
		return nil, false, err
	}
	return st, true, nil
}

// hintIfNoNotices writes a stderr hint when the store has no synced notices.
func hintIfNoNotices(cmd *cobra.Command, st *store.Store, noticeType string) {
	n, err := st.NoticeCount(cmd.Context(), noticeType)
	if err != nil || n > 0 {
		return
	}
	what := "notices"
	if noticeType != "" {
		what = noticeType + " notices"
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "hint: the local store has no %s; run %s sync first\n", what, tendersCLIName)
}

// requireLocalSource rejects --data-source live for local-only commands.
func requireLocalSource(flags *rootFlags) error {
	if activeSource(flags) == sourceLive {
		return usageErr(fmt.Errorf("this command reads the local store only; --data-source live has no live equivalent (run sync, then use --data-source local or auto)"))
	}
	return nil
}

var isoDateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// resolveSinceDate accepts YYYY-MM-DD or a duration such as 30d/2w/24h and
// returns a YYYY-MM-DD date.
func resolveSinceDate(s string, now time.Time) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if isoDateRE.MatchString(s) {
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return "", fmt.Errorf("invalid date %q: %w", s, err)
		}
		return s, nil
	}
	d, err := cliutil.ParseDurationLoose(s)
	if err != nil {
		return "", fmt.Errorf("invalid --since %q: use YYYY-MM-DD or a duration like 30d", s)
	}
	return now.Add(-d).UTC().Format("2006-01-02"), nil
}

// daysAgo returns the YYYY-MM-DD date n days before now.
func daysAgo(n int, now time.Time) string {
	return now.AddDate(0, 0, -n).UTC().Format("2006-01-02")
}

// cpvLike returns a SQL LIKE pattern matching every code under a CPV prefix.
func cpvLike(cpv string) string {
	if strings.TrimSpace(cpv) == "" {
		return "%"
	}
	return ted.CPVPrefix(cpv) + "%"
}

// likeSubstring returns a LIKE pattern matching s anywhere, with LIKE
// metacharacters escaped (use with ESCAPE '\').
func likeSubstring(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// noticeFilterSQL builds a WHERE fragment over the notices table.
type noticeFilterSQL struct {
	clauses []string
	args    []any
}

func (f *noticeFilterSQL) add(clause string, args ...any) {
	f.clauses = append(f.clauses, clause)
	f.args = append(f.args, args...)
}

func (f *noticeFilterSQL) country(col, country string) {
	if country != "" {
		f.add(col+" = ?", strings.ToUpper(strings.TrimSpace(country)))
	}
}

func (f *noticeFilterSQL) cpv(col, cpv string) {
	if strings.TrimSpace(cpv) != "" {
		f.add(col+" LIKE ?", cpvLike(cpv))
	}
}

func (f *noticeFilterSQL) since(col, date string) {
	if date != "" {
		f.add(col+" >= ?", date)
	}
}

func (f *noticeFilterSQL) where() string {
	if len(f.clauses) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(f.clauses, " AND ")
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// parseCSVList splits a comma-separated flag value into trimmed lowercase terms.
func parseCSVList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func containsAny(text string, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	lower := strings.ToLower(text)
	for _, t := range terms {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

var publicationNumberRE = regexp.MustCompile(`^\d{1,8}-\d{4}$`)

// companyKey identifies one company across notices: TED repeats the same
// firm with varying case and spacing, and same-name firms in different
// countries are different companies.
func companyKey(name, country string) string {
	return ted.NormalizeName(name) + "|" + strings.ToUpper(strings.TrimSpace(country))
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// liveScratchStore fetches notices matching a TED query into a temporary
// store so local-store analytics can answer from live data without a sync.
// The caller must call cleanup.
func liveScratchStore(cmd *cobra.Command, flags *rootFlags, query string, maxScan int) (*store.Store, func(), int, error) {
	maxScan = capMaxScan(maxScan)
	ctx, cancel := boundCtx(cmd.Context(), flags)
	defer cancel()
	raws, _, err := tedSearchNotices(ctx, flags, query, ted.SyncFields, maxScan)
	if err != nil {
		return nil, func() {}, 0, err
	}
	dir, err := os.MkdirTemp("", "eu-tenders-live-")
	if err != nil {
		return nil, func() {}, 0, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	st, err := store.OpenWithContext(cmd.Context(), filepath.Join(dir, "live-scratch"))
	if err != nil {
		cleanup()
		return nil, func() {}, 0, err
	}
	notices := make([]ted.Notice, 0, len(raws))
	for _, raw := range raws {
		notices = append(notices, ted.Extract(raw))
	}
	if err := st.UpsertNotices(cmd.Context(), notices, raws); err != nil {
		_ = st.Close()
		cleanup()
		return nil, func() {}, 0, err
	}
	return st, func() { _ = st.Close(); cleanup() }, len(raws), nil
}

// tedQuoted renders a value for a TED text clause such as winner-name~"...".
// Quotes and backslashes are dropped so the value cannot close the literal
// or escape its closing quote.
func tedQuoted(s string) string {
	s = strings.NewReplacer(`"`, "", `\`, "").Replace(s)
	return `"` + strings.TrimSpace(s) + `"`
}
