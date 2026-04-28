# AGENTS.md

Josh owns this project.

## Intent

Nice Weather Finder is a Europe-first map for escaping Dutch grey/cold weather and finding warm, sunny, bike-friendly places. User preference is warm comfort, sunshine, dry conditions, and cycling-friendly low wind. `16C` is mediocre, not good.

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

- CLI: run `weatherchase build --dir ./.cache/dwd/daytime --land ./.cache/land/ne_50m_land.geojson --out ./public/artifacts --run "YYYYMMDDHH steps 008-018"`; inspect logs, manifest, and generated tile files.
- Static server: run `weatherchase serve --dir ./public`; fetch expected URLs.
- Frontend: open in browser, switch controls, verify map layer changes, inspect screenshots.
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

Performance and telemetry targets for the POC:

- artifact build must print manifest/layer/tile counts
- manifest fetch should complete in under 500ms on localhost
- first score layer install should complete in under 1500ms on localhost
- browser console must log manifest load, map load, and score-layer install timings
- map tile failures are correctness bugs, not harmless warnings

The agent must keep fixing until its observable checks pass. Handoff is forbidden unless the agent can state what it ran, what it observed, what was wrong before fixes, and why the current output proves the feature works.

## Recursive Review Contract

After every changeset, explicitly invoke `review-recent-work`. Rerun all relevant observable checks after review fixes. Repeat review, fixes, and checks until no correctness or simplicity issue remains. Record each review pass and resulting edits in the ExecPlan `Decision Log` or `Outcomes & Retrospective`.
