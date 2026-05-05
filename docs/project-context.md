# Nice Weather Finder Project Context

This document is the handoff brief for new agents. Root `AGENTS.md` is still project law; this file captures the working context and roadmap.

## Product Intent

Nice Weather Finder helps Josh avoid Dutch grey/cold weather and find warm, sunny, dry, bike-friendly places for camper-van travel, flying, and digital nomad planning.

The product preference is deliberately warm-biased:

- ideal temperature is `22..27C`
- `16C` is mediocre, not good
- rain and cloud hurt general niceness
- rain and wind hurt cycling niceness more strongly
- sea weather is mostly irrelevant because the user cannot cycle at sea

## Current Location

Local project path:

```text
/Users/josh/code/nice-weather-finder
```

Useful generated/runtime paths:

```text
./.cache/dwd/daytime/              local DWD ICON-EU GRIB2 inputs
./.cache/land/ne_50m_land.geojson  Natural Earth land mask
./artifacts/                       generated weather manifest and PNG tiles
./dist/                            Vite frontend build
```

`./artifacts`, `./dist`, caches, and `node_modules` are ignored build/runtime output.

## Current Implementation

The active POC is Europe-only forecast rendering.

- Forecast source: local DWD ICON-EU GRIB2 files.
- Forecast grid: native regular lat/lon `0.0625` degree ICON-EU grid.
- Forecast horizon: one horizon, `d1`.
- Day window: current shortcut aggregates steps `008..018` from one run as 10:00-20:00 Europe/Amsterdam.
- Land mask: Natural Earth land polygons; masking prefers full land/coast/island coverage over perfectly excluding every sea pixel.
- Frontend: Vite React TypeScript + MapLibre.
- Backend/pipeline: Go CLI.
- Static serving: `weatherchase serve --web ./dist --artifacts ./artifacts`.

Current CLI:

```bash
devenv shell
go run ./cmd/weatherchase build \
  --dir ./.cache/dwd/daytime \
  --land ./.cache/land/ne_50m_land.geojson \
  --out ./artifacts \
  --run 'YYYYMMDDHH steps 008-018'
go run ./cmd/weatherchase serve --web ./dist --artifacts ./artifacts --addr 127.0.0.1:5173
```

Current frontend URL:

```text
http://127.0.0.1:5173/
```

## Architecture

There is one scoring path:

```text
source field -> WeatherInput -> component score -> profile score -> tile
```

Canonical weather input:

```ts
type WeatherInput = {
  tempC: number;
  humidity: number;
  rainMm: number;
  cloudPct: number;
  windKmh: number;
};
```

Frontend must never rescore weather. The frontend reads `/artifacts/manifest.json`, selects a manifest layer, and asks MapLibre to render that layer's tile URL.

Generated forecast artifacts are dynamic data, not frontend assets. The frontend build and generated weather artifacts are served as separate roots:

```text
./dist       -> app shell and JS/CSS
./artifacts  -> manifest and weather tiles
```

This keeps climate and forecast artifacts free to have different rebuild cadences without changing the UI contract.

## Important Modules

```text
cmd/weatherchase/main.go        CLI entrypoint: build, inspect-dwd, serve
internal/dwd/                   DWD ICON-EU field loading and mapping
internal/weatherinput/          canonical input grid and aggregation
internal/scoring/               only scoring policy
internal/tiles/                 WeatherInput grid -> scored PNG tile
internal/artifact/              manifest and tile path contract
internal/landmask/              land/coast/island coverage policy
internal/server/                serves ./dist and ./artifacts separately
src/data/                       manifest loading and frontend layer lookup
src/map/                        MapLibre integration and raster layer install
src/App.tsx                     current UI shell, controls, anchors, details
```

## Scoring Contract

Temperature curve:

```text
<=8C 0, 12C 20, 16C 45, 18C 70, 20C 88, 22..27C 100,
30C 85, 33C 55, 36C 20, >=39C 0
```

Weights:

```text
general: temp .40, rain .25, cloud .20, wind .10, humidity .05
cycling: rain .35, wind .25, temp .25, cloud .10, humidity .05
```

Cycling score must not exceed general score and is capped by rain and wind component scores.

Current palette:

```text
Not Nice  #9ca3af
Fair      #7dd3fc
Nice      #38bdf8
Beautiful #a3e635
Perfect   #16a34a
Pristine  #f59e0b
```

## Known Shortcuts And Gaps

