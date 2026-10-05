// Copyright 2026 Mathias Michel and contributors. Licensed under Apache-2.0. See LICENSE.
// pp:data-source local

package cli

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/mvanhorn/printing-press-library/library/sales-and-crm/eu-tenders/internal/ted"
)

// tedFieldNames is the queryable field list from the TED Search API spec.
// pp:novel-static-reference
//
//go:embed ted_fields.txt
var tedFieldNames string

func init() {
	registerNovelCommand(func(root *cobra.Command, flags *rootFlags) {
		addNovelCommandIfAbsent(root, newTendersSearchCmd(flags))
		addNovelCommandIfAbsent(root, newTendersSQLCmd(flags))
		addNovelCommandIfAbsent(root, newTendersFieldsCmd(flags))
		if noticesCmd, _, err := root.Find([]string{"notices"}); err == nil && noticesCmd != root {
			addNovelCommandIfAbsent(noticesCmd, newNoticesGetCmd(flags))
		}
	})
}

// noticeRow is the compact notice shape shared by list commands.
type noticeRow struct {
	ID                 string  `json:"id"`
	NoticeType         string  `json:"notice_type"`
	PublicationDate    string  `json:"publication_date"`
	Title              string  `json:"title"`
	BuyerName          string  `json:"buyer_name"`
	BuyerCountry       string  `json:"buyer_country"`
	CPVCode            string  `json:"cpv_code"`
	EstimatedValue     float64 `json:"estimated_value"`
	ContractValue      float64 `json:"contract_value"`
	Currency           string  `json:"currency"`
	SubmissionDeadline string  `json:"submission_deadline"`
	Winners            string  `json:"winners"`
	NoticeURL          string  `json:"notice_url"`
}

const noticeRowColumns = `n.id, n.notice_type, n.publication_date, n.title, n.buyer_name, n.buyer_country,
	n.cpv_code, n.estimated_value, n.contract_value, n.currency, n.submission_deadline,
	COALESCE((SELECT group_concat(w.name, '; ') FROM notice_winners w WHERE w.notice_id = n.id), ''), n.notice_url`

func scanNoticeRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]noticeRow, error) {
	out := make([]noticeRow, 0)
	for rows.Next() {
		var r noticeRow
		if err := rows.Scan(&r.ID, &r.NoticeType, &r.PublicationDate, &r.Title, &r.BuyerName, &r.BuyerCountry,
			&r.CPVCode, &r.EstimatedValue, &r.ContractValue, &r.Currency, &r.SubmissionDeadline, &r.Winners, &r.NoticeURL); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	return out, rows.Close()
}

// ftsQuery quotes each term so user input never trips FTS5 syntax.
func ftsQuery(terms []string) string {
	parts := make([]string, 0, len(terms))
	for _, t := range terms {
		t = strings.TrimSpace(strings.ReplaceAll(t, `"`, ""))
		if t != "" {
			parts = append(parts, `"`+t+`"`)
		}
	}
	return strings.Join(parts, " ")
}

func newTendersSearchCmd(flags *rootFlags) *cobra.Command {
	var dbPath, noticeType string
	var limit int
	cmd := &cobra.Command{
		Use:   "search [query]",
		Short: "Full-text search synced notices by title, buyer and winner names",
		Example: strings.Trim(`
  eu-tenders-pp-cli search "Neubau Schule"
  eu-tenders-pp-cli search Brücke --type award --limit 50 --json`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "local",
			"pp:happy-args":  "query=Neubau",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "search local notices")
			}
			if err := requireLocalSource(flags); err != nil {
				return err
			}
			q := ftsQuery(args)
			if q == "" {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("a search query is required, e.g. search \"Neubau Schule\""))
			}
			var types []string
			if noticeType != "" {
				var err error
				if types, err = parseNoticeTypeFlag(noticeType); err != nil {
					return usageErr(err)
				}
			}
			dbPath = resolveTendersDB(dbPath)
			st, stop, err := openLocalMirror(cmd, flags, dbPath, make([]noticeRow, 0))
			if stop {
				return err
			}
			defer st.Close()
			hintIfNoNotices(cmd, st, "")
			sqlq := `SELECT ` + noticeRowColumns + ` FROM notices_fts f JOIN notices n ON n.id = f.notice_id WHERE notices_fts MATCH ?`
			qargs := []any{q}
			if len(types) == 1 {
				sqlq += ` AND n.notice_type = ?`
				qargs = append(qargs, types[0])
			}
			if limit <= 0 {
				limit = -1
			}
			sqlq += ` ORDER BY n.publication_date DESC LIMIT ?`
			qargs = append(qargs, limit)
			ctx, cancel := boundCtx(cmd.Context(), flags)
			defer cancel()
			rows, err := st.DB().QueryContext(ctx, sqlq, qargs...)
			if err != nil {
				return fmt.Errorf("searching notices: %w", err)
			}
			out, err := scanNoticeRows(rows)
			if err != nil {
				return fmt.Errorf("reading search results: %w", err)
			}
			return printNoticeRows(cmd, flags, out)
		},
	}
	cmd.Flags().StringVar(&noticeType, "type", "", "Only award or call notices (default: both)")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum results to return")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}

