Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# Defer rendered-label queries until the map is idle

`FINE_LABEL_QUERY.patch` is prepared, not applied. It changes only
`src/engine/weatherEngine.ts`. It does not contain the optional forecast
sampling patch.

The failing San Francisco phone trace enters `fineCandidates` from
`countPoints` during `onLoadingChange`, then fails in MapLibre's symbol feature
lookup. The error escapes into period loading, producing the misleading
“weather data is missing” message. The required weather files returned 200.
`src/ui/map.ts` replaces the `city-scores` GeoJSON on every label refresh.

Five subsequent fresh San Francisco phone loads did not reproduce the
exception, including the original world → Funchal → San Francisco sequence.
The failure is intermittent; its original trace did not record source-loading
state. A separate instrumented SF load recorded 222 candidate queries, 220
while `city-scores` was not loaded. This establishes premature querying, but
does not establish the source flag at the original failing instant.

The patch retains the existing top-128 overview candidates and adds the last
actual rendered-label slug set. It queries that set only on MapLibre `idle`,
after sources, movement and symbol placement settle. A changed set requests
one redraw, allowing newly displayed labels outside the top 128 to fetch
exact replay. An unchanged set does not request another redraw. There is no
exception suppression or weather fallback.

The same change was injected into the existing built JavaScript response in
an isolated headless browser, without modifying `src` or `dist`. The original
and changed bundle both exercised world desktop, Funchal desktop and San
Francisco phone, including a taste-slider change. Cards, headlines and slider
results were identical. The changed run made 18 idle label queries; every
query observed both source-loaded and tiles-loaded. Every final rendered
label had a native replay request: 23 world labels, 22 Funchal-view labels,
and 6 SF-view labels, none missing. Transient label placement and request
ordering can differ: Funchal fetched one additional place; SF fetched the
same number with two intermediate candidates exchanged. Published numbers
still use the exact same shared function and coordinates.

Evidence under the SSD's `fine-validation/b1-worldwide/`:

- `app-complete/browser-events.json`: original failure stack.
- `candidate-query-race/results.json`: three fresh SF loads.
- `candidate-query-sequence/results.json`: original navigation sequence.
- `candidate-query-state/results.json`: source-loading counters.
- `candidate-query-idle/results.json`: in-memory patch run and idle states.
- Those folders also contain request logs and rendered screenshots.

`git apply --check research/fine/FINE_LABEL_QUERY.patch` passes. The source
owner must approve/apply it, then rebuild and repeat the actual complete app
proof before this is a release candidate. The in-memory test does not replace
that final source-build proof.
