Written by AI (GPT-6.1 Sol), 2026-10-18.

# Compact default-taste named places

The exact-pixel climate calculation is unchanged. Every label and every search
place is included: 185,550 catalogue rows resolve to 170,513 distinct coordinates.
The 15,000 canonical map labels are all in the place list. There are 170,482
supported named coordinates; 31 lack full-replay support and use the existing
25 km model. No population cut-off is used.

The climate payload has 12 files, totalling **101,053,569 bytes**. Its 96 sample
scores per place are floored to one byte. This retains every integer band
threshold and reproduces the weighted typical-day score and nice, perfect and
awful day counts exactly. Eleven separately loaded fact shards retain 21
float32 monthly fields: six score factors, nine card weather values and six
full-period physical facts. The complete build took 112.08 seconds on this Mac
with four workers. The separate climate score table is 11,207,848 bytes;
the facts occupy 89,845,721 bytes. This is the full-precision float32 option.
A scratch measurement rounding facts to 0.001 saved only 12.72 MB; it was not used.

The current forecast run `20261017T0000Z` adds one `named.bin.gz` file of
**15,783,945 bytes**. Forecast weather remains the existing ECMWF/25 km model;
no ERA5 climatology correction is added to forecasts. The Go runner uses the
shared model's identical port, then the existing normalized 4 × 4 B-spline
blend at each named place. Fifteen slots preserve fourteen individual days and
the complete fourteen-day judgement. The separate total is necessary because
the existing typical-day calculation happens before the spatial blend. Scores
retain four decimal places in uint24; nice/perfect/awful shares use float32.

The daily forecast command writes this file before generating outlooks. Outlook
ranking reads the same compact table. A retained run without it still computes
the existing shared forecast calculation directly. The publisher uploads the
new file with the run's data before changing the forecast pointer. The updated
runner has five upload files totalling **35,416,693 bytes** in
`fine-runner-named-b1-20261018/runner/` on the SSD. Its binary is 35,405,986 bytes,
SHA-256 `d578023c66447b00ee586c010d176d2b96f3c477d510bb41f3758cdf68eb4401`.
The root `MANIFEST.json` records all five file hashes and compiled source inputs;
it is local handoff evidence, outside the `runner/` upload prefix.

## Verification

`named_data_proof.mjs` reads actual compressed tables through the production
shared loader and compares them with full replay / the existing Worker's
forecast calculation. The private validation table contains the thirteen
station comparison cities plus 384 seeded land points. Those random points
are not added to the public named-place catalogue.

All 26,996 climate score/count comparisons across twelve months, four seasons
and the year are exact. All 17,220 available forecast comparisons across every
forecast day and the full stretch have identical scores; count error is at most
0.000000413 day and every rounded count agrees. All 1,650 unavailable forecast
cases agree. The expanded forecast check found an ARM Go fused multiply-add
rounding difference at one native point; explicit float64 product boundaries
now preserve JavaScript's arithmetic before four-decimal score rounding.

All thirteen cities retain identical rounded card facts. Across the random
points, float32 fact error is at most 0.000003815 in the field's units. The report
enumerates 46 half-unit display ties, mostly a one-percentage-point difference
in rounded sunshine or wet-day percentages around an exact x.5% boundary.
These do not change a score, day count or corrected temperature model. Some
listed daylight-hour ties are internal fields rather than displayed card rows.

`go test ./outlook ./internal/dailyforecast ./internal/placenumbers ./cmd/niceweather`
passes. A retained-run outlook render using the new compact file produces
Monday 19 October's World, Europe and USA posters. Their winners remain
Chengdu/Lahore/Delhi, Rome/Milan/Rostov-on-Don and Los Angeles/Houston/Phoenix.

## Rebuild without publication

Set `NWF_DATA` to the external SSD data root. The builder writes a new output
folder and does not alter the full replay or existing `artifacts/v6`.

```sh
devenv shell -- node research/fine/build_named_places.mjs \
  --data "$NWF_DATA" \
  --pointer "$NWF_DATA/fine-validation/b1-worldwide/overlay/v6/fine/current.json" \
  --output "$NWF_DATA/v6-named-b1-20261018" --workers 4

devenv shell -- go run ./cmd/niceweather named-forecast \
  --source artifacts --places-source artifacts --v6-static artifacts/v6 \
  --out "$NWF_DATA/fine-named-forecast-b1-20261018"

devenv shell -- env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -trimpath -ldflags '-s -w' \
  -o "$NWF_DATA/fine-runner-named-b1-20261018/runner/niceweather-linux-amd64" \
  ./cmd/niceweather
```

`coordinates.json`, `BUILD.json`, `named-build.json`, private validation files,
and the worldwide replay are build evidence, not extra browser payloads.
Follow `UPLOAD.md` for the selected files, attribution, activation and ordering.
Nothing in these commands publishes data or changes the live pointer.
