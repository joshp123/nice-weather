Written by AI on 2026.10.18 by model GPT-6.1 Sol.

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

For the exact default taste, the engine first loads the selected period's small score PNG, decodes it with `greyscaleImage.ts`'s manual greyscale decoder, and draws it through the same palette, bands, contours and cubic B-spline as the recounted grid. It uploads bytes directly as an R8 texture; no canvas decoding or colour conversion is involved. Sea/no-data bytes (255) are omitted from the spline and the remaining weights are normalised. The image has floored integer precision. As soon as records arrive the live floating-point GPU grid replaces it, including at the default taste. Both sources use the same land mask.

For a cold period, only after that image frame completes do cells, day records and offsets start loading. If its records are already loaded (for example a day of a loaded forecast, or a month of a loaded year), the engine immediately draws from those records and judges points; the image loads separately without delaying the selection. All needed months and offsets start in parallel. A month never waits for a year. Forecast metadata is fetched first to pin images and records to the same run. A missing score image falls back to loading records directly; it is not cached as permanently missing. A moved taste starts with records, without waiting for an unusable default image.

Image names follow the shared contract: `climate/scores-typical-day-january.png` … `scores-typical-day-december.png`, `scores-typical-day-dec-feb.png`, `scores-typical-day-mar-may.png`, `scores-typical-day-jun-aug.png`, `scores-typical-day-sep-nov.png`, `scores-typical-day-year.png`; forecast `<run>/scores-typical-day-day-00.png` … `scores-typical-day-day-13.png` and `scores-typical-day-all.png`. Dimensions are 1440 × 721, row 0 = 90°N, column 0 = 180°W; land is 0..100 and sea/no data is 255.

Both generators and the forecast publisher use the `scores-typical-day-` filenames. A query string alone cannot distinguish old stored mean-score bytes. These images give the first frame; live records replace them once loaded. Runs covering one month or all twelve reuse the corresponding month or year image.

`loading` becomes true synchronously when selecting a period and false when its records and offsets are available, or when loading fails. `onLoadingChange` reports changes to that state. **Every `judgePoint` call returns null while loading, including queries for previously cached periods.** A score image can already be visible during this time. `setPeriod` still resolves only after records and a completed frame; its rejection reports record failures. The latest selection wins, and superseded responses cannot restore an older map or loading state.

`setTaste` coalesces changes into the next frame and resolves after GPU completion. Before any period it stores the taste and resolves immediately. If records are still loading, moving taste clears the default image, reports the cleared frame through `onRedraw` so labels can clear too, and waits for records before resolving the taste promise. No previous period's records are used. Returning to the default uses live records whenever they are loaded.

`scorePoints` reads the live resolved GPU grid once per completed revision and applies the same normalized spatial weights as the map fragment. Repeated map moves then sample memory, with no further GPU readback. The preview image is a source only before records load. Missing points yield NaN. Consumers should refresh labels on `onRedraw` and `onLoadingChange`.

## Point verdicts and scoring

The GPU scores each 1° cell at eleven felt shifts from −15 to +15 °C in 3 °C steps. The owning cell is floor(fine row / 4), floor(fine column / 4), matching the cell used to produce its temperature offset. Each fine point uses its own offset and its owning cell, interpolating each day score between adjacent shift levels. Sea cells use the exact nearest land cell in wrapped grid coordinates. `countPoints` reads the same GPU grid once per revision and returns packed nice, perfect and awful counts for each named place. The displayed map, named scores, counts and card use the same final fine-grid B-spline.

The overview field uses a 4 × 4 cubic B-spline footprint around the requested coordinate. Points missing in any selected month are omitted and the remaining spatial weights are normalized. Each native fine-grid point first resolves its own representative days and calendar weights. The card then combines the complete point verdicts with those same spatial weights: typical-day score, day counts, factors and physical averages. It does not substitute a filtered score into an unrelated nearest-point card. Point verdicts and grid verdicts are cached per sampler.

When the fine-place API is enabled, its shared exact-pixel `placeNumbers` output is authoritative. Each loaded 30 arc-second pixel is also drawn at its actual geographic cell area on the map, in one batched draw below the basemap water. These are data cells, not markers or invented radii. No surrounding area is claimed to have 1 km data until it is loaded. The fine-cell buffer updates after a packet, period or taste change. The card remains pending while its exact packet is unavailable.

Each day scores the product of its weather factors. A stretch uses the weighted median day: its band is the highest threshold reached by at least half its days. Interpolating the threshold shares where they cross one half gives a continuous value for colouring. A single forecast day keeps its score. Climate months carry their real calendar-day weights, with eight representative days each. Counts estimate nice-or-better, perfect and awful days out of the calendar length; a forecast counts its actual days. Taste changes alter judgements, not physical averages.

`judgePoint(latitude, longitude, period?)` uses the current taste and only already-resolved files. Loading a season exposes its three months; a year exposes all months and seasons; a forecast exposes each of its 14 days. Overview queries neither fetch nor select nor redraw. Exact-pixel queries request their packet on first use and redraw when it arrives. Judgements are cached per period and taste.

`PointVerdict.typical` is present on every non-null verdict:

| Field | Unit / meaning |
|---|---|
| `feelsInSunAt14C` | Felt temperature in sun at local 14:00, °C |
| `feelsInShadeAt15C` | Felt temperature in shade at local 15:00, °C |
| `feelsInShadeAt21C` | Felt temperature in shade at local 21:00, °C |
| `dewPointC` | Dew point, °C |
| `rainMmPerDay` | Rain per sampled day, mm |
| `wetDayShare` | Share of days with at least 1 mm rain, 0..1 |
| `windKmh` | Wind, km/h |
| `sunnyShare` | Average daily sunny hours / daylight hours, 0..1 |
| `daylightHours` | Daylight, hours |

These use exactly the weighted sample behind `meanScore`. Felt offsets apply to the three felt temperatures. Figures inherit the published 15-byte records' quantisation and representative-day sampling. Place names come from the place API. Removing the layer releases its GPU resources.

## Default-score generation

```sh
src/engine/default_scores.py artifacts/v6
src/engine/default_scores.py artifacts/v6 --images /tmp/nice-typical-scores
```

The first form writes `climate/scores.bin`: 12,458,880 bytes, twelve monthly bytes per fine point in row-major order, January–December, 255 at sea. It imports the unchanged Python reference curves, scores the same eleven shift levels and resolves the same fine points. Integer scores use `floor(score)` so quantisation cannot cross a band threshold.

The second form writes 133 PNGs and `scores.bin` to the specified output folder, including every consecutive month run, the seasons and year. It retains daily threshold shares until after weighting each period. Monthly bytes alone cannot reproduce a longer period's typical day or counts. Regenerate these files whenever records, offsets or the default rule change.

## Verification

Build with `devenv shell -- npm run build`, serve with `devenv shell -- npm run serve`, then use the product at `http://127.0.0.1:5198` in headless Chromium or WebKit. Check a climate month, season and year, the forecast and a forecast day, city cards, and taste changes. Compare place scores with the published files and the Python reference curves.


The repeatable numeric map-card check is [`research/site/map-card/check-map-card.py`](../../research/site/map-card/check-map-card.py). It takes the production map fragment source from the running WebGL program, supplies an exact readback of the live resolved texture, and reads the fragment's score output at each place. This checks the final spatial filter, rather than only the unfiltered texture that already agreed before this fix. Saved Chromium and WebKit results cover 30 places and four stretches each.
