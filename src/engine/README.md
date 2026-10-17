Written by AI on 2026.10.17 by model gpt-6.1-sol.

# Browser weather engine

`index.ts` is the integration contract. Add its custom layer before the basemap's water layer, after the map style loads. Select a period before awaiting a taste change.

```ts
import { createWeatherEngine, DEFAULT_TASTE } from './engine';
const engine = createWeatherEngine(map);
map.addLayer(engine.layer, 'sea');
await engine.setPeriod({ kind: 'month', month: 11 });
const timing = await engine.setTaste(DEFAULT_TASTE);
const verdict = engine.judgePoint(36.72, -4.42);
const labelScores = engine.scorePoints([{ latitude: 36.72, longitude: -4.42 }]);
await engine.setPeriod({ kind: 'forecast' }); // 14 days, indexed 0..13
const tomorrow = engine.judgePoint(36.72, -4.42, { kind: 'forecast-day', day: 1 });
// No fetch or map change; null if that period's files have not loaded.
const feelsAt14 = tomorrow?.typical?.feelsInSunAt14C;
```

The layer needs WebGL2 and `EXT_color_buffer_float` (supported by the tested WebKit and Chromium Metal backends). It honours MapLibre's depth so opaque water covers weather colours. On removal it releases its GPU resources.

A selected month fetches only `cells.bin`, that month's eight records per cell, and its offsets PNG. Months remain cached. Seasons and the year reuse those files; forecasts reuse their one records file and only the offset months their dates need. PNG decoding calls the existing manual greyscale decoder, without a canvas.

The GPU scores every cell at eleven felt shifts from −15 to +15 °C in 3 °C steps. Those levels cover every encoded offset. A second pass resolves each fine pixel from its monthly offset, the adjacent levels, then the four cell centres. Sea cells have the exact nearest land ordinal in wrapped grid coordinates. The final map uses the existing cubic B-spline, six bands, palette, edge antialiasing and contour lines.

After a completed frame, `scorePoints` reads the resolved GPU grid once per revision and caches it for inexpensive label updates. Point verdicts and city labels select the nearest fine land point in the 5 × 5 neighbourhood centred on the rounded requested coordinate (two fine steps each way), with longitude wrapping. Distance is measured from the requested coordinate in fine-grid units; ties take the first row then column searched. The selected point must have a non-255 offset in every month needed by the period. Without one, `judgePoint` returns `null` and `scorePoints` returns `NaN`. They use that point's own monthly offset, then the same felt-level and four-cell bilinear weights as the GPU, **without the final fine-grid B-spline**. This applies to the score, nice-day share, parts and physical averages. Only the map uses the final spline for rounded bands. The period's score is the mean of daily products; multiplying the averaged parts does not reproduce it. Climate nice-day shares describe the representative sample. Forecast nice-day counts are interpolated expected counts. The engine also interpolates land values under water; the basemap masks them. The place API is responsible for geographical place names.

`judgePoint(latitude, longitude, period?)` defaults to the selected map period. An explicit period uses only already-resolved records and offsets, with the current taste. Loading a season exposes its three months; loading the year exposes all twelve months and all seasons; loading the whole forecast exposes every day 0..13. Other combinations also work whenever all required files are loaded. Unavailable periods return `null`. Queries neither fetch nor redraw nor alter the selected period. Their cell judgements are cached separately and invalidated by `setTaste`. The method's runtime arity is three for interface feature detection.

`PointVerdict.typical` is optional in the public type for backwards compatibility, and always present on this engine's non-null verdicts:

| Field | Meaning / unit |
|---|---|
| `feelsInSunAt14C` | Felt temperature in the sun at local 14:00, °C |
| `feelsInShadeAt21C` | Felt temperature in shade at local 21:00, °C |
| `dewPointC` | Dew point, °C |
| `rainMmPerDay` | Rain, mm per sampled day |
| `wetDayShare` | Share of sampled days with at least 1 mm rain, 0..1 |
| `windKmh` | Wind, km/h |
| `sunnyShare` | Average of each day's sunny hours / daylight hours, 0..1 |
| `daylightHours` | Daylight, hours |

These are weighted averages over exactly the records and interpolation used by `meanScore`. Felt offsets apply to the two felt temperatures, not dew point or the other measurements. Taste changes affect the judgement, not these physical figures. The values inherit the published 15-byte records' quantisation and representative-day sampling; they are not a second source of raw observations.

