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

For the exact default taste, the engine loads only the selected period's precomputed score PNG. `greyscaleImage.ts` decodes its bytes without canvas colour conversion, then uploads them as an R8 texture. The map uses the same palette, bands, contours and cubic B-spline as the live grid. Sea/no-data bytes (255) are omitted from the spline and the remaining weights are normalised. Images have floored integer precision and remain authoritative after resetting a changed taste.

Default labels and counts request compact named-place numbers for places in view. Named climate cards load their compact physical facts separately. Forecast cards and unnamed current-period cards request a judgement from the Worker. These paths do not download the browser's climate month files, forecast `days.bin`, cells or temperature-offset grids. `prepareYear()` remembers a calendar request without downloading records at the default taste.

`setPeriod` resolves after its image has completed a frame. A missing image reports an error rather than downloading day records. Forecast metadata pins the image to its run. Image names use `climate/scores-typical-day-<month or month run>.png`, `climate/scores-typical-day-year.png`, and forecast `<run>/scores-typical-day-day-00.png` through `day-13.png` or `scores-typical-day-all.png`. Dimensions are 1440 × 721, row 0 = 90°N, column 0 = 180°W; land is 0..100 and sea/no data is 255.

Moving a taste slider loads the selected period's records and offsets, plus the year if a card or time picker has requested its calendar. While they load, the default image clears and the loading state is shown; no previous period's records are used. `setTaste` resolves after the resulting GPU frame. Later changes reuse cached records. `prepareTaste()` can prefetch those records when the visitor opens Your taste, without changing the map or loading state. Before selecting any period, `setTaste` simply stores the taste.

`loading` covers the selected map and requested current-period Worker facts. `pointLoading` also reports pending named-place numbers and card facts. Consumers refresh on `onRedraw` and `onLoadingChange`. The latest period and taste win: an earlier response cannot restore a previous map. A changed taste uses `scorePoints` and `countPoints` to read the resolved GPU grid once per completed revision; subsequent map moves sample memory without another readback.

## Point verdicts and scoring

The GPU scores each 1° cell at eleven felt shifts from −15 to +15 °C in 3 °C steps. The owning cell is floor(fine row / 4), floor(fine column / 4), matching the cell used to produce its temperature offset. Each fine point uses its own offset and its owning cell, interpolating each day score between adjacent shift levels. Sea cells use the exact nearest land cell in wrapped grid coordinates. `countPoints` reads the same GPU grid once per revision and returns packed nice, perfect and awful counts for each named place. The displayed map, named scores, counts and card use the same final fine-grid B-spline.

The overview field uses a 4 × 4 cubic B-spline footprint around the requested coordinate. Points missing in any selected month are omitted and the remaining spatial weights are normalized. Each native fine-grid point first resolves its own representative days and calendar weights. The card then combines the complete point verdicts with those same spatial weights: typical-day score, day counts, factors and physical averages. It does not substitute a filtered score into an unrelated nearest-point card. Point verdicts and grid verdicts are cached per sampler.

At the default taste, named climate verdicts use the published 30 arc-second point data. The Worker judges unnamed points; the browser does not download fine-place day packets. These card results do not claim that the surrounding overview map has 1 km resolution.

Each day scores the product of its weather factors. A stretch uses the weighted median day: its band is the highest threshold reached by at least half its days. Interpolating the threshold shares where they cross one half gives a continuous value for colouring. A single forecast day keeps its score. Climate months carry their real calendar-day weights, with eight representative days each. Counts estimate nice-or-better, perfect and awful days out of the calendar length; a forecast counts its actual days. Taste changes alter judgements, not physical averages.

`judgePoint(latitude, longitude, period?)` uses the current taste. Default named queries request compact data as needed; other current-period queries request Worker facts. Named numbers also supply month and forecast-day dials. The unnamed forecast Worker response includes all fourteen day judgements in the same response; unnamed month dials use compact monthly score bytes with the map's spatial weights. Changed tastes use already-resolved overview records: a loaded season exposes its three months, a year exposes all months, and a forecast exposes each of its 14 days. These queries never change the selected map period.

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

The second form writes 145 PNGs and `scores.bin` to the specified output folder: twelve months, 132 start/length runs of two to twelve months, and the canonical year alias. The twelve full-year rotations are byte-identical to `year`; seasons already have their run names. It retains daily threshold shares until after weighting each period. Monthly bytes alone cannot reproduce a longer period's typical day or counts. Regenerate these files whenever records, offsets or the default rule change.

## Verification

Build with `devenv shell -- npm run build`, serve with `devenv shell -- npm run serve`, then use the product at `http://127.0.0.1:5198` in headless Chromium or WebKit. Check a climate month, season and year, the forecast and a forecast day, city cards, and taste changes. Compare place scores with the published files and the Python reference curves.


The repeatable numeric map-card check is [`research/site/map-card/check-map-card.py`](../../research/site/map-card/check-map-card.py). It takes the production map fragment source from the running WebGL program, supplies an exact readback of the live resolved texture, and reads the fragment's score output at each place. This checks the final spatial filter, rather than only the unfiltered texture that already agreed before this fix. Saved Chromium and WebKit results cover 30 places and four stretches each.
