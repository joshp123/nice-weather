# Independent sunshine inputs for named places

Written by AI on 2026.10.18 by model GPT-6.1 Sol.

These scripts acquire satellite radiation, before the scoring model. They do
not modify ERA5, unnamed map points, temperature anchors, or forecast records.
Raw source responses and intermediate arrays live on the external SSD under
`sun/named`, outside git. Source acquisition is anonymous and needs no account.

## Sources and permission

| Source | Period and coverage | Publishing derived values |
| --- | --- | --- |
| PVGIS 5.3 / CM SAF SARAH-3 | 2005–2023; Europe, Africa and the eastern part of South America, checked at each requested coordinate | The [PVGIS usage conditions](https://joint-research-centre.ec.europa.eu/photovoltaic-geographical-information-system-pvgis/general-information/usage-conditions-data-protection_en) say the information is free with no restrictions on use. Credit the European Commission Joint Research Centre, PVGIS, and CM SAF SARAH-3. |
| NSRDB GOES PSM v4 | Americas; typical years from 1998–2024 north of 20°S, 2022 and 2024 full-disc observations farther south | The [producer's public-data registry](https://registry.opendata.aws/nrel-pds-nsrdb/) grants CC BY 3.0 US. Credit the National Solar Radiation Database and its producer; include the licence link and describe the derived monthly correction. |

The scripts require explicit independent satellite metadata. ERA5 returned by a
PVGIS route is not an eligible substitute. Exact coordinates with SARAH-3
coverage take that longer normal before either GOES source. Every other
covered point uses GOES; the final coverage report must identify unsupported
points. Neither sparse probes nor interpolation between distant towns supply
the global correction.

## SARAH-3 method

`named_profiles.py` requests DRcalc with `month=0`, `glob_2axis=1`,
`raddatabase=PVGIS-SARAH3`, and no terrain horizon. Each roughly 21 KB response
contains all 288 monthly/hourly mean direct-normal irradiances for 2005–2023.
Its metadata states the actual radiation database and period.

`named_satellite.py` requests a typical meteorological year (TMY) at the same
coordinate. TMY selects one observed year per month, rather than supplying
the full-period distribution. Raw TMY was rejected after it worsened Seville
against the independent nineteen-year series. The common method instead
scales each TMY month/hour's DNI by the nineteen-year DRcalc mean divided by
that TMY hour's mean, then recomputes the 300 W/m² sunshine threshold.
This retains the selected month's cloudy/sunny day variation while anchoring
its direct energy to the long normal. The six long-series controls passed
with the same rule, including Seville; this is still a distribution estimate,
not nineteen years of hourly observations.

Radiation timestamps are shifted by the source's documented
`irradiance_time_offset`. Minute-midpoint interpolation supplies each civil
clock hour from 10:00 through 21:00. IANA daylight-saving offsets use the
selected source year; long-period profiles average the actual 2005–2023
offsets. Each daily sunshine share uses Nice Weather's daylight hours after
10:00 and the same integer sunset rule, before monthly averaging.
The IANA zone comes from the shared place function's exact 1 km zone tile,
exported to `sun/named/target-timezones.json`. A changed zone invalidates only
the monthly reduction, preserving its raw CSV and radiation profiles. Existing
unsupported model points stay unsupported; their satellite eligibility is
counted separately from whether the app can serve their place model.

For the small initial benchmark and validation, the script retains the JSON
prefix's database and selected-year metadata and downloads hourly values as
CSV. With `--verified-csv`, an exact-coordinate explicit SARAH-3 DRcalc result
establishes TMY's source: the [primary PVGIS manual, section 3](https://joint-research-centre.ec.europa.eu/photovoltaic-geographical-information-system-pvgis/using-pvgis-5/pvgis-5-user-manual_en)
states that SARAH-3 is the default inside its coverage and that TMY uses the
same default databases. CSV retains the observation time offset and all
selected years. Provenance records this inference, its source URL, the DRcalc
hash, and the CSV hash. It does not falsely claim CSV contains database metadata.

## GOES acquisition

`named_goes.py plan` downloads metadata and counts the exact native 500-site
HDF5 groups required by named coordinates. `extract` requires an explicit
`--max-groups` budget. Native sites are selected by spherical nearest distance,
with support distance reported by eligibility; groups share each physical DNI
chunk. The northern native grid extends to 20.99 degrees south, so Iquique uses
the same long-period TMY as northern places. Exclude exact SARAH-supported
points from northern GOES, then exclude both SARAH and eligible northern
points from the southern full-disc plan. Raw ranges are fetched concurrently and completed point arrays are
checkpointed for a rerun. A truncated HTTP range is retried before any array
is accepted. `index` visits the HDF5 chunk directory once and retains exact
physical byte addresses, avoiding thousands of repeated remote B-tree
lookups during extraction. It reads metadata only. `extract` reuses
`<source>-dni-chunks.json` when present. Full HDF5 files, hundreds of gigabytes each, are never downloaded.

TDY is the producer's typical direct-radiation year, selected primarily on DNI.
Both candidates were tested at the same three independent two-year GOES
reference points. TMY improved sunshine and direct-radiation errors at all
three; TDY worsened San Diego direct radiation against native ERA5 and had
larger errors at both Californian points. Use TMY everywhere in northern GOES
coverage; there is no per-place source choice. TMY and TDY
hourly values are instantaneous midpoint samples, not hourly means. Actual
selected years are in the compact `tmy_year_short` dataset. Global extraction
must use the validated civil-hour integration, source-year offsets and final
source choice recorded in the delivery report; a filename containing TMY is
not evidence of a monthly radiation normal.

## Rerun

Run through the repository environment, for example:

```sh
SSD='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
sh research/sun/run.sh named_profiles.py \
  --coordinates "$SSD/v6-named-b1-20261018/coordinates.json" \
  --output "$SSD/sun/named/sarah-profiles.ndjson" \
  --workers 32 --requests-per-second 20

sh research/sun/run.sh named_satellite.py \
  --coordinates "$SSD/v6-named-b1-20261018/coordinates.json" \
  --profiles "$SSD/sun/named/sarah-profiles.ndjson" \
  --output "$SSD/sun/named/sarah-normalized.json" \
  --normalize --verified-csv --watch-profiles --workers 96 \
  --requests-per-second 8 --finished-requests-per-second 25

sh research/sun/run.sh named_goes.py plan --source tmy \
  --coordinates "$SSD/v6-named-b1-20261018/coordinates.json" \
  --exclude-profiles "$SSD/sun/named/sarah-profiles.ndjson" \
  --output "$SSD/sun/named/goes/tmy-global.json"

sh research/sun/run.sh named_goes.py index --source tmy \
  --coordinates "$SSD/v6-named-b1-20261018/coordinates.json" \
  --output "$SSD/sun/named/goes/tmy-dni-chunks.json"

sh research/sun/run.sh named_goes.py extract --source tmy --metadata \
  --coordinates "$SSD/v6-named-b1-20261018/coordinates.json" \
  --exclude-profiles "$SSD/sun/named/sarah-profiles.ndjson" \
  --output "$SSD/sun/named/goes/tmy-global.json" \
  --max-groups <the-approved-plan-group-count> --workers 16

sh research/sun/run.sh named_goes.py plan --source disc2022 \
  --coordinates "$SSD/v6-named-b1-20261018/coordinates.json" \
  --exclude-profiles "$SSD/sun/named/sarah-profiles.ndjson" \
  --exclude-locations "$SSD/sun/named/goes/tmy-global.locations.json" \
  --output "$SSD/sun/named/goes/disc2022-global.json"
```

PVGIS permits 30 requests/s per IP. The simultaneous example jobs total 28;
include every other API job when assigning that budget. The TMY process follows
new complete profile records continuously, and raises its rate only after the
profile progress file confirms completion. Cached responses and targets are
reused. Without `--watch-profiles`, profiles are a fixed snapshot; rerun against
the completed file to include later records.
Progress estimates use uncached acquisitions, since replaying thousands of
cached records must not imply a faster network rate. During the simultaneous
run, TMY has 8 requests/s; after profiles finish it has 25. These are request
limits, not promises of measured throughput.

`named_targets.py` combines completed inputs in coordinate order, with SARAH
first, the approved northern GOES source second, and source-day-weighted
2022/2024 GOES farther south. Worldwide mode requires completed SARAH progress
and both native-pixel plans; incomplete acquired sets fail. `--validation`
permits the fourteen-city proof and labels the result as a subset. It writes
source counts, periods, input hashes and `source-support.tsv.gz`, which lists
the exact source code at every named coordinate. Uncovered points retain the
native model. Two southern years are a short recent estimate, not a climate
normal; this limitation must accompany the published attribution.

For a cached GOES point, `named_goes.py summarize` reruns the civil-hour
integration without any network request. Source arrays, timestamps and native
coordinates are retained; typical-year summaries also use the cached selected
source-year array. This separates an arithmetic rerun from radiation download.

`named_budget_watch.py` can enforce a temporary acquisition budget while a
larger transfer awaits approval. Supply only the acquisition process IDs and
their progress files; it records its baseline and stops those processes after
the byte allowance or wall time. Leave room for responses already in flight.
It uses the standard library and needs no environment package installation.
A watcher without progress files provides a wall-time bound for metadata-only
preparation. Stopped producers and followers must be restarted together because
the producer rewrites its NDJSON stream from cached responses.

JSON/NDJSON here is an offline acquisition interface, not a browser payload.
Target records contain exact requested coordinates, twelve monthly sunshine
shares and twelve hourly DNI means per month, source/period, selected source
coordinates, IANA zone, selected years when provided, and provenance hashes.
The production builder fits these targets against the shared native replay.
See `research/fine/UPLOAD.md` for the separate packed delivery and deployment.