func printNoticeRows(cmd *cobra.Command, flags *rootFlags, out []noticeRow) error {
	if !wantsHumanTable(cmd.OutOrStdout(), flags) {
		return printJSONFiltered(cmd.OutOrStdout(), out, flags)
	}
	if len(out) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No matching notices.")
		return nil
	}
	tw := newTabWriter(cmd.OutOrStdout())
	fmt.Fprintln(tw, "ID\tDATE\tTYPE\tBUYER\tVALUE\tTITLE")
	for _, r := range out {
		v := r.ContractValue
		if v == 0 {
			v = r.EstimatedValue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%.0f\t%s\n", r.ID, r.PublicationDate, r.NoticeType, truncate(r.BuyerName, 40), v, truncate(r.Title, 60))
	}
	return tw.Flush()
}

func newTendersSQLCmd(flags *rootFlags) *cobra.Command {
	var dbPath string
	var limit int
	cmd := &cobra.Command{
		Use:   "sql [select-statement]",
		Short: "Run a read-only SELECT against the local notices store",
		Long: `Run a read-only SELECT (or WITH ... SELECT) against the local store.

Tables: notices (one row per notice), notice_winners (one row per awarded
company with contact fields), notices_fts (full-text index), lead_seen.`,
		Example: strings.Trim(`
  eu-tenders-pp-cli sql "SELECT buyer_country, COUNT(*) FROM notices GROUP BY 1"
  eu-tenders-pp-cli sql "SELECT name, city, email, SUM(value) v FROM notice_winners GROUP BY name_key ORDER BY v DESC LIMIT 10" --json`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "local",
			"pp:happy-args":  "statement=SELECT COUNT(*) AS n FROM notices",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "run local SQL")
			}
			if err := requireLocalSource(flags); err != nil {
				return err
			}
			stmt := strings.TrimSpace(strings.Join(args, " "))
			if stmt == "" {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("a SELECT statement is required"))
			}
			stmt, err := validateReadOnlySQL(stmt)
			if err != nil {
				return usageErr(err)
			}
			if limit < 0 {
				return usageErr(fmt.Errorf("--limit must be 0 (no row cap) or positive"))
			}
			dbPath = resolveTendersDB(dbPath)
			ro, stop, err := openLocalMirror(cmd, flags, dbPath, make([]map[string]any, 0))
			if stop {
				return err
			}
			defer ro.Close()
			ctx, cancel := boundCtx(cmd.Context(), flags)
			defer cancel()
			out, more, err := runReadOnlySQL(ctx, ro.DB(), stmt, limit)
			if err != nil {
				return err
			}
			if more {
				fmt.Fprintf(cmd.ErrOrStderr(), "stopped after %d rows; raise --limit\n", limit)
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), out, flags)
			}
			if len(out) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "(no rows)")
				return nil
			}
			return printAutoTable(cmd.OutOrStdout(), out)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 1000, "Maximum rows to return (0 = no row cap; output is still capped at 10 MB)")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}

