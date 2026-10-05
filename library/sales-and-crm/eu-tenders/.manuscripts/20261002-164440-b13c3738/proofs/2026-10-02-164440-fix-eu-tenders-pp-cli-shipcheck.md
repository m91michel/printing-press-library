# eu-tenders-pp-cli shipcheck

## Loop 1 (exit 3): 5/7 legs
- validate-narrative FAIL: stale staged binary (build/stage/bin built 17:33, before final build; scorecard
  later rebuilt it at 17:57). Flagged "unknown flag --new-only/--group-by" and "sync missing" — all exist.
  Retro candidate: validate-narrative should refresh the staged binary like scorecard --live-check does.
- dogfood FAIL: 7 dead generated helpers (compactFields, handleBinaryResponseDelivery,
  paginatedGetWithResponsePath, readSecretFromStdin, retainCLIQueryParams, retainExplicitQueryParams,
  successfulNoop) and novel-host check: ted.europa.eu (notice URLs) not in research artifacts;
  false positive "w.country" (SQL alias read as a .country hostname — retro candidate).
- scorecard live probe 8/10: winner and incumbents examples returned nothing on an empty store.

## Fixes
- Removed the 7 unused helpers from internal/cli/helpers.go.
- Added $API_RUN_DIR/discovery/documented-urls.txt (TED search API, spec, docs, notice detail URL).
- winner.go: quoted column w."country" in two filter strings.
- winner + incumbents: live TED fallback via a temporary store when the local store lacks the data
  (data-source auto); tests pinned to --data-source local so unit tests never hit the network.
- research.json incumbents example switched to a real open tender (679227-2026).

## Loop 2 (exit 0): 7/7 legs PASS
verify PASS 100% (43/43), validate-narrative PASS, dogfood PASS (11/11 novel), workflow-verify PASS,
verify-skill PASS, scorecard 86/100 Grade A, live sample probe 10/10.

Scorecard before (prior CLI, Printing Press 4.0.6) → after: 75 → 86. mcp_remote_transport 0→10,
mcp_tool_design 0→10, sync_correctness 2→7, dead_code 0→5, readme 8→10, output_modes 9→10.

## Behavioral sample (every novel command run against real data)
leads (local + live AUT), score (local + live AUT), deadline-heat, buyer, winner (live: Johann Bunte
45 wins), incumbents (local + live), concentration (DEU 45: HHI 475.7 competitive), win-rate,
dark-buyers, velocity (zero-count weeks present), cpv-drift. Outputs are in the build log and
subagent reports.

Verdict: ship
