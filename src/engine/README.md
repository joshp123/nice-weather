Written by AI on 2026.10.17 by model gpt-6.1-sol.

# Browser weather engine

[`index.ts`](index.ts) is the integration contract. Add the custom layer before the basemap's water layer after the style loads. WebGL2 and `EXT_color_buffer_float` are required.

```ts
import { createWeatherEngine, DEFAULT_TASTE } from './engine';
const engine = createWeatherEngine(map, {
  onLoadingChange: loading => showWeatherLoading(loading),
  onRedraw: () => updateCityLabels(),
});
map.addLayer(engine.layer, 'sea');
await engine.setTaste(DEFAULT_TASTE); // Also safe before the first period.
await engine.setPeriod({ kind: 'month', month: 11 });
const verdict = engine.judgePoint(36.72, -4.42);
const labels = engine.scorePoints([{ latitude: 36.72, longitude: -4.42 }]);
await engine.setPeriod({ kind: 'forecast' }); // 14 days, indexed 0..13.
const tomorrow = engine.judgePoint(36.72, -4.42, { kind: 'forecast-day', day: 1 });
```

## First paint and loading

For the exact default taste, the engine first loads the selected period's small score PNG, decodes it with `weatherLayer.ts`'s manual greyscale decoder, and draws it through the same palette, rounded bands, contours and cubic B-spline as the recounted grid. It uploads bytes directly as an R8 texture; no canvas decoding or colour conversion is involved. Sea/no-data bytes (255) are omitted from the spline and the remaining weights are normalised. The GPU uses the same mask and rounds default scores at fine points before the final spline, so both sources have integer precision. Other tastes retain the full floating-point grid.

For a cold period, only after that image frame completes do cells, day records and offsets start loading. If its records are already loaded (for example a day of a loaded forecast, or a month of a loaded year), the engine immediately draws from those records and judges points; the image loads separately without delaying the selection. All needed months and offsets start in parallel. A month never waits for a year. Forecast metadata is fetched first to pin images and records to the same run. A missing score image falls back to loading records directly; it is not cached as permanently missing. A moved taste starts with records, without waiting for an unusable default image.

Image names follow the shared contract: `climate/scores-january.png` … `scores-december.png`, `scores-dec-feb.png`, `scores-mar-may.png`, `scores-jun-aug.png`, `scores-sep-nov.png`, `scores-year.png`; forecast `<run>/scores-day-00.png` … `scores-day-13.png` and `scores-all.png`. Dimensions are 1440 × 721, row 0 = 90°N, column 0 = 180°W; land is 0..100 and sea/no data is 255.

`loading` becomes true synchronously when selecting a period and false when its records and offsets are available, or when loading fails. `onLoadingChange` reports changes to that state. **Every `judgePoint` call returns null while loading, including queries for previously cached periods.** A score image can already be visible during this time. `setPeriod` still resolves only after records and a completed frame; its rejection reports record failures. The latest selection wins, and superseded responses cannot restore an older map or loading state.

`setTaste` coalesces changes into the next frame and resolves after GPU completion. Before any period it stores the taste and resolves immediately. If records are still loading, moving taste clears the default image, reports the cleared frame through `onRedraw` so labels can clear too, and waits for records before resolving the taste promise. No previous period's records are used. Returning to the default can show the image again.

At the default taste, `scorePoints` reads the image's nearest fine land point directly, even while records load. Image values have integer precision; verdicts retain their unrounded means. With other tastes it reads the resolved GPU grid once per completed revision and caches that readback. Repeated map moves then sample memory, with no further GPU readback. Missing points yield NaN. Consumers should refresh labels on `onRedraw` and `onLoadingChange`.

## Point verdicts and scoring

The GPU scores each 1° cell at eleven felt shifts from −15 to +15 °C in 3 °C steps. Each fine point uses its own offset, interpolation between adjacent shift levels, then bilinear interpolation between four cell centres. Sea cells use the exact nearest land cell in wrapped grid coordinates. Only the displayed map applies the final fine-grid B-spline.

`judgePoint` and labels select the nearest land fine point in a 5 × 5 neighbourhood centred on the rounded requested coordinates: two fine steps in each direction. Distance is measured from the requested coordinate in fine-grid units, longitude wraps, and latitude must be −60..90°. Ties take the first row then column searched. The point must be land in every month needed by the period. With no such point the verdict is null. Its score, nice-day share, parts and physical averages use that point's own offset and the same level and cell interpolation as the map, without the final spline.

A period averages daily products; multiplying its averaged parts does not reproduce the score. Climate months have equal weight, with eight representative days each. Nice-day shares describe that sample; forecast nice-day counts are interpolated expected counts. Taste changes alter judgements, not physical averages.

`judgePoint(latitude, longitude, period?)` uses the current taste and only already-resolved files. Loading a season exposes its three months; a year exposes all months and seasons; a forecast exposes each of its 14 days. Queries neither fetch nor select nor redraw. Their cell judgements are cached per period and invalidated by taste changes. Runtime arity remains three for feature detection.

`PointVerdict.typical` remains optional in the type for compatibility and is always present on this engine's non-null verdicts:

| Field | Unit / meaning |
|---|---|
| `feelsInSunAt14C` | Felt temperature in sun at local 14:00, °C |
| `feelsInShadeAt21C` | Felt temperature in shade at local 21:00, °C |
| `dewPointC` | Dew point, °C |
| `rainMmPerDay` | Rain per sampled day, mm |
| `wetDayShare` | Share of days with at least 1 mm rain, 0..1 |
| `windKmh` | Wind, km/h |
| `sunnyShare` | Average daily sunny hours / daylight hours, 0..1 |
| `daylightHours` | Daylight, hours |

