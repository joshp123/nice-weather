Written by AI on 2026.10.18 by model GPT-6.1 Sol.

# One place judgement in the Go outlook runner

`internal/placenumbers.PlaceNumbers` is the Go port of the shared
`hosting/sites/fine-place.mjs` function. Both support the same fine climate
replay and existing compact forecast records. The Go entry point owns the
curves, daily judgement, score-level interpolation, typical-day band and
Nice-or-better, Perfect and Awful counts. `PlaceMonths` reconstructs physical
monthly facts from the compact replay response. `DecodeReplay` reads NWFP v1.
The application and Worker import the JavaScript implementation; the Go runner
requires no JavaScript runtime.

The existing outlooks describe tomorrow's ECMWF forecast. Their named-place
ranking calls the shared Go function on actual `days.bin` records and the
same static cells and monthly offsets used by the forecast runner. It then
blends native-point judgements with the map's normalized 4 × 4 B-spline
footprint. The Worker uses the same shared JavaScript footprint and blend;
the app's existing spatial result is preserved. The score PNG supplies the
painted overview only. This adds no ERA5 temperature
correction to forecast weather and creates no climate-poster mode. The daily
command passes its existing `--v6-static` value to the outlook renderer;
`tools/outlook` accepts `--static-source` for retained data.

## Compare the real implementations

Validation data stays on the SSD. The JSONL port driver reads `{id, packet,
periods, taste?}` and writes the corresponding judgements and physical facts.
Fine packets use the JavaScript loader's expanded representation. Add
`nativeFile` to read the actual gzip NWFP bytes through the Go decoder instead;
the date axis is supplied by `packet.replay.physicalResponse.dates` and the
packet corrections already include its clock offsets. Forecast packets use
`source: "forecast"`, `records`, `offsetsC` and calendar-day `weights`.

Inside `devenv shell`, run:

```sh
go run ./research/fine/outlook/port < input.jsonl > go-results.jsonl
node research/fine/outlook/compare_port.mjs expected.jsonl go-results.jsonl go-parity.json
```

The comparator checks the actual JavaScript outputs, including every weather
fact and score/count field. It excludes only source-status, pixel address and
resolution metadata. It accepts at most 0.0000001 difference. It does not fit
or adjust either implementation. The fixtures are produced by the real loader
and retained weather files, rather than invented day records.

For the worldwide 13-city and 384-random-place cohort, generate fixtures from
the actual upload folder, then exercise the Go binary decoder too:

```sh
node research/fine/outlook/climate_fixtures.mjs \
  --native "$SSD/v6-fine-b1-20261018/replay" \
  --tiles "$SSD/v6-fine-b1-20261018/temperature" \
  --queries "$SSD/fine-validation/b1-worldwide/queries.json" --output "$PROOF"
go run ./research/fine/outlook/port < "$PROOF/input.jsonl" > "$PROOF/go-results.jsonl"
node research/fine/outlook/compare_port.mjs \
  "$PROOF/expected.jsonl" "$PROOF/go-results.jsonl" "$PROOF/go-parity.json"
node research/fine/outlook/native_inputs.mjs "$PROOF/input.jsonl" \
  "$SSD/v6-fine-b1-20261018/replay" "$PROOF/native-input.jsonl"
go run ./research/fine/outlook/port < "$PROOF/native-input.jsonl" > "$PROOF/native-go-results.jsonl"
node research/fine/outlook/compare_port.mjs \
  "$PROOF/expected.jsonl" "$PROOF/native-go-results.jsonl" "$PROOF/native-go-parity.json"
```

Set `PROOF` to an SSD validation directory. The generator records unavailable
places explicitly, consumed file hashes, all 12 months, the year and four
seasons; unavailable places do not count as passed comparisons.

The completed 18 October 2026 run loaded all 397 places (the 13 cities and
384 seeded random land pixels), with no unavailable cases. Both the expanded
JavaScript packet comparison and the independent Go NWFP binary decoder
comparison passed all 211,601 numeric fields across the 17 periods and 12
monthly fact records per place. The largest difference was `2.84e-14`;
scores and day counts had no mismatches.
The run used the complete temperature set: 6,633 spatial tiles per month,
79,596 monthly files, generation `e68c86ba3e24654a499ae4ef37ad35f9`.
Evidence is in `$SSD/fine-validation/b1-worldwide/outlook-climate/`:
`fixtures-provenance.json`, `go-parity.json` and `native-go-parity.json`.

