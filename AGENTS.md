---
written_by: AI (Claude Opus 5.5), 2026-10-16, from Josh's steering
---

# AGENTS.md

Josh owns this project. His own words are the authority: `.agent/josh-steering-ledger.md` holds his messages verbatim. Read them before changing what the product does. Anything written by an agent, including this file, is secondary.

## What it is

Nice Weather Finder is a map of the world that answers one question: where is the weather nice? Josh uses it to decide where to go, camper-van and digital-nomad style.

- **Next 14 days**: an overview of how many days are nice, and each day on its own.
- **Year**: what each month is normally like, from real days 2006–2025.
- **Nice** and **Cycling**: Cycling is stricter about wind and rain.
- **Lenses**: Overall, Comfort (temperature and humidity), Dry, Clear sky, Calm.
- Click anywhere for the place name, the answer, and why: real figures in plain words.

Josh's taste: warm is good, 22–27 °C is ideal, 16 °C is mediocre; humid heat like Singapore is not nice; British and Dutch winters are awful; southern Spain in November is often great. Presentation follows Ryan Hall's nice-weather map (clear colour bands with rounded edges, quiet dark map, big readable city names); the yearly journey follows Theo Jaffee's and WeatherSpark's ideas.

## How it works

```
ECMWF IFS open data ──► niceweather forecast ──► forecast/<run>/  (PNG per day and layer, points per day)
ERA5 2006–2025 result ─► niceweather climate-convert ─► climate/ (PNG per month and layer, points per month)
GeoNames cities ───────► hosting/sites/places.mjs ───► places.tsv
                                   │
                    hosting/sites/publish.mjs uploads to Sites storage
                                   │
Sites Worker (hosting/sites/worker.mjs): serves the app and /data, answers /api/place and /api/search
                                   │
Browser (src/): draws one PNG grid as smooth bands; shows the place card the Worker returns
```

- One scoring function: `internal/weather/score.go`. The pipeline scores; the browser never does.
- One grid: 0.25°, 1440 × 721, for forecast and climate.
- PNG layers are 8-bit greyscale: value 0–100, 255 = no data. Open one to see the map.
- Live site: <https://nice.jjpcodes.com> (ChatGPT Sites, free, public). Local: `npm run build && npm run serve` on <http://127.0.0.1:5198>, the only local port.

## Engineering rules

- The Zen of Python, especially: there should be one obvious way to do it.
- As simple as possible, no simpler. No model soup, fallback providers, browser scoring, release machinery or compatibility layers.
- Long, plain names instead of comments. No defensive code for failures that do not happen.
- Prefer no tests over useless tests. Keep tests that protect a real behaviour.
- No proof porn: checks must test what Josh would test.

## Check your own work

Josh's rule: agents must see and debug the working product themselves, with no human in the loop.

1. Data: open the PNGs, decode points for known places (Málaga, Rotterdam, Chania, Singapore, Denver, Cape Town) and compare with ECMWF's point forecast or WeatherSpark.
2. Site: run it, click places, switch day, month, profile and lens, and look at screenshots at phone, laptop and ultrawide sizes. Reject clipped text, misaligned controls, unclear explanations or slow loading.
3. For product milestones, ask a reviewer with no context to judge the live site against Josh's verbatim steering only.
