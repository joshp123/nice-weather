Written by AI on 2026.10.17 by model GPT-6.1 Sol.

# Typical days and day counts

Implemented locally; not pushed or deployed. Josh accepted the proposal's point-choice differences after review. The daily forecast writer handoff is completed in [forecast-runner-proof.md](forecast-runner-proof.md).

## Product behaviour

- Longer periods use the calendar-weighted median band. Threshold shares give the continuous score used for colour. Single forecast days retain their own scores.
- Cards, share images and preview descriptions count nice-or-better, perfect and awful days. Numbers are floored scores and rounded day counts, so rounding cannot promote a score into another band.
- The compact picker supports one month, any consecutive run, the four seasons, November to March and the year. Forecast navigation offers all fourteen days or one day. Existing period URLs continue to work.
- The headline ranks all named label points in the current view, or the world at world zoom, by nice days then perfect days. Answers stay 250 km apart; Palestine remains excluded.
- Browser, Worker and image generator select the nearest land fine point from the exact coordinate, then use that point's own coarse cell and temperature correction. Coordinate API requests and links retain their coordinates.
- Link images use `v6/cards/typical-day/<place>/<period>.png`. A missing image produces a text preview with the correct counts. Coordinate links use text previews. Old annual images cannot silently describe a different point or period.

## Visitor checks

Built with `devenv shell -- npm run build`, and served with:

```sh
NICE_WEATHER_GENERATED_DATA=/private/tmp/nwf-typical-data devenv shell -- npm run serve
```

Headless Playwright WebKit checked the five examples at 390 × 844 and 1512 × 982, light and dark, plus 3840 × 1080 dark. Screenshots were opened and visually inspected. No horizontal overflow, clipped period controls or overlapping card and time bar were found. Phone cards scroll to their remaining weather details.

The visitor flow also selected October to April, the winter shortcut, fourteen forecast days and one forecast day. The resulting URLs retained the selected period. Searching for San Diego and clicking its map label produced the same score (88), counts (308 / 224 / 27), and canonical place URL.

Precomputed first paint was checked separately with raw monthly weather requests held pending: year, November to March, and January to December all painted their score images before the raw records arrived. January to December correctly reused the year image. The image URL has a new query key to avoid reusing immutable cached images from the former mean rule.

Evidence directory: `/private/tmp/nwf-typical-day-proof`.

| Evidence | File |
| --- | --- |
| Phone, Tenerife year | `webkit-light-390-tenerife.png` |
| Phone, Rotterdam January | `webkit-dark-390-rotterdam.png` |
| Phone, wrapped run | `webkit-light-390-custom-run.png` |
| Laptop, Málaga January | `webkit-dark-1512-malaga.png` |
| San Diego map click | `webkit-san-diego-map-click.png` |
| Ultrawide | `webkit-ultrawide.png` |
| Share image | `cape-town-share.png` |
| Precomputed first paint | `webkit-first-view-year.png`, `webkit-first-view-nov-mar.png`, `webkit-first-view-jan-dec.png` |
| Complete visitor results | `visitor-checks.json` |
| Preview HTML and PNG checks | `preview-checks.json` |
| Held-download results | `first-view.json` |
| Opened proposal page | `proposal-year.png`, `proposal-nov-mar.png` |

The reproducible visitor script is `research/site/check_typical_days.py`. It accepts `--interactions-only` to reuse a completed visual pass, and `--first-view-only` to hold downloads and inspect first paint. Playwright and its WebKit browser must already be installed.

## Comparison with the proposal

Counts below are nice-or-better / perfect / awful. The exact proposal coordinates were checked independently as well as the named-place URLs. The rounded named coordinates select the same final fine points in these examples.

| Place and period | Band | App counts | Proposal counts |
| --- | --- | --- | --- |
| Tenerife south coast, year | Perfect most days | 350 / 209 / 0 of 365 | 343 / 259 / 4 |
| Rotterdam, January | Awful most days | 0 / 0 / 31 of 31 | 0 / 0 / 31 |
| Málaga, January | Fair most days | 12 / 4 / 8 of 31 | 12 / 4 / 8 |
| San Diego, year | Perfect most days | 308 / 224 / 27 of 365 | 312 / 225 / 19 |
| Cape Town, November to March | Perfect most days | 147 / 98 / 0 of 151 | 147 / 102 / 0 |

**The requested one-or-two-day tolerance does not pass for three places.** The proposal's export (`research/periods/export.py`) calls its temporary `lib.point_days`, then rounds each daily score to a byte before counting bands. That helper chooses its coarse weather cell from the original coordinate, and separately chooses a temperature correction around a rounded fine coordinate. Its search measures distance from the rounded coordinate. The implementation instead chooses one land point using the exact coordinate, and takes both its owning weather cell and correction. This follows the published point construction in `research/climate-v6/static.py` and the requested single-point rule.

Tenerife's fine point is at 28.00° N. Its owning coarse row differs from the original 28.05° N coordinate's row; using the original row with that point's correction explains the large perfect-day difference. San Diego's sea-adjacent point also changes under the exact-distance selection. Rounding daily scores before counting adds further threshold differences. For example, one Cape Town sample scores 84.9113: the proposal rounds it to 85 and counts 3.875 more perfect days. This implementation counts the unrounded score, then rounds only the displayed count.

The cold reviewer independently reproduced the differences and found no other material issue in the checked controls, links, metadata, layouts or share card. Its detailed evidence is `/private/tmp/nwf-typical-cold-review/review.md`.

## Performance

All measurements use headless WebKit on this laptop. “Phone” means the 390 × 844 viewport, not a physical phone or CPU emulation.

| Measurement | Before | After |
| --- | --- | --- |
| Isolated engine, January, eight slider redraws | 9–17 ms | 16–34 ms |
| Isolated engine, year, eight slider redraws | 11–26 ms | 16–17 ms |
| Isolated engine, phone viewport, January | Not measured | 16–18 ms |
| Isolated engine, phone viewport, year | Not measured | 15–18 ms |
| Full app, laptop, input to updated card, five changes | Not measured | 68–95 ms |
| Full app, phone viewport, same flow | Not measured | 67–81 ms |

Reading day counts for 20,000 points, including the first GPU readback, took 16 ms at laptop size and 15 ms at phone size. The full app includes label and headline work. All measured redraws meet the requested limits. Baseline and engine results are in `/private/tmp/nwf-typical-engine/{before,points}.json`.

## Generated data and deployment handoff

No published climate files were edited. New outputs are under `/private/tmp/nwf-typical-data`:

- `v6/climate/`: 133 typical-day score PNGs and the unchanged-format monthly `scores.bin`.
- `v6/forecast/20261017T0000Z/`: fourteen single-day PNGs and the fortnight PNG.
- `v6/cards/typical-day/`: four world-period cards and five example place-period cards, rendered from the actual app share function.

Regenerate after the data owner's rebuild; these outputs describe the currently available local data. `src/engine/default_scores.py <v6-folder> --images <output-folder>` writes climate images and scores only to the chosen output. Add `--forecast <run-folder>` for the forecast images. Use the existing project Python environment with NumPy and Pillow. The preview renderer accepts `--out` and `--periods`, including arbitrary runs.

The original forecast-writer handoff is now complete: `internal/dailyforecast/v6_scores.go` uses the threshold-median rule, selected point's owning cell and floored score bytes. Josh extended edit ownership for that follow-up. See [forecast-runner-proof.md](forecast-runner-proof.md) for its fresh ECMWF run and image comparisons.

Build, Worker syntax check, Python compilation and `git diff --check` passed. No paid services were used. Other workers' `research/fine` changes were left out of these commits.
