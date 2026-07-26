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
- **Clear sky**: cloud cover only;
- **Calm**: wind comfort for the selected preference.

**Warm & sunny** favours comfortable warmth, clear sky, dryness and low wind. **Cycling** makes rain and wind more costly. The warm bias is deliberate: 22–27°C is ideal and 16°C is mediocre.

## Current data

- **Worldwide forecast:** ECMWF IFS on its public 0.25° delivery grid, labelled as about 28 km weather detail.
- **European forecast refinement:** DWD ICON-EU at about 7 km for complete supported days. A daily record comes wholly from IFS or ICON-EU; fields are never mixed.
- **Recent climate:** the rejected AgERA5 publication is not active. Its replacement uses hourly ERA5 evidence for 2006–2025 on the source 0.25° grid. It builds exact civil-local daytime records before it scores and aggregates each real day. November is the current production canary; the product will not describe climate data as available until a validated month is published.
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

The ERA5 climate pipeline has three explicit stages: immutable year-month history, one checksum-trusted land authority, then a validated month publication. Inspect the exact required paths and checksums before a production run:

```bash
go run ./cmd/niceweather era5-history-month -h
go run ./cmd/niceweather era5-publication-authority -h
go run ./cmd/niceweather era5-climate-month -h
```

Each year-month history shard and each published month is immutable. A one-month preview index can support the November product canary. The complete climate verifier accepts only a coherent twelve-month publication.

Build the worldwide place index from pinned GeoNames extracts, then rebuild the forecast-bound destination shortlist:

```bash
go run ./cmd/niceweather places \
  --source ./.cache/geonames/2026-07-18 \
  --source-date 2026-07-18 \
  --out ./artifacts

go run ./cmd/niceweather best-weather --out ./artifacts
```

The server starts directly from the active artifact root; it does not require a monolithic `artifacts/current` release. It validates and hot-swaps complete forecast snapshots. Climate, place and shortlist clients verify their static pointers and immutable files before changing visible state. A normal data refresh does not require a restart.

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
