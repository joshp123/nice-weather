Written by AI on 2026.10.17 by model GPT-6.1 Sol.

# Forecast runner handoff

Implemented locally; not pushed or deployed. The Go runner now produces typical-day forecast images using the same selected point as the app. The earlier deployment blocker in `typical-day-proof.md` is closed.

## Change

`internal/dailyforecast/v6_scores.go` reads the fine point's own coarse weather cell and temperature correction. Each daily PNG floors that day's score. `scores-all.png` finds the threshold crossing where half the fourteen days reach a band, using the same continuous interpolation as the app. It no longer averages day scores or blends neighbouring weather cells.

The runner writes `days.bin`, fourteen daily score PNGs, the fortnight PNG and the forecast pointer. Searching all Go writers found no runner writer for climate `scores.bin` or place `facts.bin`; those formats and published files remain unchanged.

Checking real records exposed floating-point differences at exact score thresholds in the existing CPU and GPU paths. The CPU, GPU, Worker, Python generator and Go writer now resolve the final interpolated day score to four decimal places before counting thresholds, and do the same for the resulting typical score. This keeps an exact 85 from becoming 84.999999 on one path. The share renderer uses the app's calculation.

## Build and run

The README build recipe produces `artifacts/runner/niceweather-linux-amd64`, with `refresh.md`, `LICENSE.libaec` and `LICENSE.pvlib` beside it. Generated artifacts remain ignored by Git, as before. The executable is a stripped, statically linked Linux x86-64 ELF with `CGO_ENABLED=0`. An independent rebuild with the same recipe was byte-identical. The final artifact's build metadata points to the committed forecast scoring change.

Linux executable SHA-256:

```
6505512681e914588cadc21005ffbecd7f590b94928cf084b47ffc0eef9197fb
```

This host is a Mac. The local end-to-end run uses a native executable built from the same final Go source and flags, without the Linux target variables. The Linux executable was inspected and independently rebuilt; it was not executed in a Linux runtime.

The run reads a frozen copy of the live Site's `cells.bin`, `points.bin` and October offsets from `/private/tmp/nwf-runner-typical-20261017/static`. Their hashes and equality with the local published data are recorded in `static-inputs.json` beside that folder. ECMWF fields are freshly downloaded from the runner's normal public source, rather than replayed fixtures. No publishing flags or credentials are used.

Final run command:

```sh
/private/tmp/niceweather-typical-native forecast \
  --out /private/tmp/nwf-runner-typical-20261017/verified \
  --v6-static /private/tmp/nwf-runner-typical-20261017/static
```

The final executable selected ECMWF's complete **2026-10-17 00 UTC** run, producing 17–30 October. It completed in **3 minutes 25.191 seconds**, downloaded **433,938,015 bytes**, and wrote **17 run files totalling 5,299,193 bytes**, then `current.json`. The day file is **3,920,070 bytes**, exactly `18,667 × 14 × 15`. Its fifteen images total **1,379,046 bytes** and took **2.592 seconds** to write. `previousRun` is empty because this is a new scratch folder. Uploaded bytes: zero.

The complete log is `/private/tmp/nwf-runner-typical-20261017/verified-forecast.log`. An earlier completed run in the sibling `final` folder preceded the precision fix and is not the final handoff. Use **`verified`**. That root also contains newly regenerated `v6/climate/scores.bin` and all 133 climate images using the final precision rule; published climate data was not edited.

## Verification

The actual Go writer and Python image generator agree pixel for pixel across all fifteen images: 15,573,600 pixels, including sea pixels. Actual headless WebKit GPU readback was checked separately from the precomputed image, against the CPU point judgement. The standard five climate examples retain their accepted bands and day counts. Three discovered forecast boundary points now also agree: the two Virginia points give 47.5 (PNG byte 47), and 31.25° N, 76.5° E gives 85 and seven perfect days. The Worker agrees with the CPU on these examples too.

The final fresh producer's fifteen PNGs also match Python pixel for pixel, and replaying the same records through the Go writer reproduces them. Fresh-run evidence is under `/private/tmp/nwf-go-typical/`: `comparison-producer-fresh.json`, `comparison-precision-fresh.json`, `precision-app-fresh.json` and `worker-precision-fresh.json`.

| Place, next fourteen days | PNG and displayed score | Nice or better | Perfect | Awful |
| --- | --- | --- | --- | --- |
| Tenerife south coast | 93 | 14 | 11 | 0 |
| Rotterdam | 12 | 0 | 0 | 14 |
| Málaga | 85 | 11 | 7 | 0 |
| San Diego | 87 | 13 | 8 | 0 |
| Cape Town | 85 | 10 | 7 | 2 |

Single-day checks include Málaga on 18 October (39, Poor) and 30 October (67, Nice), and San Diego on 23 October (89, Perfect). These retain their own day's score. The actual GPU scores can differ from CPU scores by less than five millionths because its texture stores float32 values; their bands, floored scores and day counts agree.

A reviewer independently compared decoded final producer PNGs with the app's current point sampler at San Diego, Málaga, Cape Town, Tenerife south coast, Rotterdam, Singapore, London and Melbourne. Every single day and the fortnight agree: **120 comparisons, zero differences**. Its report and numerical evidence are `/private/tmp/nwf-typical-cold-review/runner-review.md` and `runner-parity-results.json`. No material findings.

## Visitor checks and performance

The built app was served with the final scratch folder as `NICE_WEATHER_GENERATED_DATA`. Headless Playwright WebKit checked 390 × 844 and 1512 × 982, light and dark. All six screenshots below were opened and visually inspected. Cards and controls fit; selected scores agree with map labels. Holding `days.bin` pending confirms the fortnight and single-day maps paint from the runner's images before the raw weather arrives, then finish loading successfully after release.

Screenshots under `/private/tmp/nwf-runner-typical-20261017/screenshots/`:

- `webkit-light-390-malaga-next-14-days.png`
- `webkit-dark-390-san-diego-day-6.png`
- `webkit-light-1512-cape-town-next-14-days.png`
- `webkit-dark-1512-malaga-day-13.png`
- `webkit-first-view-next-14-days.png`
- `webkit-first-view-day-6.png`

`visitor-checks.json` records the cards, links, counts, layout checks and absence of browser errors. The reproducible visitor script is `research/site/check_forecast_runner.py`.

Final engine timings used eight slider redraws per period in headless WebKit, with no other browser checks running. The phone figure means a phone viewport on this laptop, rather than a physical phone. Both meet the requested 0.2 s laptop and 1 s phone limits.

| Viewport | Fourteen days | Single forecast day |
| --- | --- | --- |
| 1512 × 982 | 10–32 ms, median 17 ms | 16–18 ms, median 16.5 ms |
| 390 × 844 | 12–33 ms, median 16.5 ms | 16–17 ms, median 17 ms |

Raw timing evidence: `/private/tmp/nwf-go-typical/fresh-timings.json`.

`devenv shell -- npm run build`, Worker syntax checking, Python compilation, `git diff --check`, `go test ./internal/... ./cmd/...` and the final `go test ./...` all pass. An earlier full Go check failed while concurrent outlook dependencies were incomplete; that work now passes too.

After the verified build and run, another worker added uncommitted outlook generation to `cmd/niceweather/main.go` and poster uploads to `internal/dailyforecast/publish.go`. Those later edits are not included in this pinned executable, and were not changed or committed by this task. A combined forecast-and-outlook release must rebuild and verify its runner after that integration is complete. The completed forecast-scoring change and its binary are ready independently.
