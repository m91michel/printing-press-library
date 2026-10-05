# Novel features brainstorm (subagent output, eu-tenders reprint 2026-10-02)

## Customer model

**Lena — inside-sales rep, construction-machinery rental (Munich).** Today: TED web search + Apify lead-finder/SaaS export; copies winner names to a spreadsheet, googles phone/email; multi-lot notices list contacts in a different order than winner names so she sometimes calls the wrong firm; no memory of who she contacted last week. Weekly ritual: Monday pull of last week's CPV 45 awards in DE2/DE1, filter by crane-implying keywords, push to CRM with contacts, call biggest new winners first. Frustration: turning an award notice into a callable lead by hand without duplicates.

**Jonas — bid manager, civil-engineering contractor.** Today: saved TED searches + CSV/Excel sorting; opens old notices one by one to find the incumbent. Ritual: twice-weekly sweep of open calls (14–60 day deadlines), rank by urgency/value/fit, qualify 2–3. Frustration: ranking 200 calls and seeing the incumbent for each.

**Dr. Aylin Kaya — procurement market analyst.** Today: ad-hoc Python fighting ITERATION pagination, multilingual shapes, 1,800 fields; rebuilds pandas notebooks for share/HHI/YoY each quarter. Ritual: refresh country×CPV slice, recompute concentration/velocity/CPV growth, write market notes. Frustration: re-deriving the same aggregates with no persistent corpus.

**Marek — data journalist / integrity researcher.** Today: Apify award-integrity feeds + manual lookups; would need his own DB to join calls to awards across years. Ritual: screen buyers for anomalous award patterns, build dossiers. Frustration: cn↔can join per buyer over years.

## Candidates (pre-cut)

C1 leads (prior-keep, user vision, index-alignment) · C2 score (prior-keep) · C3 deadline-heat (prior-keep) · C4 win-rate (prior-keep) · C5 dark-buyers (prior-keep) · C6 concentration (prior-keep) · C7 velocity (prior-keep) · C8 cpv-drift (prior-keep) · C9 buyer (prior-keep) · C10 winner <name> (persona/cross-entity) · C11 incumbents <publication-number> (persona/cross-entity) · C12 newcomers · C13 winner-graph (prior manifest, unbuilt) · C14 peer-benchmark (prior manifest, unbuilt) · C15 regions · C16 upcoming-awards · C17 digest (Slack/CRM — external service fail) · C18 contacts <publication-number>.

Inline checks: no LLM dependency; C17 fails external-service check; all unauthenticated read-only; C13/C14/C16 scope-creep or verifiability risk; C5 low-confidence heuristic kept on prior grounds; all survivors read notices/notice_winners or call live search — no synthesized payloads.

## Survivors and kills

### Survivors

| # | Feature | Command | Score | Buildability | Long Description |
|---|---------|---------|-------|--------------|------------------|
| 1 | Construction Leads | leads | 10 | hand-code | Use this command to get a contactable outreach list of companies that recently won contracts. Do NOT use it to profile one known company's history; use 'winner' instead. Do NOT use it for a plain award table without contacts; use 'awards' instead. |
| 2 | Open-tender ranker | score | 9 | hand-code | Use this command to rank open tenders by fit to your keywords plus value and urgency. Do NOT use it to find tenders closing soon in under-competed markets; use 'deadline-heat' instead. |
| 3 | Buyer dossier | buyer | 9 | hand-code | Use this command to profile one contracting authority. Do NOT use it for market-wide share; use 'concentration' instead. Do NOT use it to profile a winning company; use 'winner' instead. |
| 4 | Market concentration | concentration | 8 | hand-code | Use this command for market-level winner share and HHI in a country/CPV slice. Do NOT use it for one buyer's winners; use 'buyer' instead. Do NOT use it for one company's wins; use 'winner' instead. |
| 5 | Winner dossier | winner | 8 | hand-code | Use this command to profile one winning company. Do NOT use it for a list of new leads; use 'leads' instead. Do NOT use it for an authority; use 'buyer' instead. |
| 6 | Tender incumbents | incumbents | 7 | hand-code | Use this command to see who previously won from the same buyer for one specific open tender. Do NOT use it for a buyer's full profile; use 'buyer' instead. |
| 7 | Deadline heat | deadline-heat | 7 | hand-code | Use this command to find open tenders closing within days where few competitors usually bid. Do NOT use it for keyword-fit ranking; use 'score' instead. |
| 8 | Buyer award rate | win-rate | 7 | hand-code | Use this command to list award rate and winner diversity for every buyer in a market. Do NOT use it to flag only anomalous buyers; use 'dark-buyers' instead. |
| 9 | Dark buyers | dark-buyers | 7 | hand-code | Use this command to flag buyers with suspicious award patterns (low award rate, single repeat winner). Do NOT use it for the full per-buyer award-rate table; use 'win-rate' instead. |
| 10 | Award velocity | velocity | 6 | hand-code | none |
| 11 | CPV drift | cpv-drift | 6 | hand-code | none |

Scores (Domain Fit/User Pain/Build Feasibility/Research Backing): leads 3/3/2/2; score 3/2/2/2; buyer 3/2/2/2; concentration 3/2/2/1; winner 3/2/2/1; incumbents 2/2/2/1; deadline-heat 2/2/2/1; win-rate 2/2/2/1; dark-buyers 2/1/2/2; velocity 2/1/2/1; cpv-drift 2/1/2/1.

### Killed candidates

| Feature | Kill reason | Closest surviving sibling |
|---------|-------------|---------------------------|
| newcomers | Reachable via `leads --new-only --group-by company` + `winner`; splitting one workflow. | leads |
| winner-graph | Edge list is one `sql` over notice_winners; analyses already in concentration/buyer. | concentration |
| peer-benchmark | Scope creep; comparing two buyer dossiers covers it. | buyer |
| regions | Single GROUP BY via sql; `leads --region` covers territory filtering. | leads |
| upcoming-awards | cn→can matching depends on sparse previous-notice-id; unverifiable; 4/10. | leads |
| digest | Needs Slack/CRM outside the spec; `leads --new-only --csv/--json` piped covers it. | leads |
| contacts | Duplicates leads rows for one notice + `notices get`. | leads |

## Reprint verdicts

All nine prior features: **keep** — leads (10, enriched with contacts, --group-by company, --new-only), score (9), deadline-heat (7), win-rate (7), dark-buyers (7, low-confidence heuristic), concentration (8, name+country grouping), velocity (6), cpv-drift (6), buyer (9).
