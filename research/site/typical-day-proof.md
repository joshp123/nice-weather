Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# Default views without day-record downloads

Completed locally on 18 October. Not deployed or pushed. This section supersedes the earlier background-record-loading and point-sampling descriptions below.

## What changed

The default taste draws only the selected precomputed score PNG. It no longer fetches `cells.bin`, monthly day records, forecast `days.bin`, temperature-offset images or fine replay packets. Compact named-place scores and facts supply labels, rankings, counts and calendars. The Worker supplies the current-period forecast and unnamed card judgements. Its unnamed climate judgement and compact monthly score bytes now use the same normalised spatial footprint as the browser. Unnamed forecast cards receive all fourteen daily judgements in that one response.

Opening Your taste starts record loading while preserving the default image. The first changed taste waits for those records and its GPU frame. Later slider moves reuse them. A calendar requested by a place card or time picker is remembered but its year records are deferred until Your taste opens or the taste changes. Reset restores the authoritative default image. Changing periods while requests are in flight preserves the latest choice.

The generator now covers all 144 start/length choices: twelve single months and 132 runs of two to twelve months. It also writes the canonical `year` image: **145 PNGs, 13,605,385 bytes (13.61 MB)**. All 133 previously generated images are byte-identical. The twelve added full-year rotations are byte-identical to `year`. Monthly `scores.bin` remains 12,458,880 bytes with the same format. The point and scoring rule is unchanged.

The named-place worker's commits landed before the final build; the final baseline for integration is `a298e8f`. Their named transport code is preserved. The before build was frozen while that work was landing and already used compact point requests, rather than the former 11 MB catalogue transfer. The figures isolate the additional saving from this work.

## Cold visit measurements

Headless Playwright WebKit, fresh context for every route, empty browser cache, light mode, 390 × 844 and 1512 × 982. No network or CPU throttling. These are local laptop measurements at phone and laptop viewport sizes, not physical phone timings. Local static JS/CSS are served uncompressed; production compression and internet latency will change the totals and timings. External basemap requests are included.

The final app was built and served with the current named-data fixture and generated images:

```sh
devenv shell -- npm run build
NICE_WEATHER_GENERATED_DATA=/private/tmp/nwf-default-record-free/data devenv shell -- npm run serve
```

Bytes are HTTP(S) response body sizes, excluding internal blob worker creation, headers and request bodies. MB means 1,000,000 bytes. First paint is WebKit's first contentful paint; weather paint is the first completed weather frame. Settled is the last recorded resource response or weather/named-answer refresh after network idle. Each cell is before → after. The complete request ledgers retain individual URLs, statuses, byte sizes and resource timings.

| Width | Route | Requests | MB | First paint, ms | Weather paint, ms | Settled, ms |
| --- | --- | --- | --- | --- | --- | --- |
| 390 | `/nov-mar` | 38 → 25 | 15.115 → 2.945 | 162 → 146 | 269 → 285 | 594 → 436 |
| 390 | `/year` | 52 → 25 | 32.061 → 2.966 | 85 → 83 | 222 → 202 | 523 → 358 |
| 390 | `/july` | 30 → 25 | 5.468 → 2.958 | 90 → 90 | 215 → 186 | 371 → 517 |
| 390 | `/` | 32 → 29 | 7.171 → 3.000 | 77 → 81 | 222 → 210 | 333 → 481 |
| 1512 | `/nov-mar` | 38 → 25 | 15.115 → 2.946 | 80 → 88 | 226 → 214 | 408 → 289 |
| 1512 | `/year` | 52 → 25 | 32.062 → 2.966 | 82 → 88 | 239 → 213 | 564 → 672 |
| 1512 | `/july` | 30 → 25 | 5.468 → 2.958 | 81 → 84 | 215 → 191 | 352 → 332 |
| 1512 | `/` | 32 → 29 | 7.177 → 3.005 | 81 → 82 | 226 → 240 | 336 → 452 |

Raw day-record requests fell from 5 / 12 / 1 / 1 to **zero on every route and viewport**. Their removed body payloads were 11,200,275 / 26,880,660 / 2,240,055 / 3,920,085 bytes respectively, in route order. The remaining selected climate image is only 76,424 bytes for November to March, 97,027 for year and 89,310 for July. No cells or offset grids are fetched at default either.

Settled times include the compact facts arriving from the Worker and vary between runs; several did not improve in this final pass. Forecast weather still paints near a quarter second without the 3.92 MB browser day-record download. These local measurements do not claim every timing improved.

