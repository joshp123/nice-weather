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

For the exact default taste, the engine first loads the selected period's small score PNG, decodes it with `greyscaleImage.ts`'s manual greyscale decoder, and draws it through the same palette, rounded bands, contours and cubic B-spline as the recounted grid. It uploads bytes directly as an R8 texture; no canvas decoding or colour conversion is involved. Sea/no-data bytes (255) are omitted from the spline and the remaining weights are normalised. The GPU uses the same mask and rounds default scores at fine points before the final spline, so both sources have integer precision. Other tastes retain the full floating-point grid.

For a cold period, only after that image frame completes do cells, day records and offsets start loading. If its records are already loaded (for example a day of a loaded forecast, or a month of a loaded year), the engine immediately draws from those records and judges points; the image loads separately without delaying the selection. All needed months and offsets start in parallel. A month never waits for a year. Forecast metadata is fetched first to pin images and records to the same run. A missing score image falls back to loading records directly; it is not cached as permanently missing. A moved taste starts with records, without waiting for an unusable default image.

Image names follow the shared contract: `climate/scores-january.png` … `scores-december.png`, `scores-dec-feb.png`, `scores-mar-may.png`, `scores-jun-aug.png`, `scores-sep-nov.png`, `scores-year.png`; forecast `<run>/scores-day-00.png` … `scores-day-13.png` and `scores-all.png`. Dimensions are 1440 × 721, row 0 = 90°N, column 0 = 180°W; land is 0..100 and sea/no data is 255.

`loading` becomes true synchronously when selecting a period and false when its records and offsets are available, or when loading fails. `onLoadingChange` reports changes to that state. **Every `judgePoint` call returns null while loading, including queries for previously cached periods.** A score image can already be visible during this time. `setPeriod` still resolves only after records and a completed frame; its rejection reports record failures. The latest selection wins, and superseded responses cannot restore an older map or loading state.

`setTaste` coalesces changes into the next frame and resolves after GPU completion. Before any period it stores the taste and resolves immediately. If records are still loading, moving taste clears the default image, reports the cleared frame through `onRedraw` so labels can clear too, and waits for records before resolving the taste promise. No previous period's records are used. Returning to the default can show the image again.

At the default taste, `scorePoints` reads the image's nearest fine land point directly, even while records load. Image values have integer precision; verdicts retain their unrounded means. With other tastes it reads the resolved GPU grid once per completed revision and caches that readback. Repeated map moves then sample memory, with no further GPU readback. Missing points yield NaN. Consumers should refresh labels on `onRedraw` and `onLoadingChange`.

## Point verdicts and scoring

The GPU scores each 1° cell at eleven felt shifts from −15 to +15 °C in 3 °C steps. Each fine point uses its own offset, interpolation between adjacent shift levels, then bilinear interpolation between four cell centres. Sea cells use the exact nearest land cell in wrapped grid coordinates. Only the displayed map applies the final fine-grid B-spline.

`judgePoint` and labels select the nearest land fine point in a 5 × 5 neighbourhood centred on the rounded requested coordinates: two fine steps in each direction. Distance is measured from the requested coordinate in fine-grid units, longitude wraps, and latitude must be −60..90°. Ties take the first row then column searched. The point must be land in every month needed by the period. With no such point the verdict is null. Its score, nice-day share, parts and physical averages use that point's own offset and the same level and cell interpolation as the map, without the final spline.

A period averages daily products; multiplying its averaged parts does not reproduce the score. Climate months have equal weight, with eight representative days each. Nice-day shares describe that sample; forecast nice-day counts are interpolated expected counts. Taste changes alter judgements, not physical averages.

`judgePoint(latitude, longitude, period?)` uses the current taste and only already-resolved files. Loading a season exposes its three months; a year exposes all months and seasons; a forecast exposes each of its 14 days. Queries neither fetch nor select nor redraw. Their cell judgements are cached per period and invalidated by taste changes.

`PointVerdict.typical` is present on every non-null verdict:

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
src/engine/default_scores.py artifacts/v6 --images artifacts/v6/climate
```

The first form writes `climate/scores.bin`: 12,458,880 bytes, twelve monthly bytes per fine point in row-major order, January–December, 255 at sea. It imports the unchanged Python reference curves, scores the same eleven shift levels and resolves the same fine points. Rounding is `floor(score + 0.5)`.

The second form writes **only 17 PNGs to the specified output folder**. It retains unrounded monthly means until after averaging each season or year. Averaging bytes from `scores.bin` introduces a second rounding and does not reproduce a period verdict. Regenerate these files whenever records, offsets or the default rule change.

## Verification

Build with `devenv shell -- npm run build`, serve with `devenv shell -- npm run serve`, then use the product at `http://127.0.0.1:5198` in headless Chromium or WebKit. Check a climate month, season and year, the forecast and a forecast day, city cards, and taste changes. Compare place scores with the published files and the Python reference curves.
