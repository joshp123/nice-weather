# Nice Weather Finder

Europe-first weather map that helps chase pleasant weather for travel and cycling.

New agents should read `AGENTS.md`, then `docs/project-context.md`.

## Scope

- Frontend: Vite + React + TypeScript + MapLibre.
- Backend: Go.
- Forecast source: DWD ICON-EU GRIB2, native 0.0625 degree Europe grid, rendered as a generated z0-z7 weather tile pyramid.
- Current map mask: Natural Earth land polygons hide sea pixels.
- Forecast horizons: today plus the next four local forecast dates.
- Target climate data: WorldClim 2.1 monthly normals, roughly 1 km.
- Profiles: `general`, `cycling`.
- Time basis: `forecast` daytime aggregate from 10:00-20:00 local time per grid cell; `climatology` returns when real monthly artifacts exist.
- Freshness: the UI shows model run age, run time, artifact update time, selected date/window, and a stale warning.

The timezone at the grid-cell center chooses the local forecast day/window. That keeps timezone boundaries explicit and avoids hardcoding Amsterdam time.

## One Scoring Contract

All source data must map into one canonical weather input:

```ts
type WeatherInput = {
  tempC: number;
  humidity: number;
  rainMm: number;
  cloudPct: number;
  windKmh: number;
};
```

The scoring engine is source-agnostic. It only accepts `WeatherInput` and returns component scores plus `general` and `cycling` composites.

## Backend Direction

Use Go for the backend/pipeline. This is grid/file processing, not app-server cleverness. Keep it boring: download source files, normalize fields, score cells, emit map artifacts and small metadata JSON.

Forecast artifacts are dynamic generated data, not frontend assets. `weatherchase update` writes a release under `./artifacts/releases/` and points `./artifacts/current` at the latest valid release. It chooses the newest complete DWD run that can cover the requested number of full `10:00-20:00` local forecast dates. `weatherchase serve --web ./dist --artifacts ./artifacts` serves the React build and generated weather data as separate roots. Climate artifacts can later be rebuilt rarely from WorldClim, but should still use the same `/artifacts/*` URL contract.

## Forecast Update

```bash
devenv shell -- go run ./cmd/weatherchase update \
  --cache ./.cache/dwd/forecast \
  --land ./.cache/land/ne_50m_land.geojson \
  --out ./artifacts \
  --days 5
```

On a VPS, run that command hourly from a timer. DWD publishes runs around `00Z`, `06Z`, `12Z`, and `18Z`; the updater checks whether the newest usable run is complete before doing any work. If no newer complete run can cover five full local forecast dates, it exits without changing the published release. If a build fails, the previous `./artifacts/current` release remains live.

The UI marks the forecast stale once the published DWD run is older than the manifest threshold, currently 12 hours. That warning is deliberately prominent so a dead timer or broken update is obvious from the map. Browser telemetry logs manifest load, map load, score-layer updates, tile/resource totals, and control interactions.

## Verification

```bash
devenv shell -- go test ./...
devenv shell -- go vet ./...
devenv shell -- npm run test
devenv shell -- npm run build
devenv shell -- npm run qa:browser -- http://127.0.0.1:5173/
```

Run those sequentially. `qa:browser` expects `weatherchase serve` to be running. It opens the real app, checks the freshness/date/window UI, switches forecast horizon and profile, verifies forecast tile requests, checks browser telemetry and resource summaries, rejects blank map screenshots, and saves ignored screenshots under `output/screenshots/`.