- `d1` is an internal-looking horizon and should become date-based horizon IDs with human labels.
- Forecast aggregation currently uses Amsterdam time; proper implementation must aggregate 10:00-20:00 local time per grid cell.
- Only one forecast horizon is generated.
- DWD run discovery/download is not automated; current build expects local GRIB2 files.
- Anchor scores are not yet horizon-aware.
- Climatology is not implemented.
- Land mask uses Natural Earth 50m; good enough for POC, not final coastline precision.
- Current tiles are PNG filesystem tiles; revisit PMTiles or COG only when artifact size or serving complexity demands it.

## Forecast Roadmap

The next forecast workflow should stay pre-rendered and boring:

1. Detect the latest complete DWD ICON-EU run by checking required files, not by trusting the clock.
2. Download/decompress required fields and steps into `.cache/dwd`.
3. Build forecast horizons as local calendar dates.
4. For each grid cell, map lon/lat to an IANA timezone using timezone polygons.
5. For each local date, convert local `10:00..20:00` to UTC forecast steps for that cell.
6. Aggregate temperature, humidity, cloud, and wind as daytime averages.
7. Aggregate rain as the precipitation accumulation delta across the local daytime window.
8. Convert to `WeatherInput`, score once, render tiles, write manifest.
9. Write artifacts to a staging directory, validate, then atomically swap into `./artifacts`.

Rebuild cadence:

- rebuild when a newer complete DWD run is available
- do not live-render tiles per HTTP request
- do not bundle forecast data into `./dist`

Plain-English VPS workflow:

- A Linux timer runs one command every so often.
- That command checks whether DWD has published a newer complete forecast.
- If nothing changed, it exits without touching the live site.
- If there is a newer complete run, it downloads the required files, builds tiles in a temporary staging directory, validates them, then swaps the finished directory into place.
- Users either see the previous good forecast or the new good forecast; they should not see a half-built artifact tree.

When time advances:

- manifest horizon IDs should remain local dates such as `2026-05-05`
- frontend labels can render `today`, `tomorrow`, or weekday/date from those IDs
- a new DWD run replaces artifacts atomically; old frontend sessions should fail clearly or reload if manifest/tile versions drift

## Climatology Roadmap

WorldClim is the intended first climatology source. It should use the same canonical scoring path:

```text
WorldClim monthly field -> WeatherInput -> component score -> profile score -> tile
```

Monthly horizons should be `jan..dec` or date-independent month IDs. Climate artifacts are static-ish and can be rebuilt rarely, but they still belong under `./artifacts`, not in `./dist`.

## Short-Term Priorities

1. Replace Amsterdam-time `d1` with timezone-correct multi-day forecast horizons.
2. Make anchors horizon-aware and clickable details reflect the selected horizon.
3. Add latest-complete DWD run discovery/download.
4. Add atomic artifact swap and manifest version metadata.
5. Add WorldClim monthly climatology artifacts.
6. Keep frontend tests in the Node environment unless a test actually needs DOM APIs; `vite.config.ts` is typechecked with Node types and Vitest config typing.

## Xuezh Workflow Lessons To Port

The Chinese app's strongest practices apply here:

- Use a design-system route for production components and key UI states.
- Keep browser-visible self-review mandatory for UI work; screenshots are evidence, not decoration.
- Keep the server/pipeline as the source of truth. The frontend may select and render manifest state but must not own scoring or weather policy.
- Use one deep boundary for unattended updating. For this app that means one forecast updater command that owns run discovery, download/cache, build, validation, and atomic publish.
- Make updater reruns idempotent: running the command twice for the same complete DWD run should not change published artifacts.
- Prefer disposable fixture/generated artifacts for smoke checks.

## Longer-Term Direction

- Europe-first until the workflow is correct and fast.
- Expand geography only after forecast and climatology artifact contracts are stable.
- Keep one source per basis until a concrete reason forces another choice.
- Add route/campsite/city search only after the map layers are trusted.
- Consider PMTiles, COG, or object storage only when filesystem PNG tiles become a measured bottleneck.

## Verification Contract

Before handoff after any changeset:

```bash
go test ./...
go vet ./...
devenv shell -- npm run test
devenv shell -- npm run build
```

Observable checks:

```bash
go run ./cmd/weatherchase build --dir ./.cache/dwd/daytime --land ./.cache/land/ne_50m_land.geojson --out ./artifacts --run 'YYYYMMDDHH steps 008-018'
go run ./cmd/weatherchase serve --web ./dist --artifacts ./artifacts --addr 127.0.0.1:5173
curl -I http://127.0.0.1:5173/artifacts/manifest.json
curl -I http://127.0.0.1:5173/artifacts/tiles/forecast/general/d1/6/35/24.png
```

Use the in-app browser or Browser Use to verify the rendered map, controls, anchor details, console logs, and screenshot pixels.
