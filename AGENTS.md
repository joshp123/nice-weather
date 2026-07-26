---
written_by: ai
---

# AGENTS.md

Josh owns this project.

## Intent

Nice Weather Finder is a worldwide map for choosing where to position yourself for warm, bright, dry and calm weather. Europe must retain its better regional forecast detail. `16°C` is mediocre, not good.

## New-Agent Handoff

Before editing, read:

1. `README.md` for the current runnable project shape.
2. `docs/project-context.md` for product intent, current state, architecture, known shortcuts, and roadmap.
3. `.agent/execplan-pending.md` for the living implementation log and review history.

Current local project path:

`/Users/josh/code/nice-weather-finder`

Current state:

- The running app is a worldwide decision map with one 14-day forecast and 14 individual days. The twelve-month seasonal journey remains a core product requirement, but the control is withheld until the ERA5 replacement publishes validated evidence.
- ECMWF IFS supplies the complete worldwide forecast at an honestly labelled 28 km. DWD ICON-EU replaces complete supported European days at about 7 km.
- The rejected AgERA5 publication is inactive. Its replacement uses hourly ERA5 evidence for 2006–2025 on the source 0.25° grid. It builds exact civil-local daytime records, scores every real day, then calculates monthly probabilities and poor-spell risk. November is the current production canary. Do not show climate as available until a validated month is published.
- Natural Earth 5.1.1 centre-on-land cells are the climate land authority. Water stays transparent. A named coastal place may use its nearest valid land cell with explicit disclosure; missing inland evidence remains missing.
- `go run ./cmd/niceweather forecast-world` validates and atomically publishes a complete worldwide forecast. `era5-history-month`, `era5-publication-authority` and `era5-climate-month` build and publish the replacement climate contract in explicit stages.
- Generated weather data lives in `./artifacts`; it is not frontend static build output.
- Frontend build lives in `./dist`.
- Forecast horizons are 14 concrete local-date IDs. Climate horizons are 12 explicit months.
- Forecast aggregation uses the canonical local daytime window. ERA5 climate uses exact IANA civil-local 10:00–20:00 windows and valid civil dates.
- Forecast, climate, places and best-weather each use small pointers to immutable, validated artifacts. Sample data must never look live.
- Server startup uses the active artifact root directly. The retired monolithic `artifacts/current`, `/manifest.json` and `/releases/...` contract is removed.
- The server validates and hot-swaps complete forecast snapshots. Climate, place and shortlist clients validate their static pointers and immutable files before changing visible state. A request must never mix evidence from different releases.
- Forecast freshness, source, forecast detail and resolution are hard UX requirements. Raw provider detail belongs in progressive disclosure.

Current product boundaries:

1. One weather domain and scoring path serves map pixels and point details.
2. Compact spatial point blocks provide bounded selected-place lookup.
3. Pre-evaluated scalar tiles keep rendering cheap. The browser colours stored values but never rescales or scores weather.
4. Forecast and climate may have different honest resolutions; never resample to imply false fidelity.
5. Worldwide coverage uses one score meaning. European ICON-EU detail must not regress.

## UI And Self-Review Workflow

Borrow the xuezh app discipline for web work:

- The app is a human-facing decision map, not a machine-facing weather pipeline console. Visible copy must say what a person needs to know, such as `Updated this morning`, `Forecast is stale`, and `Covers Wed-Sun`; raw model/run terms belong behind data details.
- Runtime UI copy must be deterministic typed app state: manifest timestamps, selected date/profile, score output, and update job state. Do not use LLM-generated prose in the production interface.
- Keep the bottom control as the one map control system unless a later ExecPlan records a better decision. It must support the map, not become a dashboard.
- Permanent map chrome should be limited to forecast status, profile/day controls, current location, and an affordance for score/data explanation. Place lists, place details, and score theory should be progressive disclosure by default.
- A searched place or selected coordinate is represented by one map marker. Opening it should show why that cell is good or bad in context.
- Internal forecast refresh controls are private-deployment features only. They must be disabled or hidden by default, must not be exposed on public internet deployments, and must be explicitly enabled only for local/Tailnet/admin use.
- Keep the UI system intentionally small: one body/display font stack, one bottom map cockpit, one segmented-control style, one score-detail table style, one legend style and one map-marker style.
- The map is the primary surface. The cockpit is supporting chrome and must not stretch into empty space on large monitors; it is capped for 57-inch/ultrawide-class workspaces.
- Do not add cards inside cards, alternate button families, decorative palettes, or new explanatory panels unless an observed workflow needs them.
- After UI changes, run the app in the Codex browser and a real Chrome or Safari session. Capture desktop and phone states. Reject the work if text clips, controls wrap badly, keyboard or pointer map interaction breaks, the sample warning disappears, large-monitor layout wastes space, or the score explanation is unclear.
- For data changes, inspect generated pointers, catalogs, scalar values, point evidence, place scores and served URLs. Do not judge correctness only from the full map screenshot.
- Use disposable/generated artifacts for smoke checks; do not mutate source weather caches unless that is the task.

