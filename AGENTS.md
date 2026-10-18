---
written_by: AI (Claude Opus 5.5), 2026-10-16, updated 2026-10-17 for v6, from Josh's steering
---

# AGENTS.md

Josh owns this project. His own words are the authority; they are kept verbatim in private notes (`.agent/`, not in this repository). Read them before changing what the product does. Anything written by an agent, including this file, is secondary.

## What it is

Nice Weather is a map of the world that answers one question: where on earth, and when, is the weather nice? Nice means t-shirt-and-shorts weather from 10:00 until the walk home at 21:00, without rain, muggy air or a gale, by the visitor's taste (Josh's by default).

- **Next 14 days** from ECMWF's free forecast, each day and all 14 together.
- **Typical year**: each month, the four seasons (Dec to Feb, Mar to May, Jun to Aug, Sep to Nov) and the year, from real days of ERA5.
- **Taste sliders** in real units (felt temperatures, dew point, km/h, mm). The whole world redraws in tens of milliseconds.
- Click or search a place: its score, the band word, nice days, and why. Every view has a shareable URL (`/lisbon-portugal/july`, `/@36.72,-4.42,7z/november`) with its own link preview; Copy image makes a share card.

Josh's taste, in his words, is in those private notes. The scoring method is Fable's graded rule: `research/taste/nice_v6.py`, explained for the public at `/method` (`method.html`).

## How it works

```
ERA5 hourly (free Earthmover archive) ─► research/climate-v6 ─► v6/cells.bin, points.bin, climate/month-MM.bin, offsets-MM.png, facts.bin, scores.bin
ECMWF IFS open data ─► niceweather forecast (daily, on Sites) ─► v6/forecast/<run>/days.bin
                                   │
                    hosting/sites/publish.mjs uploads to Sites storage
                                   │
Sites Worker (hosting/sites/worker.mjs): serves the app and /data, answers /api/place, /api/search, /api/slug, and gives each URL its own preview tags
                                   │
Browser: src/engine scores eight real days per 1° square and month on the GPU for the current taste, adds 25 km temperature detail, and draws; src/ui is the interface
```

- The rule lives in `research/taste/nice_v6.py` (reference) and `src/engine/taste.ts` (the browser). They must agree.
- Formats and grids: `.agent/v6-contract.md` (private notes).
- The interface is a poster on one grid: a top bar (Nice!, the answer, search and tools), the map fitted between the bars, and a bottom bar (when, and the key); panels open on the same outer margin. Every design value is in `src/design-tokens.css`: one control height, one panel style, two weights, four sizes, colour only for weather.
- Live site: <https://nice.jjpcodes.com> (ChatGPT Sites, free, public). Local: `npm run build && npm run serve` on <http://127.0.0.1:5198>, the only local port.

## Engineering rules

- The Zen of Python, especially: there should be one obvious way to do it.
- As simple as possible, no simpler. No model soup, fallback providers, browser scoring, release machinery or compatibility layers.
- Long, plain names instead of comments. No defensive code for failures that do not happen.
- Prefer no tests over useless tests. Keep tests that protect a real behaviour.
- No proof porn: checks must test what Josh would test.

## Check your own work

Josh's rule: agents must see and debug the working product themselves, with no human in the loop.

1. Data: score known places (Málaga, Rotterdam, Chania, Singapore, Cape Town, San Diego, San Francisco) from the published files and compare with Josh's own words about them.
2. Site: run it, click places, switch day, month and season, drag every slider, copy a link and an image, and look at screenshots at phone, laptop and ultrawide sizes. Reject clipped text, misaligned controls, unclear explanations or slow loading. `research/site/layout-audit/check_layout.py` screenshots every screen at Josh's sizes, light and dark, and must report no overlaps, alignment faults or low contrast.
3. For product milestones, ask a reviewer with no context to judge the live site against Josh's verbatim steering only.