const (
	// sqlMaxValueBytes caps one text or blob cell, so a raw_data column
	// cannot dominate the output.
	sqlMaxValueBytes = 64 << 10
	// sqlMaxOutputBytes caps the estimated memory of the collected result:
	// JSON bytes plus a per-row and per-column allowance for the Go maps.
	sqlMaxOutputBytes = 10 << 20
	// sqlMaxSQLiteLength makes SQLite refuse building any single string or
	// blob above this size ("string or blob too big"), so zeroblob/printf
	// tricks fail inside SQLite before the value is copied into Go memory.
	// Stored notice payloads are a few KB, far below it.
	sqlMaxSQLiteLength = 4 << 20
	sqlTruncatedMarker = "…(truncated)"
)

// runReadOnlySQL runs a validated statement and collects up to limit rows
// (limit <= 0 means no row cap). more reports that rows remained after the
// cap. ctx bounds the query: modernc sqlite interrupts a running statement
// when ctx is done, so a runaway recursive CTE stops at --timeout.
func runReadOnlySQL(ctx context.Context, db *sql.DB, stmt string, limit int) (out []map[string]any, more bool, err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()
	if _, err := sqlite.Limit(conn, sqlite3.SQLITE_LIMIT_LENGTH, sqlMaxSQLiteLength); err != nil {
		return nil, false, fmt.Errorf("sql: setting value size limit: %w", err)
	}
	rows, err := conn.QueryContext(ctx, stmt)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, sqlTimeoutErr(ctx)
		}
		return nil, false, usageErr(fmt.Errorf("sql: %w", err))
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, false, err
	}
	out = make([]map[string]any, 0)
	size := 0
	for rows.Next() {
		if limit > 0 && len(out) >= limit {
			more = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, false, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			switch v := vals[i].(type) {
			case []byte:
				m[c] = capSQLText(string(v))
			case string:
				m[c] = capSQLText(v)
			default:
				m[c] = v
			}
		}
		b, err := json.Marshal(m)
		if err != nil {
			return nil, false, err
		}
		size += len(b) + 64 + 48*len(cols)
		if size > sqlMaxOutputBytes {
			return nil, false, usageErr(fmt.Errorf("sql: result exceeds %d MB after %d rows; add a LIMIT, select fewer columns or lower --limit", sqlMaxOutputBytes>>20, len(out)))
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, false, sqlTimeoutErr(ctx)
		}
		return nil, false, fmt.Errorf("sql: %w", err)
	}
	if ctx.Err() != nil {
		return nil, false, sqlTimeoutErr(ctx)
	}
	return out, more, nil
}

func sqlTimeoutErr(ctx context.Context) error {
	return fmt.Errorf("sql: query stopped (%w); narrow the statement or raise --timeout", ctx.Err())
}

// capSQLText shortens s to sqlMaxValueBytes on a UTF-8 boundary.
func capSQLText(s string) string {
	if len(s) <= sqlMaxValueBytes {
		return s
	}
	cut := sqlMaxValueBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + sqlTruncatedMarker
}

// sqlForbiddenWords are statements that write, attach other files or change
// connection state. The query-only handle refuses writes as well; rejecting
// them up front gives a clear usage error and keeps ATTACH from creating
// files on disk.
var sqlForbiddenWords = map[string]bool{
	"ATTACH": true, "DETACH": true, "PRAGMA": true, "VACUUM": true,
	"INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true,
	"CREATE": true, "DROP": true, "ALTER": true,
}

