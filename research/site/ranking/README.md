Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# One place ranking

All app answers, lists, van suggestions and poster answers use `src/ui/ranking.ts`. The daily outlook keeps its native Go implementation in `outlook/`; sharing executable code would require adding another runtime to the browser or forecast job. The production implementations are checked against each other using actual forecast data.

World candidates have at least 300,000 residents. Continent and USA candidates have at least 50,000. At zoom 5 and above, the app uses all named places in the visible area. World and continent answers cover the named region, so the phone and laptop agree despite their different aspect ratios. Exact-place candidate loading uses that same region rather than clipping the ranking to the screen; its existing 128-candidate budget is retained.

Rank by displayed whole nice days, then whole perfect days, population and slug. Use the largest eligible place within 50 km and five unrounded score points, with that place's own numbers and coordinates. Winning spots and named places must both be at least 250 km apart. Sort the named results by the same day-count order.

## Forecast comparison

For tomorrow, 19 October 2026, using forecast run `20261017T0000Z`, the production Go ranking and browser TypeScript ranking agreed:

| Region | Both implementations |
|---|---|
| World | Chengdu, Lahore, Delhi |
| Europe | Rome, Milan, Rostov-on-Don |
| USA | Los Angeles, Houston, Phoenix |

`results.json` records the comparison. The check also independently obtained the app engine's real GPU scores and counts for all outlook candidates and checked the resulting names, then moved the actual map to each region and checked its visible headline. No browser errors occurred. The browser date was fixed to 18 October to make the meaning of tomorrow repeatable.

Fresh light-mode first views were captured and inspected at 1512 × 982 and 390 × 844 in `/tmp/nwf-agents/round5/ranking/`. The build and `go test ./outlook ./research/site/ranking/port` passed. No deployment or upload was performed.

## Repeat locally

Build and serve the app on port 5198. Generate actual outlook candidates:

```sh
devenv shell -- sh -c 'go run ./research/site/ranking/port -source artifacts -places artifacts -static artifacts/v6 -now 2026-10-18 > /tmp/nwf-agents/round5/ranking/outlook-candidates.json'
```

Bundle the production browser rule for the browser-only proof route:

```sh
devenv shell -- node --input-type=module -e "import {build} from 'esbuild'; await build({entryPoints:['src/ui/ranking.ts'],bundle:true,platform:'node',format:'esm',outfile:'/tmp/nwf-agents/round5/ranking/ranking.mjs'});"
```

Run `check.py` through the user-required headless Playwright environment:

```sh
devenv shell -- sh -c 'cd /tmp && UV_CACHE_DIR=/tmp/nwf-uv-cache uv run --quiet --with playwright python -I /Users/josh/code/nice-weather-finder/research/site/ranking/check.py'
```