Evidence: `/private/tmp/nwf-default-record-free/before/results.json` and `final/results.json`. Screenshots: `before/` and `final/`, named `390-nov-mar.png`, `1512-year.png`, `390-july.png`, `1512-forecast.png` and the other route/viewport combinations. Screenshots were opened and inspected; controls, colours, labels and headlines remained readable and unchanged, without horizontal overflow.

The repeatable check is [`check_default_records.py`](check_default_records.py):

```sh
# With Playwright installed and the built app served on port 5198:
python research/site/check_default_records.py /tmp/nwf-default-record-free after
```

## Taste and card checks

A second visitor pass blocked all raw-record, cells, offset and fine-replay browser requests. San Francisco's July, whole year, November to March, fourteen-day forecast and one forecast day still showed their complete cards, counts, calendars, labels and headlines. Both laptop light and phone dark passed with zero blocked requests attempted and no JavaScript errors. Changing taste then fetched the forecast records; reset reproduced the original card exactly. The custom climate calendar retains all twelve scored months after the deferred year loads.

Evidence: `/private/tmp/nwf-default-engine-proof/flows.json`, `laptop-july.png`, `phone-nov-mar.png`, `laptop-forecast.png`, `phone-day.png`, `calendar-after.json` and `calendar-after.png`. The independently held-request checks also cover a period switch and reset during downloads, prefetch without altering the default map, and a failed download followed by retry: `races.json` in that directory.

Measured actual UI input-to-update times, including the first record download. Eight later moves were measured per case. The card case requests the complete year for its calendar; the no-card case needs only the five selected months. The prefetch case waits for records after opening Your taste before moving a slider.

| Viewport | November to March case | First input, ms | Later median / maximum, ms | Raw records |
| --- | --- | --- | --- | --- |
| laptop | immediate-card | 1457 | 120 / 154 | 12 files, 26.88 MB |
| laptop | prefetched-card | 1652 | 108 / 118 | 12 files, 26.88 MB |
| laptop | immediate-no-card | 370 | 53 / 134 | 5 files, 11.20 MB |
| phone | immediate-card | 1119 | 122 / 248 | 12 files, 26.88 MB |
| phone | prefetched-card | 1799 | 119 / 142 | 12 files, 26.88 MB |
| phone | immediate-no-card | 256 | 49 / 56 | 5 files, 11.20 MB |

Opening Your taste starts prefetching without changing the default map. Prefetch removes the download wait but does not remove the first GPU and full-calendar scoring cost: a first changed-taste card update still took 1.12–1.80 seconds. Later input-to-update maxima were 154 ms laptop and 248 ms phone viewport. Records arrived 58–254 ms after opening the controls in these local cases. Evidence: `/private/tmp/nwf-default-engine-proof/taste-timings.json`.

The independent review found and then verified the fix for unnamed forecast calendars. At `31.12345, 10.23456`, one compact Worker response supplies all fourteen daily verdicts; aggregate-to-day selection reuses them, and a cold day URL also has the scored calendar and card strip. No raw record requests or page errors occurred. Screenshots were inspected. Evidence: `/tmp/nwf-record-free-cold/recheck.md`, `recheck.json` and `recheck-aggregate-calendar.png`. No material findings remain in that focused recheck.

## Image upload and release steps

Generated files are under `/private/tmp/nwf-default-record-free/data/v6/climate`. A clean upload folder contains **only the 145 PNGs**, with no named data, forecast records, offsets or `scores.bin`:

`/private/tmp/nwf-default-record-free/upload/v6/climate`

Upload these images before releasing the app. Default views deliberately report a missing image instead of silently downloading day records. The year, season and existing month URLs retain their names.

```sh
# Operator step, not run during this task. Supply the existing publish token
# through NICE_WEATHER_PUBLISH_TOKEN without printing it.
devenv shell -- node hosting/sites/publish.mjs files \
  /private/tmp/nwf-default-record-free/upload \
  https://nice.jjpcodes.com v6/climate
```

The publisher accepts the PNGs as they are and gzip-compresses their upload. Afterwards build and deploy the app and Worker through the existing Sites flow. No published climate data was changed by this task.

To regenerate after the data owner's rebuild, use the same generator and put outputs in a scratch folder, then copy only `scores-typical-day-*.png` into a clean upload tree:

```sh
devenv shell -- src/engine/default_scores.py artifacts/v6 \
  --images /tmp/nwf-default-record-free-regenerated/v6/climate
```

The generator requires NumPy and Pillow, declared in its script metadata. The generated images here used the current local `artifacts/v6` records and offsets and completed in 19.1 seconds. `images.json` in the evidence directory records coverage, byte counts and exact equality of every existing image and full-year alias.

Build, Worker syntax and whitespace checks passed. No paid services were used. No deployment or push was performed.

---

# Earlier typical-day implementation evidence

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
