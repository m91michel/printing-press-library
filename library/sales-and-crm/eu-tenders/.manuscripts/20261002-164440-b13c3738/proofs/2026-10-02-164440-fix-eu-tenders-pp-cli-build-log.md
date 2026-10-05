Manifest transcendence rows: 11 planned, 11 built.

# eu-tenders-pp-cli build log (reprint 2026-10-02)

## Slice 1 — foundation + absorbed commands (done, build green)
Files: internal/ted/extract.go (+ extract_test.go, testdata/search_sample.json from live TED),
internal/store/extras.go (notices, notice_winners, notices_fts, lead_seen, ted_sync_state),
internal/store/tenders.go (UpsertNotices with winner + FTS replace, sync state, lead seen-state),
internal/cli/tenders_shared.go (tedSearch via PostQueryWithParams read path, paging, --data-source
root flag registration, SQL filter builder, since/days parsing), sync.go, store_queries.go
(search, sql, fields, notices get), cpv.go + cpv_reference.go (pp:novel-static-reference),
awards_deadline.go (awards, deadline + shared loadOpenCalls), leads_data.go (loadAwardWinners
local/live, lead filter/grouping), leads.go.

Decisions:
- Contacts zipped against organisation-name-tenderer; per-lot winner-name/tender-value summed per
  company (verified on 680471-2026, 679898-2026). Fixes prior misattribution risk.
- Prior CLI parsed result-value-notice (a string) as float → always 0; now string-parsed.
- Title: title-proc > title-lot > notice-title (prefix stripped); 0/600 missing titles in smoke sync.
- Multilingual: preferred eng/deu/fra/nld/mul, then any language (prior returned "" for e.g. pol).
- All TED queries SORT BY publication-date DESC (default order is oldest-first).
- FTS: standalone fts5 keyed by notice_id, delete-before-insert on every upsert (prior patch).
- --data-source registered by a novel hook (generator emits it only for spec-derived store reads).
- sync accepts --param country=/cpv=/type=/query= to match framework narrative vocabulary.
- leads: mcp:local-write (because --new-only records lead_seen); other reads mcp:read-only.

Smoke (DEU, cpv 45, 10d, 600 notices): 242 winners, 170 with email, 160 with phone, 242 with city.

## Slice 2 — analytics (delegated)
Delegated to two subagents with behavioral acceptance tests (raw outputs reviewed):
- buyer, winner, incumbents, concentration, win-rate, dark-buyers (shared helpers in buyer.go;
  buyerAwardStats in win_rate.go). All winner aggregation groups by (name_key, country).
  Effective winner value: own lot value, else notice value when sole winner, else 0.
- score, deadline-heat, velocity, cpv-drift (+ ranking.go pure math with table tests).
  velocity emits zero-count ISO weeks; score excludes past/over-window deadlines.
Fix after review: live paths report the CPV code matching --cpv (ted.PreferCPV) instead of the
notice's first code.

## Completion gate
- go test ./... green; go vet clean; per-row Cobra resolution: 0 misses (20 paths).
- dogfood novel_features_check: planned 11, found 11.

## Deferred / limitations
- Generator emits no --data-source flag, sync, search or sql for a single POST-search spec; all
  hand-built here (retro candidate: POST-search APIs with a store get no framework sync).
- Root Short renders "Eu Tenders CLI" although narrative.display_name is "EU Tenders" (retro candidate).