The separate real forecast comparison passed 574 judgements exactly, and
287 retained compact-climate judgements with unequal December–February
calendar weights also matched exactly. These are separate from the fine
climate comparison; the outlook weather remains the native ECMWF forecast.

The real three-region rendering can be checked against independent JavaScript
results for its nine named winners:

```sh
go run ./tools/outlook --source "$SSD" --places-source ./artifacts \
  --static-source "$SSD/v6" --out "$SSD/fine-validation/b1-worldwide/outlook-render" \
  --today 2026-10-17
node research/fine/forecast_module_parity.mjs --data "$SSD" \
  --queries "$SSD/fine-validation/b1-worldwide/outlook-render/queries.json" \
  --output "$SSD/fine-validation/b1-worldwide/outlook-render/actual-place-js"
node research/fine/outlook/verify_posters.mjs \
  "$SSD/fine-validation/b1-worldwide/outlook-render/v6/forecast/20261017T0000Z/outlook" \
  "$SSD/fine-validation/b1-worldwide/outlook-render/actual-place-js/expected.jsonl" \
  "$SSD/fine-validation/b1-worldwide/outlook-render/poster-number-parity.json"
```

`queries.json` contains the nine rendered winners' slug, latitude and longitude
as `{slug, lat, lon}`. All three preview PNGs were opened and inspected; none
of their styling changed. No files were uploaded and no social posts were sent.

## Final forecast integration, 18 October

The later interface change made forecast cards and labels read the map's
B-spline field. Leaving outlooks and previews on their previous nearest point
would disagree: the 397-place cohort yielded 574 available period judgements,
with 234 different displayed scores and 36 different band words. The largest
score difference was 35.1095 points. This was measured before changing the
outlook producer; it was not a temperature correction problem.

`MapPointWeights` and `BlendPlaceNumbers` now port the shared spatial functions
into Go. The outlook ranks the same filtered field as the app and Worker.
A place with no valid map footprint is unavailable, matching the app; it does
not borrow a more distant land value. A one-in-four-decimal rounding case
also exposed a pre-existing arithmetic-order difference in Go: hourly
forecast interpolation must decode each anchor to degrees before interpolating,
matching JavaScript. No rounding tolerance or fitted adjustment was added.

The actual retained forecast proof is reproducible with:

```sh
PROOF="$SSD/fine-validation/b1-worldwide/outlook-refresh/spatial-parity"
node research/fine/outlook/app_forecast_parity.mjs --data "$SSD" \
  --queries "$SSD/fine-validation/b1-worldwide/queries.json" --output "$PROOF"
go run ./research/fine/outlook/port < "$PROOF/input.jsonl" > "$PROOF/go-results.jsonl"
node research/fine/outlook/compare_port.mjs \
  "$PROOF/expected.jsonl" "$PROOF/go-results.jsonl" "$PROOF/go-parity.json"
```

This checks all score, count, share, weather and factor fields, for tomorrow
and the full fortnight at default and non-default tastes. Of 397 coordinates,
287 have support in the existing quarter-degree forecast mask; the remaining
110 are unavailable in both app and Worker. All 1,148 available judgements
matched: scores, day counts and band words exactly, with other numeric fields
within `7.11e-14`. The independent Go footprint and score calculation matched
all 28,700 numeric fields to the same precision. Fine climate's 397-place
coverage is separate from this older forecast mask.

Fresh posters were rendered under
`$SSD/fine-validation/b1-worldwide/outlook-refresh/spatial-render/` using the
new `scores-typical-day-*` filenames. Their nine named winners' displayed
scores match the actual Worker and app. All three preview images were opened
and inspected. The changed spatial model changes the world winners to
Tijuana, Harare and Osaka; Europe remains Sassari, Naples and Lisbon; the USA
becomes San Diego, Lake Havasu City and Fresno. These are retained 18 October
forecast results, not current live recommendations.

`sampling-shared.patch` is an **unapplied** minimal import/averaging hook for
`src/engine/sampling.ts`. The measured browser results above use its unchanged
implementation. Applying the patch centralizes the existing math without
changing those results; the interface worker owns that source file.

The prepared Linux amd64 runner and refreshed instructions/notices remain in
`$SSD/fine-runner-b1-20261018/`. Its manifest records exact sizes and SHA-256
hashes. Nothing was uploaded or deployed.
