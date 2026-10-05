# eu-tenders — Prior Feature Inventory (reverse-engineered from shipped CLI)

Source: public library `library/sales-and-crm/eu-tenders`, run_id 20260508-143927,
Printing Press 4.0.6. Extracted from `--help` of the built binary and `internal/cli/*.go`.
The user asked that the reprint keep this feature set. Each entry is a must-keep
unless the Printing Press now emits an equivalent; a drop needs a stated reason.

API: TED (Tenders Electronic Daily) search API, expert-query notices search. No auth.

## Local store (internal/store)
Table `notices`: id, notice_type (cn-standard | can-standard …), publication_date,
buyer_name, buyer_country, cpv_code, cpv_codes_json, estimated_value, currency,
winner_name, winner_country, contract_value, procedure_type, submission_deadline,
title, place_of_performance, notice_url, previous_notice_id, raw_data, synced_at.
FTS5 `notices_fts(title, buyer_name, winner_name)` external-content. `sync_state` kv.
Default DB: `~/.config/eu-tenders-pp-cli/notices.db`, overridable with `--db`.

## Data commands
- `sync` — pull notices into the store. Flags: --country (ISO3), --cpv (prefix), --since
  YYYY-MM-DD, --limit (0=all), --query (expert query, overrides others).
- `search <query>` — FTS5 over the store. --limit.
- `sql` — raw SELECT against the store.
- `notices` — endpoint mirror of TED expert search.
- `awards` — local award notices (can-standard). --country --cpv --year --winner (partial) --limit.
- `deadline` — open tenders by closest deadline; live/local via --data-source. --country --cpv --days(30) --min-value.
- `cpv get <code>` / `cpv search <keyword>` — CPV vocabulary browse.

## Novel features (9)
1. `leads` (Construction Leads) — **the user's key command.** Recent award winners as B2B
   outreach candidates for construction-machinery rental. Flags: --country, --cpv (default
   "45"), --days (90), --keywords (comma, OR, case-insensitive title match), --limit (50),
   --min-value (0 = include unreported value), --region (NUTS prefix, e.g. DE2).
   Output fields: winner_name, winner_country, title, cpv_description (fallback when TED
   omits title, ~85% of awards), contract_value, currency, location, published_date,
   cpv_code, buyer_name, buyer_country, ted_url. Help text carries ICP guidance: CPV
   45500000 machinery hire, 45000000 all construction, 45200000 civil engineering,
   45310000 electrical/PV, 45230000 pipelines/power lines, 45210000 building; crane-implying
   keywords (Neubau, Rohbau, Hochbau, Stahlbeton, Brücke, Tunnel, Krankenhaus, Schulbau,
   Industriebau, Generalunternehmer); note that titles name what is built, not equipment.
2. `win-rate` — join cn-standard and can-standard by buyer: award rate and unique winners
   per buyer. --country --cpv --min-calls(3) --show-winners --since.
3. `score` — rank open tenders: deadline urgency 40 / log value 30 / keyword fit 30; future
   deadlines only. --country --cpv --keywords --limit(20) --max-days(60) --min-value.
4. `concentration` — winner share of awarded value + HHI (<1500 competitive, 1500–2500
   moderate, >2500 high). Groups by winner name AND country. --country --cpv --since --top(5).
5. `velocity` — weekly call/award counts over a window; --compare 1y for prior period;
   sparkline with --human-friendly. --country --cpv --window(90d) --compare.
6. `dark-buyers` — buyers with low award rate or single-winner patterns. --country --cpv
   --min-calls(3) --since.
7. `cpv-drift` — YoY CPV category growth/shrink in a country. --country --metric
   count|value --since (default 4y ago) --top(20).
8. `buyer` — contracting-authority dossier: cadence, CPV mix, typical values, date range,
   winner patterns (grouped by name+country). --name (partial, required) --show-winners --since.
9. `deadline-heat` — heat = days_left_inverse×0.5 + log_value×0.3 + 1/competition_density×0.2.
   --country --cpv --days(14) --min-value.

## Extraction truths baked into sync (see patches)
- Title fallback order: title-proc > title-lot > contract-title; ResolveMultilingual must
  accept both map[lang][]string and map[lang]string.
- FTS rows must be deleted before re-insert on upsert.
