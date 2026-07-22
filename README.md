---
written_by: ai
---

# Nice Weather Finder

Nice Weather Finder answers one question: where should I go for genuinely pleasant weather?

The full-screen map covers the world. It shows the next 14 days, individual forecast days and a recent-climate view for each month. Search for any place or click the map. The place view gives one answer, then shows the evidence behind it.

The visible lenses are:

- **Overall**: the complete warm-weather judgement;
- **Comfort**: temperature and moisture together;
- **Dry**: rain amount and wet time;
- **Bright**: sunshine, solar energy and cloud;
- **Calm**: wind comfort for the selected preference.

**Warm & sunny** favours comfortable warmth, brightness, dryness and low wind. **Cycling** makes rain and wind more costly. The warm bias is deliberate: 22–27°C is ideal and 16°C is mediocre.

## Current data

- **Worldwide forecast:** ECMWF IFS on its public 0.25° delivery grid, labelled as about 28 km weather detail.
- **European forecast refinement:** DWD ICON-EU at about 7 km for complete supported days. A daily record comes wholly from IFS or ICON-EU; fields are never mixed.
- **Recent climate:** AgERA5 daily evidence for 2006–2025. Every real day is scored before monthly probabilities are calculated. The delivery grid is 0.1°, atmospheric evidence is about 30 km and rain evidence is 0.25°.
- **Land:** Natural Earth 5.1.1. Water stays transparent. A coastal place can use its nearest valid land cell, which is disclosed in the place view.
- **Places:** a pinned worldwide GeoNames index with local names, alternate names and transliterations.

The climate view answers questions such as “How often is Málaga nice in November?” It shows the chance of a Nice day, the chance of an Awful day, the median score and the risk of a poor week. It does not score a synthetic average day.

## Run locally

Use the project shell to build the frontend, then serve it with the current artifacts:

```bash
devenv shell
npm run build
go run ./cmd/niceweather serve \
  --web ./dist \
  --artifacts ./artifacts \
  --listen 127.0.0.1:5198
```

Open <http://127.0.0.1:5198/>. Port `5198` is the only local application port.

## Refresh data

Build and atomically publish a worldwide forecast:

```bash
go run ./cmd/niceweather forecast-world \
  --cache ./.cache/weather \
  --staging /absolute/path/on/a-large-disk \
  --out ./artifacts
```

Build one AgERA5 climate month from existing source checkpoints. The Copernicus bearer token is read from standard input and must not be stored in the repository:

```bash
printf '%s' "$AGERA5_BEARER_TOKEN" | go run ./cmd/niceweather climate-world \
  --month 1 \
  --checkpoints /absolute/path/to/checkpoints \
  --land-cache /absolute/path/to/land-cache \
  --out /absolute/path/to/artifacts
```

Repeat for months 1–12. Each validated month is immutable. The climate pointer changes only when the published month set is coherent.

Build the worldwide place index from pinned GeoNames extracts, then rebuild the forecast-bound destination shortlist:

```bash
go run ./cmd/niceweather places \
  --source ./.cache/geonames/2026-07-18 \
  --source-date 2026-07-18 \
  --out ./artifacts

go run ./cmd/niceweather best-weather --out ./artifacts
```

The server validates and hot-swaps complete forecast snapshots. Climate, place and shortlist clients verify their static pointers and immutable files before changing visible state. A normal data refresh does not require a restart.

## Architecture

Go owns source retrieval, normalisation, scoring, artifact generation, validation and HTTP serving. The browser never scores weather:

```text
source fields -> DailyMetrics -> canonical component and profile scores
              -> immutable point blocks and scalar tiles
              -> viewport-loaded map and place evidence
```

Forecast and climate use small pointer and catalogue files. The browser loads only scalar tiles for the visible viewport and exact point blocks for a selected place. Versioned files are release-scoped, checksum-bound, immutable, cacheable and suitable for a private static origin. HTTP Range is supported for immutable point assets.

React owns product state and progressive disclosure. MapLibre owns the geographic map. A WebGL custom layer colours stored scalar values through the canonical palette. Place labels, borders and coastlines remain visible above the weather.

Generated weather belongs in `artifacts`; the frontend build belongs in `dist`. Neither is source code.

## Verify

```bash
go test ./...
go vet ./...
npm test -- --run
npm run build
go run ./cmd/niceweather verify-climate --out ./artifacts
```

Then run the server and verify:

- the map loads useful weather in under two seconds;
- forecast and climate controls respond within 100 ms;
- ordinary map movement has no task over 100 ms;
- desktop, phone and ultrawide layouts do not clip or waste the screen;
- selected-place evidence matches the visible scalar value;
- the browser console has no warning or error;
- exact immutable files return the declared cache and content-type headers.

New agents must read [AGENTS.md](AGENTS.md), [docs/project-context.md](docs/project-context.md) and [.agent/execplan-pending.md](.agent/execplan-pending.md) before editing.
