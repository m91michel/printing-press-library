# EU Tenders (TED) CLI Brief

## API Identity
- Domain: EU public procurement. TED (Tenders Electronic Daily) is the official EU journal for
  above-threshold public procurement notices, run by the Publications Office of the EU.
- Users: (1) B2B sales / business development teams prospecting award winners (e.g. construction
  machinery rental selling to firms that just won construction contracts); (2) bid managers
  scanning open calls; (3) procurement/market analysts and policy researchers; (4) compliance and
  data journalists; (5) eProcurement platform integrators.
- Data profile: ~680k notices/year, EU27 + EEA, full data since 2007. One public, unauthenticated
  endpoint `POST https://api.ted.europa.eu/v3/notices/search` with an expert query DSL
  (`field=value AND ... SORT BY publication-date DESC`), explicit `fields` selection, two
  pagination modes (PAGE_NUMBER up to 15k results, ITERATION via `iterationNextToken`, unlimited).

## Reachability Risk
- None. Live probe 2026-10-02: `POST /v3/notices/search` returned 200, `totalNoticeCount` 2007 for
  DEU construction awards since 2026-09-01, `timedOut: false`. No auth.
- Spec: official OpenAPI 3.1 at `https://api.ted.europa.eu/api-v3.yaml` (via
  `/v3/api-docs/swagger-config`). It now bundles eSender endpoints (submit, validate, render,
  convert, api-keys, stop-publication) that require an eSender API key and are out of scope.
  Search contract (`PublicExpertSearchRequestV1`: query, fields, limit, page, paginationMode,
  iterationNextToken, onlyLatestVersions, scope, checkQuerySyntax) is unchanged versus the
  2026-05 spec apart from tag rename `notice-search-v3` → `Search`. Plan: search-only spec.

## Top Workflows
1. **Award-winner lead generation** (user's priority): "Which companies won construction contracts
   in Germany in the last 7/30/90 days, where, for how much, and how do I reach them?" Weekly digest
   piped to Slack/CRM.
2. **Open-call sweep**: country + CPV + deadline-within-N-days, ranked by urgency/value/fit.
3. **Award intelligence / competitor mapping**: who wins CPV X in country Y, market share, HHI,
   incumbents vs newcomers.
4. **Buyer profiling**: a contracting authority's cadence, CPV mix, values, repeat winners.
5. **Bulk sync + SQL analytics**: pull a slice into SQLite, run trends (velocity, CPV drift).
6. **New-since-last-run monitoring** (ted-cli `watch`): only show notices not seen before.

## Table Stakes
- ted-cli (jgalea, Python): search by country/CPV/text/date/value/open/deadline, CPV presets,
  `watch` with local seen-state, raw `query`, `show <pub-number>`, `fields` list, table/json/csv/urls.
- mcp-ted-eu / fbuchner ted-mcp: fetch notice by publication number, structured search,
  multilingual field resolution, winner/contract pairing.
- Apify actors (TED lead finder, award watch, award integrity feed): award-winner rows with
  normalized values — confirms the leads workflow has paying demand.
- SaaS (TenderMetric, Tenderlake, Spend Network): saved searches, alerts, CSV export.

## Data Layer
- Primary entity: Notice (publication-number PK, notice-type, publication-date, buyer name/country/
  city/email, CPV codes, procedure-type, deadline, estimated/result value + currency, title,
  place-of-performance NUTS + city, previous-notice id, links).
- Secondary: **NoticeWinner** (one row per awarded tenderer per notice): name, country, NUTS
  (`winner-country-sub`), city, post code, email, phone, identifier (VAT/HRB), size
  (small/medium/large). The prior CLI collapsed winners to one string per notice; multi-lot notices
  carry several winners.
- CPV reference (code → description) for human-readable project type.
- Sync cursor: publication-date (+ publication-number), ITERATION pagination token.
- FTS5: title, buyer name, winner names. Delete old FTS row before re-insert on upsert.

## Codebase Intelligence (live probes + prior CLI + patches)
- Field truths verified live 2026-10-02:
  - Contact fields exist on award notices: `winner-email`, `organisation-tel-tenderer`,
    `winner-city`, `winner-post-code`, `winner-country-sub`, `winner-identifier`, `winner-size`;
    buyer side `buyer-email`, `buyer-city` (map `{"mul":[...]}`), `place-of-performance-city-proc`.
    Not available: `winner-phone`, `winner-street`, `winner-internet-address`, `business-email`.
  - **Index alignment trap:** contact arrays (email, tel, city, …) are index-aligned with
    `organisation-name-tenderer`, NOT with `winner-name` (pub 680471-2026: winner-name order
    [Johann Bunte, SECUTEC, SP] vs tenderer/email order [SP, SECUTEC, Johann Bunte]). Zip contacts
    against `organisation-name-tenderer`; match to winners by name.
  - Multilingual shapes: `title-proc` = map[lang]string; `title-lot`, `winner-name`,
    `organisation-name-tenderer` = map[lang][]string; `buyer-city` = map["mul"][]string;
    `notice-title` = map[lang]string with a "Country – Category – " prefix, present on every
    notice (usable final title fallback). Title order: title-proc > title-lot > notice-title.
  - `result-value-notice` is a string; `total-value` is a number; either may be missing.
  - Default ordering is ascending/old (a query without `SORT BY publication-date DESC` returned a
    2016 notice first). Always sort explicitly.
- Prior patches to honor: FTS delete-before-reinsert; group winners by name+country; no replay of
  ambiguous writes (moot for a read-only API but keep client default); Go floor ≥ 1.26.5.

## User Vision
- "can you use the existing cli and reverse engineer the features so that we have the same feature
  set? e.g. I built a command to get leads based on who won a bid."
- Every prior command is a must-keep (see prior-feature-inventory.md). `leads` must ship at parity
  or better: same flags (--country, --cpv default 45, --days, --keywords, --limit, --min-value,
  --region) and output fields, plus the ICP help text for construction-machinery rental.
- Reprint enrichment accepted: MCP over stdio + http with an agent-useful surface; sync/cache
  correctness for notices.

## Product Thesis
- Name: eu-tenders-pp-cli (slug eu-tenders, unchanged).
- Why it should exist: TED is free and huge, but it needs query-DSL knowledge, field-name lore
  (1,800+ fields, multilingual shapes, misaligned arrays) and custom pagination. Lead-gen SaaS and
  Apify actors charge for what this does locally: contactable award-winner leads, offline SQL over
  a synced corpus, and market analytics (share, HHI, velocity, drift) no competitor ships, all
  agent-reachable via MCP.

## Build Priorities
1. `sync` — correct incremental sync (ITERATION pagination, SORT DESC, date cursor), notices +
   notice_winners tables, FTS hygiene.
2. `leads` — parity + winner contact data (email, phone, city, post code, NUTS, VAT/HRB id, size),
   one row per winner, dedupe/aggregate per company (`--group-by company` with win count + total).
3. `deadline`, `awards`, `search`, `sql`, `cpv get|search`, notice `get` by publication number.
4. Analytics: score, deadline-heat, win-rate, concentration, buyer, dark-buyers, velocity, cpv-drift.
5. `watch`-style new-since-last-run (absorbed from ted-cli) — candidate for leads `--new-only`.