// validateReadOnlySQL accepts exactly one SELECT or WITH statement and
// returns it without a trailing semicolon. String literals, quoted
// identifiers and comments are blanked before the checks, so a ';' or a
// keyword inside 'text' is fine while one in the statement body is not.
func validateReadOnlySQL(stmt string) (string, error) {
	masked := maskSQLLiterals(stmt)
	trimmed := strings.TrimRight(masked, " \t\r\n")
	cut := len(trimmed)
	if strings.HasSuffix(trimmed, ";") {
		cut--
		trimmed = trimmed[:cut]
	}
	if strings.Contains(trimmed, ";") {
		return "", fmt.Errorf("only one SQL statement is allowed")
	}
	body := strings.TrimSpace(trimmed)
	lower := strings.ToLower(body)
	if !strings.HasPrefix(lower, "select") && !strings.HasPrefix(lower, "with") {
		return "", fmt.Errorf("only SELECT or WITH ... SELECT statements are allowed")
	}
	words := sqlWordRE.FindAllStringIndex(body, -1)
	for _, w := range words {
		word := strings.ToUpper(body[w[0]:w[1]])
		if !sqlForbiddenWords[word] {
			continue
		}
		// replace(x, y, z) is a read-only string function; REPLACE as a
		// statement is never followed by "(".
		if word == "REPLACE" && strings.HasPrefix(strings.TrimLeft(body[w[1]:], " \t\r\n"), "(") {
			continue
		}
		return "", fmt.Errorf("%s is not allowed: the sql command runs read-only SELECT statements", word)
	}
	// masked keeps byte offsets, so the cut applies to the original text.
	return strings.TrimSpace(stmt[:cut]), nil
}

var sqlWordRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// maskSQLLiterals replaces the contents of '...', "...", `...`, [...] and
// comments with spaces, keeping byte offsets. An unterminated literal masks
// to the end, which SQLite itself rejects.
func maskSQLLiterals(s string) string {
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		switch c := b[i]; {
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			for j < len(b) {
				if b[j] == c {
					if j+1 < len(b) && b[j+1] == c {
						b[j], b[j+1] = ' ', ' '
						j += 2
						continue
					}
					break
				}
				b[j] = ' '
				j++
			}
			i = j
		case c == '[':
			j := i + 1
			for j < len(b) && b[j] != ']' {
				b[j] = ' '
				j++
			}
			i = j
		case c == '-' && i+1 < len(b) && b[i+1] == '-':
			j := i
			for j < len(b) && b[j] != '\n' {
				b[j] = ' '
				j++
			}
			i = j
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			j := i
			for j < len(b) && !(b[j] == '*' && j+1 < len(b) && b[j+1] == '/') {
				b[j] = ' '
				j++
			}
			if j < len(b) {
				b[j], b[j+1] = ' ', ' '
				j++
			}
			i = j
		}
	}
	return string(b)
}

func newTendersFieldsCmd(flags *rootFlags) *cobra.Command {
	var contains string
	cmd := &cobra.Command{
		Use:   "fields",
		Short: "List TED expert-query field names, optionally filtered by a substring",
		Example: strings.Trim(`
  eu-tenders-pp-cli fields --contains winner
  eu-tenders-pp-cli fields --contains deadline --json`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "computed",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "list TED fields")
			}
			term := strings.ToLower(strings.TrimSpace(contains))
			out := make([]string, 0)
			for _, f := range strings.Split(tedFieldNames, "\n") {
				f = strings.TrimSpace(f)
				if f != "" && (term == "" || strings.Contains(strings.ToLower(f), term)) {
					out = append(out, f)
				}
			}
			if !wantsHumanTable(cmd.OutOrStdout(), flags) {
				return printJSONFiltered(cmd.OutOrStdout(), out, flags)
			}
			for _, f := range out {
				fmt.Fprintln(cmd.OutOrStdout(), f)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&contains, "contains", "", "Only fields whose name contains this text (e.g. winner, buyer, deadline)")
	return cmd
}

