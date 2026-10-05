# Phase 4.95 local code review

Review path chosen: direct reviewer-subagent dispatch (correctness; security + maintainability), every round re-ran both.
Autofix summary: 41 findings autofixed in-place across 3 rounds (round 1: 5 correctness + 6 security + 12
maintainability; round 2: 3 correctness + 4 security + 3 maintainability; round 3: 2 security). Highlights:
`sql` multi-statement/ATTACH write bypass closed (single-statement validator + query_only read handle), read
commands no longer migrate the store, TED filter injection validated, --new-only marked unreturned leads,
runaway SQL bounded by --timeout, row/value/memory caps, SQLITE_LIMIT_LENGTH against zeroblob allocation.
Convergence outcome: round 3 correctness clean; round 3 security returned 2 findings (zeroblob allocation,
memory estimate) — fixed and verified in-session after the round-3 review (RSS 1.1 GB → 20 MB; 620 MB → 60 MB)
with a regression test. No further review round run (3-round cap).

## Template-shape / out-of-scope retro candidates
- store.go (generated) OpenReadOnly: "file:"+dbPath without URI escaping; `?`/`&` in --db can inject URI params.
- helpers.go (generated) declaredAgentSource maps data-source auto → "live" regardless of the branch taken.
- helpers.go (generated) error hints: "check your API credentials" and "Run the 'list' command" emitted for a
  no-auth API without a list command.
- Generated which.go examples ("stale tickets", "bottleneck") unrelated to the CLI.
- Novel-host check flags SQL column refs like `w.country` as hostnames (.country TLD).
- validate-narrative ran against a stale build/stage binary while scorecard refreshed it.
- Generator emits no sync/search/sql/--data-source for a single POST-search spec, and generic README/SKILL
  sections (raw OpenAPI HTML description, example-value placeholders, credentials.toml / auth boilerplate,
  wrong config slug ted-search) for a no-auth CLI.

## Surface-to-user findings
None.
