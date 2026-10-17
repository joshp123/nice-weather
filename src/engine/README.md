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
```

The layer needs WebGL2 and `EXT_color_buffer_float` (supported by the tested WebKit and Chromium Metal backends). It honours MapLibre's depth so opaque water covers weather colours. On removal it releases its GPU resources.

A selected month fetches only `cells.bin`, that month's eight records per cell, and its offsets PNG. Months remain cached. Seasons and the year reuse those files; forecasts reuse their one records file and only the offset months their dates need. PNG decoding calls the existing manual greyscale decoder, without a canvas.

The GPU scores every cell at eleven felt shifts from −15 to +15 °C in 3 °C steps. Those levels cover every encoded offset. A second pass resolves each fine pixel from its monthly offset, the adjacent levels, then the four cell centres. Sea cells have the exact nearest land ordinal in wrapped grid coordinates. The final map uses the existing cubic B-spline, six bands, palette, edge antialiasing and contour lines.

After a completed frame, `scorePoints` reads the resolved GPU grid once per revision and caches it for inexpensive label updates. Point verdicts use the identical weighted records and interpolation, including the final spline. This also applies to the warmth, walk-home and nuisance-factor averages. The period's score is the mean of daily products; multiplying the averaged parts does not reproduce it. Climate nice-day shares describe the representative sample. Forecast nice-day counts are interpolated expected counts. The engine also interpolates land values under water; the basemap masks them. The place API is responsible for geographical place names and physical facts.

`setTaste` coalesces changes before the next MapLibre frame and resolves after a GPU completion fence. Its duration includes waiting for that frame and GPU execution. Replacing a pending period selection leaves the latest selection active. A failed file fetch rejects the selection and can be retried.

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

Browser proof on the real data, once it exists:

```sh
cd /tmp
uv run --quiet --with playwright python -I /Users/josh/code/nice-weather-finder/src/engine/check_browser.py --real
uv run --quiet --with numpy python -I /Users/josh/code/nice-weather-finder/src/engine/check_reference.py /tmp/nwf-agents/overnight/engine/chromium-real-reference.json /tmp/nwf-agents/overnight/engine/webkit-real-reference.json
```

Without `--real`, the browser check exercises the full-size synthetic set, every forecast slot and all seasons. Both modes measure twelve month and year redraws, record renderer identity, capture screenshots, verify actual GPU fine-grid values against point verdicts and export the exact weighted day samples. The independent Python check imports the reference curves from `research/taste/nice_v6.py` and checks every returned part, score and nice-day share. Results go to `/tmp/nwf-agents/overnight/engine/`.