## Stack

- `devenv.nix` is the dependency entrypoint.
- Go owns the CLI, data pipeline, scoring, artifact generation and static server.
- Vite + React + TypeScript owns the frontend.
- MapLibre renders the map.
- GDAL and ecCodes are available for real GeoTIFF/GRIB work.

## Engineering Philosophy

Use the Zen of Python as project law, especially: "There should be one and preferably only one obvious way to do it."

There is one scoring path:

`source field -> DailyMetrics -> component score -> profile score -> point block and scalar tile`

Do not add model soup, fallback providers, client-side scoring, speculative abstraction, plugin systems, duplicate concepts, compatibility layers, or hidden alternate paths.

## Agent Self-Assessment Loop

Every changeset must produce observable artifacts the agent can inspect without human judgment. The agent must inspect the working project, not just code.

Required observable checks:

- Forecast build: run `go run ./cmd/niceweather forecast-world --cache ./.cache/weather --staging <large-disk-path> --out ./artifacts`; inspect all 14 days, both source tiers, resolutions, point blocks and scalar tiles.
- Climate build: publish months through `climate-world`, then run `go run ./cmd/niceweather verify-climate --out ./artifacts`; inspect all 12 months, source quality, scenario evidence and scalar/point parity.
- Static server: run `go run ./cmd/niceweather serve --web ./dist --artifacts ./artifacts --listen 127.0.0.1:5198`; fetch expected URLs and prove a same-process release swap.
- Frontend: open in browser, switch controls, verify map layer changes, inspect screenshots.
- Scalar tiles: verify declared checksums, non-empty land values, canonical palette mapping, exact point/lens parity and transparency outside land.
- Scores: run deterministic scoring cases and compare expected numeric/category outputs.

Verify layer by layer before end-to-end claims:

1. scoring and source mapping
2. point-block and scalar-tile encoding and exact parity
3. pointer and catalog shape, checksums and URL contract
4. HTTP responses and content types
5. frontend manifest/state selection
6. MapLibre scalar-layer install and release-scoped checksum cache key
7. rendered browser pixels and screenshots

Performance and telemetry targets:

- artifact build must print period/field/file counts, byte size and wall time
- pointer fetches should complete in under 500 ms on localhost
- the first useful map should appear in under two seconds on localhost
- visible controls should respond within 100 ms
- ordinary map movement should sustain at least 55 fps with no task above 100 ms
- settled JavaScript heap should stay below 60 MB after representative interaction
- browser console must log pointer/catalog load, map load, score-layer readiness, resource summaries and profile/horizon/place interactions
- resource telemetry must expose overlay count/bytes and base-map request count, plus base-map bytes when the browser exposes transfer size
- map or overlay failures are correctness bugs, not harmless warnings

The agent must keep fixing until its observable checks pass. Handoff is forbidden unless the agent can state what it ran, what it observed, what was wrong before fixes, and why the current output proves the feature works.

## Recursive Review Contract

After every consequential changeset, run a fresh no-edit review and rerun the relevant observable checks after fixes. For product milestones, use zero-context reviewers given only Josh's steering and the fixed website URL. Do not give them implementation notes or known defects. Repeat until no correctness, simplicity or severe product issue remains. Record each pass and resulting edits in the ExecPlan `Decision Log` or `Review Log`.