`setTaste` coalesces changes before the next MapLibre frame and resolves after a GPU completion fence. Its duration includes waiting for that frame and GPU execution. Replacing a pending period selection leaves the latest selection active. A failed file fetch rejects the selection and can be retried.

## Default scores for link previews

```sh
src/engine/default_scores.py artifacts/v6
```

The executable uses `uv` with NumPy and Pillow. It reads the published compact monthly records and greyscale offsets and imports the unchanged Python reference curves. It scores each cell's eight days at the same eleven felt shifts as the browser, then resolves each fine land point with its own offset, the same level interpolation and four cell-centre weights. Sea cells use the same wrapped nearest-land ordinals as the browser. It does not apply the map spline.

The output is `v6/climate/scores.bin`: **12,458,880 bytes**, 1440 × 721 points in row-major order, twelve bytes per point (January–December). Row 0 is 90°N, column 0 is 180°W. Sea/no-data values are 255; land values are the default-taste mean rounded with `floor(score + 0.5)`, matching `Math.round` for nonnegative scores. Regenerate after updating the published records, offsets or default scoring rule. The browser continues to calculate arbitrary tastes; this table serves the preview's default taste.

## Debug and proof

Build the isolated debug page (does not change the product build):

```sh
devenv shell -- node_modules/.bin/vite build --config src/engine/vite.debug.config.ts
```

With the normal server on port 5198, open `/engine-debug/debug.html` for real files or `/engine-debug/debug.html?data=/data/v6-synthetic` for the separate fixture. Generate that fixture on the SSD:

```sh
python3 src/engine/synthetic.py '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/v6' '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/engine-synthetic-v6'
```

`artifacts/v6` and `artifacts/v6-synthetic` must point at those separate directories. Synthetic days are never written into the real data directory.

Browser proof on the real data:

```sh
cd /tmp
uv run --quiet --with playwright python -I /Users/josh/code/nice-weather-finder/src/engine/check_browser.py --real
uv run --quiet --with numpy python -I /Users/josh/code/nice-weather-finder/src/engine/check_reference.py /tmp/nwf-agents/overnight/engine/chromium-real-reference.json /tmp/nwf-agents/overnight/engine/webkit-real-reference.json
```

Without `--real`, the browser check uses the separate full-size synthetic set. Both modes measure twelve month and year redraws, record renderer identity, capture screenshots, exercise every forecast day, all seasons and all six sliders, verify actual GPU fine-point values and city labels against point verdicts and export the exact weighted day samples. They also verify cached-period queries leave the map and request count unchanged, month/day aggregation agrees with its parent period, and cached judgements follow taste changes. With `--real`, all twelve months at Málaga, Lisbon, Rotterdam and Cape Town must also match the generated `scores.bin`, after rounding. An open-ocean query must return no verdict or label score. The independent Python check imports the reference curves from `research/taste/nice_v6.py` and checks every returned part, score, nice-day share and physical average. Results go to `/tmp/nwf-agents/overnight/engine/`.

Verified on 2026-10-17 with the real one-year climate files (2021-09 to 2022-09; eight representative days/month, 18,667 cells) and forecast run `20261016T0000Z` (14 days from 2026-10-16). On Apple M4 Pro, hardware Metal, 1512 × 982, twelve taste redraws per period:

| Browser | November median / worst | Year median / worst | 100 city labels, year |
|---|---|---|---|
| Chromium | 8.6 / 16.7 ms | 15.3 / 16.3 ms | 10.9 ms |
| WebKit | 16.0 / 47.0 ms | 17.0 / 33.0 ms | 4.0 ms |

Median here matches the debug page's upper middle sample. Redraw timing runs from `setTaste` through GPU completion of the MapLibre frame. No network is needed. Real-data month/year screenshots from both browsers were inspected. Map shaders and rendering remain unchanged by the point-sampling change. The three reference scores are Málaga/November **54.033051745**, Rotterdam/January **0.049356459**, Singapore/July **10.041952888**. Every part, nice share and physical average agrees with Python within **1.5e-14**; GPU fine-point and label scores differ from point means by less than **0.000015** points. Additional real year, whole-forecast and final-forecast-day reference comparisons pass. No page or WebGL errors.

The generator ran on `artifacts/v6` in **2.9 seconds**, writing all **250,011** land points for each month and 255 elsewhere. All **48 city/month combinations** match `Math.round(judgePoint(...).meanScore)` in both browsers. The specified interpolation gives Málaga/November **54 (Fair)** in both the regenerated table and the card; it does not retain the old preview's 59. Preview publishing is outside this engine change.
