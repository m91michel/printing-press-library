Acceptance Report: eu-tenders
  Level: Full Dogfood (live TED API, no auth)
  Tests: 159/159 passed (loop 3; loop 1 144/149, loop 2 158/159)
  Failures (fixed):
    - cpv search / winner error_path: unknown term returns [] exit 0 by design → pp:no-error-path-probe (CLI fix)
    - notices happy/json/dry-run: generated example passed all ~1,800 field names to --fields → overridden
      Example, Short/Long and pp:happy-args via tenders_root.go hook (CLI fix; Printing Press issue)
    - notices dry_run_json: client dry-run envelope lacked "action" → client.go now emits {"dry_run":true,
      "action":"POST /v3/notices/search"} (CLI fix; Printing Press issue: client.go.tmpl dryRun)
    - leads hollow coverage after dropping mcp:read-only → pp:live-happy-path; run with --allow-destructive,
      which only permits sandbox-local store writes (TED is read-only)
  Fixes applied: 5
  Printing Press issues: 2 (huge enum used for endpoint example/happy-args; dry-run envelope without action)
  Gate: PASS
