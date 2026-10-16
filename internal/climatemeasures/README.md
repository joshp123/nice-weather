# Climate day measures

Written by AI on 2026.10.16 by model GPT-6.1 Sol.

`niceweather climate-measures` builds one 3° tile and local calendar year. It
reads hourly ERA5 facts directly from the free Earthmover public archive. No
score, grade, preference curve or presentation file is produced.

```sh
niceweather climate-measures \
  --static era5-static.bin.gz \
  --source-index earthmover \
  --tile-row 17 --tile-column 118 --year 2020 \
  --out ./artifacts
```

The north-west sample of tile 17/118 is 39°N, 6°W. Tiles are twelve by twelve
0.25° ERA5 samples; the last polar row is shorter. Land coverage is the same
embedded Natural Earth all-touched mask as the forecast. ERA5 mixed coastal
cells and islands are retained.

The output is `climate-measures/r17-c118/2020.bin.gz` plus a report. Records are
ordered by cell, then date. The cell ID is the existing map index: north to
south, −180° to 179.75°. A later judgement or summary can stream the gzip file
through `internal/daymeasures.NewReader`. That shared package owns the record
schema and precision.

Completed tile-years have a report checkpoint. Repeating a completed batch
checks its file checksum and input identity, then returns its report without
downloading weather again. Incomplete files end in
`.partial` and the next invocation rebuilds that batch. Use a separate output
root when changing source snapshots, static terrain, or the measure schema.
The full run is a list of independent tile-year invocations; no global cube is
held in memory. Adjacent tiles can reread source chunks at their boundaries.

`--publish <Site> --token-stdin` writes these two files below the Site's
`climate-measures/` storage prefix. It never changes an app deployment or a live
climate or forecast pointer. The existing publisher bearer is read from stdin,
never a command-line argument. A forecast-only upload grant cannot write this
prefix. The measured probe can use
`--publish-prefix forecast/climate-measures-probe` with the existing short-lived
forecast upload grant. This writes a scratch prefix, never the current forecast
pointer.

The builder reads a 48-hour UTC pad on each side of the year. Every output date
is a local civil date, using the forecast's IANA timezone polygons and embedded
timezone database. Temperature is sampled at local 10:00 through 19:00. States
in fractional-hour zones are linearly interpolated. Rain and sunlight use
preceding-hour ERA5 intervals and their exact overlap with local 10:00–20:00 or
midnight–midnight. Wind is the mean of hourly vector magnitudes, in km/h.
Midnight clock changes produce real 23- or 25-hour whole days. A civil date
that never occurred, such as Samoa's 30 December 2011, has no record; the
report counts these skipped dates.

Only temperature changes source cell. The nearest cell with at least 80% land
within the surrounding 3×3 is selected; without one, the cell with the most
land wins, then distance. Temperature is corrected by
`0.0065 × (source terrain metres − target ground metres)` degrees Celsius.
Rain, dew point, wind and direct radiation remain at the target native cell.
`internal/clearsky` owns the measured physical sunshine calculation.

The static file is gzip containing `NWFSTAT1`, then three 721×1440 float32
little-endian arrays: land fraction, ERA5 source height, target ground height.
All three use ERA5 order: 90°N southward; longitude 0° eastward to 359.75°.

For independent source verification, `--places <file>` accepts objects with
`Latitude`, `Longitude`, `ElevationM`, `Timezone`, optional `MonthlyTurbidity`
(twelve Linke values), and optional `SourceLatitude`/`SourceLongitude` for a
source archive's exact native sampling coordinates. IDs then follow the place
list order. Use a separate output directory for these off-grid checks.

The report records source chunk and catalogue bytes, compressed measure bytes,
wall time, observed Go heap, and the Linux kernel's process peak resident
memory. The cloud automation must also measure its real elapsed runtime and
publish the report before a global run is considered.

The optional research comparison uses the same hourly-fact conversion, physical
solar model, day aggregator and binary writer/reader as the builder. Prepare
the retained inputs with `prepare_reference.py`, then run:

```sh
NWF_REFERENCE=/path/to/prepared/references \
NWF_STATIC=/path/to/era5-static.bin.gz \
NWF_REFERENCE_REPORT=/path/to/comparison.json \
go test -count=1 -v ./internal/climatemeasures
```

The research retained eleven state samples (10:00 through 20:00); this builder
follows the requested ten (10:00 through 19:00). The comparison checks the first
ten research temperatures, recomputes ten-sample means from the original
hourly dew point and wind, and reports the difference from the eleven-sample
baseline. It does not silently treat those two definitions as equivalent.
Stored precision allows at most 0.0051°C, 0.0501 mm, 0.000051 sun share, and
0.0051 km/h difference from those inputs. Actual source archive decoding is
checked separately; passing retained-input comparisons is not a claim that
every historical source chunk has been downloaded again.
