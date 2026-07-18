---
written_by: ai
---

# AGENTS.md

Josh owns this project.

## Intent

Nice Weather Finder is a Europe-first map for escaping Dutch grey/cold weather and finding warm, sunny, bike-friendly places. User preference is warm comfort, sunshine, dry conditions, and cycling-friendly low wind. `16C` is mediocre, not good.

## New-Agent Handoff

Before editing, read:

1. `README.md` for the current runnable project shape.
2. `docs/project-context.md` for product intent, current state, architecture, known shortcuts, and roadmap.
3. `.agent/execplan-pending.md` for the living implementation log and review history.

Current local project path:

`/Users/josh/code/nice-weather-finder`

Current state:

- The app is a Europe-first decision map with 14 forecast days and 12 typical months.
- Detailed days use DWD ICON-EU at about 7 km. ECMWF AIFS extends the outlook to day 14 at an honestly labelled 28 km.
- Typical months use CHELSA 2.1 normals for 1981–2010, delivered on a 0.1 degree grid at about 11 km. CHELSA does not provide wet-time frequency; never invent it from monthly rain.
- Natural Earth 5.1.1 land polygons at 1:10 million scale are the only land authority. Forecast uses all-touched cells. Climate publishes centre-on-land and touched-only coastline cells only when all fields are present; missing cells stay transparent, and a month fails if missing centre support exceeds 2%.
- `go run ./cmd/niceweather update` discovers complete runs, validates source metadata and units, builds under `./artifacts/.building`, then atomically publishes through `./artifacts/current`.
- Generated weather data lives in `./artifacts`; it is not frontend static build output.
- Frontend build lives in `./dist`.
- Forecast horizons are 14 concrete local-date IDs. Climate horizons are 12 explicit months.
- Forecast aggregation is `10:00-20:00` local time per grid cell using timezone lookup; the grid-cell center chooses the timezone at boundaries.
- The manifest is version 4 with product contract `nice-weather-v6` and explicitly identifies `live` or `sample` data. Sample data must never look live.
- The running server validates and swaps complete release snapshots when `current` changes. A request must never mix manifest, image and point data from different releases.
- Forecast freshness, source, forecast detail and resolution are hard UX requirements. Raw provider detail belongs in progressive disclosure.

Current product boundaries:

1. One weather domain and scoring path serves map pixels and point details.
2. One compact binary grid per period provides constant-time point lookup.
3. Pre-evaluated overall and component PNGs keep browser rendering cheap. They use the one canonical scoring path; the browser never rescales or scores weather.
4. Forecast and climate may have different honest resolutions; never resample to imply false fidelity.
5. Europe comes first. World coverage waits until this contract remains fast and trustworthy after deployment.

## UI And Self-Review Workflow

Borrow the xuezh app discipline for web work:

- The app is a human-facing decision map, not a machine-facing weather pipeline console. Visible copy must say what a person needs to know, such as `Updated this morning`, `Forecast is stale`, and `Covers Wed-Sun`; raw model/run terms belong behind data details.
- Runtime UI copy must be deterministic typed app state: manifest timestamps, selected date/profile, score output, and update job state. Do not use LLM-generated prose in the production interface.
- Keep the bottom bar as a map control dock unless a later ExecPlan records a better decision. The dock should support the map, not become a dashboard.
- Permanent map chrome should be limited to forecast status, profile/day controls, current location, and an affordance for score/data explanation. Place lists, place details, and score theory should be progressive disclosure by default.
- A searched place or selected coordinate is represented by one map marker. Opening it should show why that cell is good or bad in context.
- Internal forecast refresh controls are private-deployment features only. They must be disabled or hidden by default, must not be exposed on public internet deployments, and must be explicitly enabled only for local/Tailnet/admin use.
- Keep the UI system intentionally small: one body/display font stack, one bottom map cockpit, one segmented-control style, one score-detail table style, one legend style and one map-marker style.
- The map is the primary surface. The cockpit is supporting chrome and must not stretch into empty space on large monitors; it is capped for 57-inch/ultrawide-class workspaces.
- Do not add cards inside cards, alternate button families, decorative palettes, or new explanatory panels unless an observed workflow needs them.
- After UI changes, run the app in the Codex browser and a real Chrome or Safari session. Capture desktop and phone states. Reject the work if text clips, controls wrap badly, keyboard or pointer map interaction breaks, the sample warning disappears, large-monitor layout wastes space, or the score explanation is unclear.
- For data changes, inspect generated artifacts, manifest, overlay PNG pixels, place scores, and served URLs. Do not judge correctness only from the full map screenshot.
- Use disposable/generated artifacts for smoke checks; do not mutate source weather caches unless that is the task.

## Stack

- `devenv.nix` is the dependency entrypoint.
- Go owns the CLI, data pipeline, scoring, image generation, and static server.
- Vite + React + TypeScript owns the frontend.
- MapLibre renders the map.
- GDAL and ecCodes are available for real GeoTIFF/GRIB work.

## Engineering Philosophy

Use the Zen of Python as project law, especially: "There should be one and preferably only one obvious way to do it."

There is one scoring path:

`source field -> DailyMetrics -> component score -> profile score -> grid and PNG`

Do not add model soup, fallback providers, client-side scoring, speculative abstraction, plugin systems, duplicate concepts, compatibility layers, or hidden alternate paths.

## Agent Self-Assessment Loop

Every changeset must produce observable artifacts the agent can inspect without human judgment. The agent must inspect the working project, not just code.

Required observable checks:

- CLI source smoke: run `go run ./cmd/niceweather update --cache ./.cache/weather --out ./artifacts --smoke`; inspect provider grids, CHELSA metadata and transformed ranges.
- CLI real build: run `go run ./cmd/niceweather update --cache ./.cache/weather --out ./artifacts`; inspect all 14 days, 12 months, sources, resolutions, grids and images.
- Static server: run `go run ./cmd/niceweather serve --web ./dist --artifacts ./artifacts --listen 127.0.0.1:5173`; fetch expected URLs and prove a same-process release swap.
- Frontend: open in browser, switch controls, verify map layer changes, inspect screenshots.
- Images: sample PNGs and verify non-empty pixels, colours from the canonical continuous palette, exact point/lens parity, and alpha outside land.
- Scores: run deterministic scoring cases and compare expected numeric/category outputs.

Verify layer by layer before end-to-end claims:

1. scoring and source mapping
2. grid encoding, image generation and exact PNG pixels
3. manifest shape, checksums and URL contract
4. HTTP responses and content types
5. frontend manifest/state selection
6. MapLibre image source/layer install and release-scoped checksum cache key
7. rendered browser pixels and screenshots

Performance and telemetry targets:

- artifact build must print period/field/file counts, byte size and wall time
- manifest fetch should complete in under 500ms on localhost
- first score layer ready event should complete in under 1500ms on localhost
- browser console must log manifest load, map load, score-layer ready timings, resource summaries, and profile/horizon/place interactions
- resource telemetry must expose overlay count/bytes and base-map request count, plus base-map bytes when the browser exposes transfer size
- map or overlay failures are correctness bugs, not harmless warnings

The agent must keep fixing until its observable checks pass. Handoff is forbidden unless the agent can state what it ran, what it observed, what was wrong before fixes, and why the current output proves the feature works.

## Recursive Review Contract

After every changeset, explicitly invoke `review-recent-work`. Rerun all relevant observable checks after review fixes. Repeat review, fixes, and checks until no correctness or simplicity issue remains. Record each review pass and resulting edits in the ExecPlan `Decision Log` or `Outcomes & Retrospective`.
