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
ranking now calls the shared Go function on actual `days.bin` records and the
same static cells and monthly offsets used by the forecast runner. The score
PNG supplies the painted overview only. This adds no ERA5 temperature
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