func newNoticesGetCmd(flags *rootFlags) *cobra.Command {
	var dbPath string
	cmd := &cobra.Command{
		Use:   "get [publication-number]",
		Short: "Show one notice by publication number, with winners and their contacts",
		Example: strings.Trim(`
  eu-tenders-pp-cli notices get 680471-2026
  eu-tenders-pp-cli notices get 680471-2026 --data-source local --json`, "\n"),
		Annotations: map[string]string{
			"mcp:read-only":  "true",
			"pp:data-source": "auto",
			"pp:happy-args":  "publication-number=680471-2026",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && cmd.Flags().NFlag() == 0 {
				return cmd.Help()
			}
			if dryRunOK(flags) {
				return writeDryRun(cmd.OutOrStdout(), flags, "get notice")
			}
			if len(args) == 0 {
				_ = cmd.Usage()
				return usageErr(fmt.Errorf("a publication number is required, e.g. notices get 680471-2026"))
			}
			id := strings.TrimSpace(args[0])
			if !publicationNumberRE.MatchString(id) {
				return usageErr(fmt.Errorf("invalid publication number %q: expected digits-year, e.g. 680471-2026", id))
			}
			ctx, cancel := boundCtx(cmd.Context(), flags)
			defer cancel()
			if activeSource(flags) != sourceLive {
				if n, ok, err := localNotice(cmd, resolveTendersDB(dbPath), id); err != nil {
					return err
				} else if ok {
					recordSource(flags, sourceLocal)
					return printNotice(cmd, flags, n)
				} else if activeSource(flags) == sourceLocal {
					return notFoundErr(fmt.Errorf("notice %s is not in the local store; run sync or use --data-source live", id))
				}
			}
			recordSource(flags, sourceLive)
			resp, err := tedSearch(ctx, flags, ted.SearchRequest{Query: ted.PublicationQuery(id), Fields: ted.SyncFields, Limit: 1})
			if err != nil {
				return err
			}
			if len(resp.Notices) == 0 {
				return notFoundErr(fmt.Errorf("TED has no notice %s", id))
			}
			return printNotice(cmd, flags, ted.Extract(resp.Notices[0]))
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default: the CLI data directory)")
	return cmd
}

func localNotice(cmd *cobra.Command, dbPath, id string) (ted.Notice, bool, error) {
	st, ok, err := openTendersIfSynced(cmd.Context(), dbPath)
	if err != nil || !ok {
		return ted.Notice{}, false, err
	}
	defer st.Close()
	var raw string
	err = st.DB().QueryRowContext(cmd.Context(), `SELECT raw_data FROM notices WHERE id=?`, id).Scan(&raw)
	if err != nil {
		return ted.Notice{}, false, nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(raw), &m) != nil || len(m) == 0 {
		return ted.Notice{}, false, nil
	}
	return ted.Extract(m), true, nil
}

func printNotice(cmd *cobra.Command, flags *rootFlags, n ted.Notice) error {
	if !wantsHumanTable(cmd.OutOrStdout(), flags) {
		return printJSONFiltered(cmd.OutOrStdout(), n, flags)
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "%s  %s  %s\n%s\n", n.ID, n.NoticeType, n.PublicationDate, n.Title)
	fmt.Fprintf(w, "Buyer: %s (%s, %s) %s\n", n.BuyerName, n.BuyerCity, n.BuyerCountry, n.BuyerEmail)
	fmt.Fprintf(w, "CPV: %s %s\n", n.CPVCode, cpvDescription(n.CPVCode))
	if n.ContractValue > 0 {
		fmt.Fprintf(w, "Awarded value: %.2f %s\n", n.ContractValue, n.Currency)
	} else if n.EstimatedValue > 0 {
		fmt.Fprintf(w, "Estimated value: %.2f %s\n", n.EstimatedValue, n.Currency)
	}
	if n.SubmissionDeadline != "" {
		fmt.Fprintf(w, "Deadline: %s\n", n.SubmissionDeadline)
	}
	for _, win := range n.Winners {
		fmt.Fprintf(w, "Winner: %s (%s %s) %s %s %s\n", win.Name, win.City, win.Country, win.Email, win.Phone, win.Identifier)
	}
	fmt.Fprintln(w, n.NoticeURL)
	return nil
}