These use exactly the weighted sample behind `meanScore`. Felt offsets apply to the two felt temperatures. Figures inherit the published 15-byte records' quantisation and representative-day sampling. Place names come from the place API. Removing the layer releases its GPU resources.

## Default-score generation

```sh
src/engine/default_scores.py artifacts/v6
src/engine/default_scores.py artifacts/v6 --images /tmp/nwf-agents/v6b/engine/corrected-climate-images
```

The first form writes `climate/scores.bin`: 12,458,880 bytes, twelve monthly bytes per fine point in row-major order, January–December, 255 at sea. It imports the unchanged Python reference curves, scores the same eleven shift levels and resolves the same fine points. Rounding is `floor(score + 0.5)`.

The second form writes **only 17 PNGs to the specified output folder**. It retains unrounded monthly means until after averaging each season or year. Averaging bytes from `scores.bin` introduces a second rounding and does not reproduce a period verdict. Regenerate these files whenever records, offsets or the default rule change.

## Proof

All browser scripts use only `p.webkit.launch()` with its default headless mode. They do not open a visible browser or use computer-use tools. Chromium is not used for this round, following Josh's latest instruction.

Build the product, then an isolated proof bundle that concurrent product builds cannot erase:

```sh
devenv shell -- npm run build
devenv shell -- node_modules/.bin/vite build --config src/engine/vite.debug.config.ts --outDir /private/tmp/nwf-agents/v6b/engine/debug-build
cd /tmp
uv run --quiet --with playwright python -I /Users/josh/code/nice-weather-finder/src/engine/check_states.py
uv run --quiet --with playwright python -I /Users/josh/code/nice-weather-finder/src/engine/check_browser.py --real
uv run --quiet --with numpy python -I /Users/josh/code/nice-weather-finder/src/engine/check_reference.py /tmp/nwf-agents/overnight/engine/webkit-real-reference.json
uv run --quiet --with playwright --with numpy --with pillow python -I /Users/josh/code/nice-weather-finder/src/engine/check_images.py
uv run --quiet --with playwright --with numpy --with pillow python -I /Users/josh/code/nice-weather-finder/src/engine/check_images.py /tmp/nwf-agents/v6b/engine/corrected-climate-images
uv run --quiet --with playwright python -I /Users/josh/code/nice-weather-finder/src/engine/check_loading.py after
```

For a manually opened debug page, omit the `--outDir` override when building and use `/engine-debug/debug.html` on the existing port 5198. Its sliders, period selector, verdict and GPU frame timing use real data. `?data=/data/v6-synthetic` selects the separate fixture; `synthetic.py` must write that fixture to a separate SSD directory, never into real v6 data.

`check_states.py` holds records while the real image paints, checks a cached month draws with its image held, scores 400 actual city points 30 times without a GPU readback, changes taste while loading, checks null verdicts (including cached periods), parallel year requests, superseded selections, missing-image fallback and record failure. `check_browser.py` covers all sliders, cached months/seasons/forecast days, 48 city/month comparisons with scores.bin, actual GPU fine-point scores, renderer identity, month/year redraws and screenshots. `check_reference.py` applies Python curves independently to the exact weighted samples exported by the browser.

`check_loading.py before|after [paths...]` visits the actual product at 1512 × 982, 2×, using fresh contexts and a shared 500,000 bytes/s response-body budget plus 100 ms response latency. It includes partial transfers when counting bytes before colour. Basemap and font traffic share that budget. Only HTTP(S) requests are intercepted; MapLibre’s in-memory worker script runs normally. Responses are released whole after throttling; this is a conservative local response model, not a mobile-radio simulation. First colour is observed from the weather draw followed by two animation frames. The script clicks the map as soon as its controls are available and measures the actual scored card appearing in the DOM; `cardReady` separately records the first scoring pass. The baseline uses a frozen build of the previously committed app in `/tmp/nwf-agents/v6b/engine/before-build`; the current build also includes the interface owner's concurrent changes. Each run snapshots its assets to avoid mixed builds.

Current results and remaining image-data issue: `/tmp/nwf-agents/v6b/engine/REPORT.md` (also copied to the original overnight report path). Product load screenshots, timing/resource JSON, loading-state proof and image/GPU comparisons are alongside it. Reference samples and detailed redraw proof remain in `/tmp/nwf-agents/overnight/engine/`.

Measured on 2026-10-17 in default-headless WebKit, with the throttle and viewport above:

| Route | First colour, before → after | Bytes before colour | Scored card, before → after |
|---|---:|---:|---:|
| `/` | 14.26 → 4.12 s | 6.33 → 1.78 MB | 14.14 → 15.27 s |
| `/november` | 10.05 → 3.28 s | 4.65 → 1.42 MB | 10.04 → 11.73 s |
| `/year` | 64.11 → 3.39 s | 31.25 → 1.51 MB | 64.07 → 65.38 s |

The published climate season/year PNGs currently average already-rounded months. Their five aggregate images require regeneration with `default_scores.py --images` before claiming parity with the unrounded period mean. Corrected files are prepared in the report folder; published data was not modified. Corrected November/year comparisons leave only 19/52 changed pixels out of 5,939,136 from float32 half-integer rounding. Full forecast and every day were also compared. Current Python checks agree within 4.3e-14, and all 48 city/month table checks pass. See the report for the complete evidence and remaining data handoff.
