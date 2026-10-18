Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# Defer rendered-label queries until the map is idle

`FINE_LABEL_QUERY.patch` was applied, with Josh's explicit approval, to
`src/engine/weatherEngine.ts`. The production diff is exactly that patch. It
does not contain the optional forecast sampling patch.

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

The source build passes. The prepared Sites package embeds that build and its
matching Worker modules: 16 files, 5,250,945 bytes (Worker entry: 1,992,603 bytes).
No data was sealed or uploaded, and no code was deployed.

`b1_app_proof.mjs` exercises the actual packaged Worker and embedded app in
headless Chrome at 390 × 844 and 1440 × 1080. Both active and inactive runs cover
world July, Funchal, San Francisco phone, San Francisco laptop and San Diego,
then change the warmth slider. All ten views produce answers without runtime
exceptions or the earlier missing-data message. I opened and inspected the
San Francisco phone/laptop screenshots in both states and the Funchal and
San Diego laptop cards.

For inactive mode, both the empty private overlay and `artifacts` genuinely
lack `v6/fine/current.json`; the actual Worker reports `{enabled:false}`.
There is no stored-null substitution or availability override. No view makes
a coordinate replay request. The cards match the unchanged browser
`PointSampler` running in Node against the same published cells, records and
offsets. `legacy_browser_numbers.mjs` transpiles the real TypeScript modules;
it replaces only the DOM PNG loader with the Worker's byte decoder. It does
not copy or change the weather model. Active mode reports `{enabled:true}`;
its cards match the Worker's exact-pixel API.

Displayed July values are score / nice days / perfect days / awful days:

| Place and viewport | Fine active | Fine inactive (25 km) |
| --- | --- | --- |
| San Francisco phone | 75 / 27 / 8 / 0 | 98 / 31 / 30 / 0 |
| San Francisco laptop | 75 / 27 / 8 / 0 | 98 / 31 / 30 / 0 |
| Funchal laptop | 100 / 31 / 31 / 0 | 95 / 31 / 26 / 0 |
| San Diego laptop | 100 / 31 / 31 / 0 | 90 / 31 / 21 / 0 |

The inactive values retain the previously documented coarse temperature
errors, especially San Francisco; this fix changes label-query timing only.
There is also an existing inactive consumer discrepancy: the Worker link
preview uses the nearest 25 km point while the app blends the map's 4 × 4
footprint. Its July score / nice / perfect / awful values are SF
100 / 31 / 31 / 0, Funchal 96 / 31 / 27 / 0, and San Diego 88 / 31 / 19 / 0.
The inactive `/api/place` response has monthly facts but no period judgement.
The rendered check records this discrepancy rather than asserting false
preview parity. Fixing it is outside the approved engine patch.

Final evidence below `fine-validation/b1-worldwide/` on the SSD:

- `app-fixed-active/results.json`: packaged app/API parity, all five views.
- `app-fixed-inactive/results.json`: actual inactive status, published-model
  parity and separately recorded legacy preview numbers, all five views.
- In each folder, `san-francisco-phone-default.png` and
  `san-francisco-laptop-default.png` show the default card; `funchal-cold-default.png`
  and `san-diego-desktop-default.png` show the other checked places. Unqualified
  `.png` files show the result after the slider change.
- Request logs confirm inactive zero coordinate replay requests and active
  compact replay transport, without full native or correction tiles in the
  browser. Both catalogue label responses return 200 in both modes.

These are Mac headless viewport checks, not a physical-phone or remote-Sites
latency measurement. The original race was intermittent; the source now
queries labels only after MapLibre's `idle`, and the actual rebuilt cards pass
both required deployment states.
