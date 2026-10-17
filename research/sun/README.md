<!-- Written by AI on 2026.10.17 by model GPT-6.1 Sol. -->

# Coastal sunshine investigation

[REPORT.md](REPORT.md) contains the measured result and proposed product change.
[LICENCES.md](LICENCES.md) records publication rights and primary evidence.
All generated data are on the SSD in
`/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/sun/`.
These scripts do not publish or edit the application or the other research trees.

Run from the repository through the existing devenv. The wrapper supplies
temporary Python dependencies through uv; it does not change project dependencies.

```sh
sh research/sun/run.sh era5_measurement.py
sh research/sun/run.sh observations.py --duckdb-bin /nix/store/6sxcfsi0m39bk89sal2nydxbbxvm5ny9-duckdb-1.5.5/bin/duckdb
sh research/sun/run.sh observations.py --aemet-only
sh research/sun/run.sh pvgis.py --years 2020 2022
sh research/sun/run.sh satellite.py --domain goes --year 2022 --place Lima --place Antofagasta --place 'San Diego' --place 'Lima ERA5 point'
sh research/sun/run.sh satellite.py --domain goes --year 2024 --place Lima --place Antofagasta --place Valparaiso --place 'San Francisco'
sh research/sun/run.sh satellite.py --summarize
sh research/sun/run.sh compare_sources.py
sh research/sun/run.sh prototype.py --satellite-monthly '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/sun/comparison/prototype-targets.json'
python3 research/sun/report_tables.py
```

The DuckDB path is the installed CLI used for this run. Omitting that option
uses NOAA's larger plain-text files. No installation is needed for that route.

`era5_measurement.py` snapshots the published baseline on its first run. Keep
the retained baseline when reproducing this report: a later live build can
contain other workers' temperature changes. It measures native radiation,
nearby coastal/ocean points, the one-degree records and the published facts.
`--browser-only` reproduces the API-versus-click pixel-selection audit.
`--archive-only` checks one anonymous Earthmover source point and additionally
needs icechunk, pcodec and zarr in the temporary uv environment.

`observations.py` preserves cloud element types, quality flags and coverage.
Cloud openings and AEMET sunshine totals are independent checks, not inputs
substituted for the product's DNI threshold.

`pvgis.py` uses satellite SARAH-3 explicitly, never PVGIS-ERA5.
`satellite.py` uses anonymous NSRDB S3 byte ranges, not the credentialed API.
Its `.npz` files preserve subhourly DNI; `--summarize` requires no redownload.
Both sources approximate hour means from instantaneous satellite samples.

`compare_sources.py` produces matched-date comparisons, co-located checks,
held-out-year errors and explicitly labelled prototype targets.
`prototype.py` replays the existing eight-day score calculation with only
the sunshine multiplier changed. Its offset images modify measured pixels
only; neutral unmeasured land is not a global correction.
`report_tables.py` renders the final CSVs as Markdown under
`sun/comparison/report-tables.md`.
