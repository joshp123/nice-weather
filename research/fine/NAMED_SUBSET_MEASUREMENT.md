Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# Named-place data size, measured without upload

The existing file format can reduce the full 18.54 GB carrier to **3.98 GB for every named place**, or **1.66 GB for all map labels plus search places with population at least 50,000**. The correction tiles dominate the latter option. **A sparse active release does not fall back to the 25 km product today. It must not be activated as it stands.** Keeping the fine pointer absent preserves the current product.

No data was copied into a release, sealed, uploaded or activated for this measurement. All sizes are decimal bytes of actual existing files, before HTTP transfer compression of text. Source: `v6-fine-b1-20261018` on the SSD, generation `e68c86ba3e24654a499ae4ef37ad35f9`.

## File unions

The catalogue inputs are 15,000 label rows and 170,550 place-list rows. They overlap. All named places together contain 170,513 distinct coordinates and 169,013 distinct 1 km pixels. Of these, 168,982 pixels / 170,482 coordinates have valid correction, timezone and native replay support. The remaining 31 pixels lack support even in the full worldwide release; all 15,000 label rows have support.

Each row below keeps all timezone variants of every native ERA5 point hosting an included supported place, the twelve current correction tiles containing those places, their timezone maps, and the same 37 runtime/catalogue/licence/provenance files described below. A 0.25° native point can also serve unnamed nearby pixels where the corresponding correction tile is present; the measured subset is not a strict named-coordinate whitelist.

| Included places | Supported distinct 1 km pixels | Own native points | Files | Bytes |
| --- | ---: | ---: | ---: | ---: |
| All labels and all place-list entries | 168,982 | 49,512 | 94,246 | 3,976,394,622 |
| All labels; place list ≥10,000 population | 44,807 | 23,427 | 57,144 | 2,530,854,794 |
| All labels; place list ≥50,000 population | 15,149 | 10,156 | 35,611 | 1,657,197,246 |
| All labels alone | 14,952 | 10,048 | 35,487 | 1,651,962,426 |
| Both catalogues ≥50,000 population | 12,332 | 8,497 | 32,233 | 1,514,560,221 |
| Both catalogues ≥100,000 population | 6,258 | 4,674 | 23,881 | 1,168,471,909 |
| Both catalogues ≥500,000 population | 1,209 | 1,074 | 9,334 | 501,773,826 |

Filtering the place list above 50,000 while retaining all labels saves little more: ≥100,000 is 1,653,307,782 bytes; ≥500,000 is 1,652,199,817 bytes. Filtering both catalogues reduces coverage of named map labels and the coastal towns this work is intended to improve. I would retain all labels if choosing among these existing-format options; that still leaves a substantial travel upload.

The all-labels-plus-50,000 option consists of:

| Component | Files | Bytes |
| --- | ---: | ---: |
| Own-native replay packets, all target-clock variants | 10,796 | 424,771,385 |
| Twelve correction tiles at each required geographic tile | 22,872 | 1,226,194,556 |
| Target-timezone tile maps | 1,906 | 1,419,049 |
| Runtime metadata, full label catalogues, notices and provenance | 37 | 4,812,256 |
| Total | 35,611 | 1,657,197,246 |

Retaining only the exact native-point/target-clock pairs used by those named coordinates reduces this option to 35,028 files / 1,634,233,529 bytes. That saves 22,963,717 bytes and gives less incidental coverage of nearby unnamed points. The all-named equivalent is 91,228 files / 3,856,461,603 bytes.

## What the consumer actually reads

`createFinePlaceLoader` in `hosting/sites/fine-place.mjs` reads the active pointer, replay manifest, one target-clock tile, one native-point/target-clock replay packet, and twelve correction tiles. The Worker separately reads the active label catalogues. It does **not** read the native reference normals, packet index, correction-tile index, or build inventories.

The 37-file tier includes the existing replay manifest; all four label files; root, replay and temperature attribution; temperature configuration/source provenance; and the complete underlying land-membership and timezone licence/data packages. This retains the supplied ODbL source data and licence material rather than dropping it to claim a smaller measurement. The exact list is in `measurement.json`.

Keeping every unchanged ancillary file instead adds 52 files / 136,267,412 bytes to each row. For all labels plus places ≥50,000 that is 35,663 files / 1,793,464,658 bytes. This includes the 66,836,586-byte native normals, full-world packet/tile indexes, source overviews and audit inventories. They are unnecessary for these runtime reads. The full-world inventories and counts must not be presented as evidence that a future subset is complete: a publishable subset would need its own manifest, filtered inventories/checksums and audit. Those have not been generated or sealed here. The tables are exact unions of current assets, not a ready upload package; future replacement metadata will add its measured size.

The smallest possible temperature payload alone would be much smaller than the current 256 ×256 tiles: 24 signed 16-bit monthly high/low offsets require **727,152 bytes** for the 15,149 supported fine pixels in the all-labels-plus-50,000 option, or **8,111,136 bytes** for all 168,982 supported named pixels. These are arithmetic payload sizes, not a built format or complete package: they exclude point addresses, source/support fields, metadata/indexes, clocks, replay and compression. This would require a consumer format change. No such change was made.

## Actual missing-point behaviour

`named_subset_fallback.mjs` executes the real Worker's `/api/place` and `/api/fine/replay` routes. Its local bucket reads the actual data, but hides exactly the files outside the measured all-labels-plus-50,000 union. It neither changes source files nor creates a sparse release. The test point is an unnamed supported land pixel at 82.52083333333333° N, 81.5375° W, outside that union's native points.

| Local state | `/api/place` | Monthly facts | Judgement | `/api/fine/replay` |
| --- | ---: | --- | --- | ---: |
| Fine pointer absent | 200 | 12 current-product months | Omitted, as in today's API | 404 |
| Full fine data active | 200 | 12 fine months | Score 16.6667 | 200 |
| Measured sparse assets active | 200 | `null` | `null` | 404 |

The sparse response still advertises `fineReplay: true` and `resolutionArcSeconds: 30`. `typicalYearForPlace` and `defaultPeriodJudgement` return `null` when an enabled fine loader has no packet, rather than trying the existing overview path. In the app, the active-fine `judgePoint` branch likewise returns `FinePlaces.judge` directly; a completed 404 yields `null` and does not reach `PointSampler`. **This is an unavailable card, not a clean fallback.** No implementation fix was made in this measurement task.

## Reproduce

The measurement uses Python's standard library and the actual compressed files. It checks each candidate pixel's correction support and target timezone, joins the existing replay inventory, stats every required correction tile and writes per-option file lists with byte sizes. It does not approximate size from a sample.

```sh
python3 research/fine/measure_named_subset.py \
  --release '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/v6-fine-b1-20261018' \
  --output '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/b1-worldwide/named-subset'

devenv shell -- node research/fine/named_subset_fallback.mjs \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/v6-fine-b1-20261018' \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/b1-worldwide/named-subset' \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/b1-worldwide/queries.json' \
  '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-validation/b1-worldwide/named-subset/fallback.json'
```

Private measurement evidence stays under that SSD validation folder: `measurement.json`, ten compressed exact file inventories, and `fallback.json`. Catalogue hashes and the original generation identify the measured inputs. Station observations are not included. This report makes no claim about Sites pricing or storage quota; that is a separate read-only check.
