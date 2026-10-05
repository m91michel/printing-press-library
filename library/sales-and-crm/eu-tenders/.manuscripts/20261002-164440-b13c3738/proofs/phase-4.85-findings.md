# Phase 4.85 output review — status WARN (3 warnings, all fixed)
Sample: scorecard live-check 10 pass / 1 skip; store-backed commands reviewed on empty store (hint printed).
1. incumbents meta.source "local" on live fallback — annotation set to auto. FIXED.
2. deadline-heat --cpv 45 ranked a fuel-card framework (main CPV 66172000, secondary 45259000) #1 —
   live paths now require the main CPV under the filter prefix (ted.PrimaryCPVMatches), matching the
   local-store filter. FIXED; live rerun shows only 45xxxxxx main codes.
3. deadline-heat/score crowded by same-day deadlines with unknown value — both now rank from tomorrow
   (days_left >= 1). FIXED.
Reviewer note: reviewed inline by the output-review skill (no separate reviewer agent dispatched).
