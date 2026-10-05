# EU Tenders (TED) CLI Absorb Manifest — reprint 2026-10-02

Sources: TED Search API v3 (official spec), ted-cli (jgalea), mcp-ted-eu / fbuchner ted-mcp,
Apify TED lead-finder / award-watch / award-integrity actors, prior eu-tenders-pp-cli
(reverse-engineered inventory), prior 2026-05 absorb manifest.

## Absorbed (match or beat everything that exists)

| # | Feature | Best Source | Our Implementation | Added Value |
|---|---------|-------------|-------------------|-------------|
| 1 | Expert-query notice search | TED API, ted-cli `query`, ted-mcp | (generated endpoint) notices search | --json/--select/--csv, agent-native, typed MCP tool |
| 2 | Single notice by publication number | ted-cli `show`, mcp-ted-eu | eu-tenders-pp-cli notices get | Multilingual resolved, winners with aligned contacts, TED URL |
| 3 | Searchable field list | ted-cli `fields` | eu-tenders-pp-cli fields | Grep-able list of queryable field names from the spec |
| 4 | CPV lookup | ted-cli presets, prior `cpv` | eu-tenders-pp-cli cpv get | Code → description |
| 5 | CPV keyword search | prior `cpv search` | eu-tenders-pp-cli cpv search | Description → codes |
| 6 | Open tenders by deadline | prior `deadline`, SaaS | eu-tenders-pp-cli deadline | live or local, closest first |
| 7 | Award notices listing | prior `awards`, ted-mcp | eu-tenders-pp-cli awards | Winner + buyer + value; --winner, --year |
| 8 | Incremental sync to SQLite | tap-eu-ted, prior `sync` | eu-tenders-pp-cli sync | ITERATION pagination, SORT DESC, date cursor, notices + notice_winners, FTS delete-before-reinsert |
| 9 | Offline FTS search | prior `search` | eu-tenders-pp-cli search | FTS5 over title/buyer/winner |
| 10 | Raw SQL over store | prior `sql` | eu-tenders-pp-cli sql | Read-only SELECT, composable with jq |
| 11 | New-since-last-run watch | ted-cli `watch` | (behavior in eu-tenders-pp-cli leads) --new-only | Seen-state in SQLite instead of seen.json |
| 12 | CSV/JSON/table output | ted-cli, SaaS | (behavior in eu-tenders-pp-cli awards) global output flags | --csv/--json/--select everywhere |
| 13 | Doctor | Printing Press standard | eu-tenders-pp-cli doctor | Reachability + store |

## Transcendence (only possible with our approach)

| # | Feature | Command | Buildability | Why Only We Can Do This | Long Description |
|---|---------|---------|--------------|------------------------|------------------|
| 1 | Construction Leads (10) | leads | hand-code | Zips winner contact arrays against organisation-name-tenderer (not winner-name), one row per winner, dedupe + seen-state in SQLite | Use this command to get a contactable outreach list of companies that recently won contracts. Do NOT use it to profile one known company's history; use 'winner' instead. Do NOT use it for a plain award table without contacts; use 'awards' instead. |
| 2 | Open-tender ranker (9) | score | hand-code | Weighted urgency/value/keyword rank over open calls | Use this command to rank open tenders by fit to your keywords plus value and urgency. Do NOT use it to find tenders closing soon in under-competed markets; use 'deadline-heat' instead. |
| 3 | Buyer dossier (9) | buyer | hand-code | Multi-year join across one authority's notices and winners | Use this command to profile one contracting authority. Do NOT use it for market-wide share; use 'concentration' instead. Do NOT use it to profile a winning company; use 'winner' instead. |
| 4 | Market concentration (8) | concentration | hand-code | Share + HHI over winners grouped by name+country | Use this command for market-level winner share and HHI in a country/CPV slice. Do NOT use it for one buyer's winners; use 'buyer' instead. Do NOT use it for one company's wins; use 'winner' instead. |
| 5 | Winner dossier (8, new) | winner | hand-code | Aggregates every notice_winners row for a company with latest contacts | Use this command to profile one winning company. Do NOT use it for a list of new leads; use 'leads' instead. Do NOT use it for an authority; use 'buyer' instead. |
| 6 | Tender incumbents (7, new) | incumbents | hand-code | Joins an open call's buyer + CPV prefix to prior awards in the store | Use this command to see who previously won from the same buyer for one specific open tender. Do NOT use it for a buyer's full profile; use 'buyer' instead. |
| 7 | Deadline heat (7) | deadline-heat | hand-code | Competition density from historical winners per CPV/buyer | Use this command to find open tenders closing within days where few competitors usually bid. Do NOT use it for keyword-fit ranking; use 'score' instead. |
| 8 | Buyer award rate (7) | win-rate | hand-code | cn↔can join per buyer | Use this command to list award rate and winner diversity for every buyer in a market. Do NOT use it to flag only anomalous buyers; use 'dark-buyers' instead. |
| 9 | Dark buyers (7) | dark-buyers | hand-code | Award-rate + single-winner thresholds on the per-buyer join (low-confidence heuristic) | Use this command to flag buyers with suspicious award patterns (low award rate, single repeat winner). Do NOT use it for the full per-buyer award-rate table; use 'win-rate' instead. |
| 10 | Award velocity (6) | velocity | hand-code | Weekly bucketing + prior-year comparison | none |
| 11 | CPV drift (6) | cpv-drift | hand-code | YoY CPV growth by count or value | none |

### Reprint verdicts
All nine prior novel features kept (leads, score, deadline-heat, win-rate, dark-buyers,
concentration, velocity, cpv-drift, buyer). No prior features dropped. Killed new candidates:
newcomers, winner-graph, peer-benchmark, regions, upcoming-awards, digest, contacts (see
brainstorm file).

### Stubs
None.
