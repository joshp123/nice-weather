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

- Active app is Europe-only forecast rendering from DWD ICON-EU GRIB2 files.
- `weatherchase update` discovers the newest complete DWD run that can cover the requested full local-day windows, downloads/caches it, builds release artifacts, and publishes them through `./artifacts/current`.
- Generated weather data lives in `./artifacts`; it is not frontend static build output.
- Frontend build lives in `./dist`.
- Current forecast horizons are concrete local-date IDs for five complete daytime forecast windows.
- Forecast aggregation is `10:00-20:00` local time per grid cell using timezone lookup; the grid-cell center chooses the timezone at boundaries.
- Forecast freshness is a hard UX requirement. The UI must show DWD run time, artifact build time, selected local date/window, age, and a stale warning when the run is older than the manifest threshold.
- Climatology is planned but not implemented; WorldClim is the intended first source.

Near-term state/plan:

1. Done: multi-day forecast horizons as local calendar dates.
2. Done: per-grid-cell local 10:00-20:00 aggregation from timezone polygons.
3. Done: horizon-aware anchor pins/details so city scores change with selected day.
4. Done: latest-complete DWD run detection/download, then atomic artifact rebuild/swap.
5. Next: WorldClim monthly climatology artifacts using the same `WeatherInput -> score -> tile` path.

## UI And Self-Review Workflow

Borrow the xuezh app discipline for web work:

- Add a design-system/debug route before major UI iteration, preferably `#/design-system`.
- The design-system route must render the same production React components used by the real app, not copied demo markup.
- Cover the important states there: map shell, loading, missing manifest, layer error, controls, legend, anchor list, anchor detail, scoring explainer, and mobile layout.
- After UI changes, run the app in a browser, capture screenshots for the relevant states, and reject the work if text clips, controls wrap badly, map interactions break, production and design-system components drift, or the score explanation is unclear.
- For data changes, inspect generated artifacts, manifest, tile pixels, anchor scores, and served URLs. Do not judge correctness only from the full map screenshot.
- Use disposable/generated artifacts for smoke checks; do not mutate source weather caches unless that is the task.

## Stack

- `devenv.nix` is the dependency entrypoint.
- Go owns the CLI, data pipeline, scoring, tile generation, and static server.
- Vite + React + TypeScript owns the frontend.
- MapLibre renders the map.
- GDAL and ecCodes are available for real GeoTIFF/GRIB work.

## Scaffold Source

`docs/chatgpt-pro-response.txt` controls the first scaffold: module layout, pipeline order, artifact format, and first milestone. Follow it unless the ExecPlan records a justified deviation.

## Engineering Philosophy

Use the Zen of Python as project law, especially: "There should be one and preferably only one obvious way to do it."

There is one scoring path:

`source field -> WeatherInput -> component score -> profile score -> tile`

Do not add model soup, fallback providers, client-side scoring, speculative abstraction, plugin systems, duplicate concepts, compatibility layers, or hidden alternate paths.

## Agent Self-Assessment Loop

Every changeset must produce observable artifacts the agent can inspect without human judgment. The agent must inspect the working project, not just code.

Required observable checks:

- CLI: run `weatherchase update --cache ./.cache/dwd/forecast --land ./.cache/land/ne_50m_land.geojson --out ./artifacts --days 5`; inspect logs, manifest, and generated tile files.
- Static server: run `weatherchase serve --web ./dist --artifacts ./artifacts`; fetch expected URLs.
- Frontend: open in browser, switch controls, verify map layer changes, inspect screenshots.
- Browser fallback: if Browser Use is unavailable, run `npm run qa:browser -- http://127.0.0.1:5173/` against the served app and inspect the screenshots in `output/screenshots/`.
- Images/tiles: sample PNGs and verify non-empty pixels, expected colors, and alpha outside land.
- Scores: run deterministic scoring cases and compare expected numeric/category outputs.

Verify layer by layer before end-to-end claims:

1. scoring and source mapping
2. tile generation and exact PNG pixels
3. manifest shape and tile URL contract
4. HTTP responses and content types
5. frontend manifest/state selection
6. MapLibre source/layer install
7. rendered browser pixels and screenshots

Performance and telemetry targets:

- artifact build must print manifest/layer/tile counts
- manifest fetch should complete in under 500ms on localhost
- first score layer install should complete in under 1500ms on localhost
- browser console must log manifest load, map load, and score-layer install timings
- map tile failures are correctness bugs, not harmless warnings

The agent must keep fixing until its observable checks pass. Handoff is forbidden unless the agent can state what it ran, what it observed, what was wrong before fixes, and why the current output proves the feature works.

## Recursive Review Contract

After every changeset, explicitly invoke `review-recent-work`. Rerun all relevant observable checks after review fixes. Repeat review, fixes, and checks until no correctness or simplicity issue remains. Record each review pass and resulting edits in the ExecPlan `Decision Log` or `Outcomes & Retrospective`.
