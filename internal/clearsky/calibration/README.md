# Forecast DNI calibration

Written by AI on 2026.10.17 by model GPT-6.1 Sol.

The forecast has accumulated global solar radiation (SSRD), but no direct beam.
`../forecast.go` estimates hourly direct normal irradiance (DNI):

```
DNI_hour = clear_DNI_hour × clamp((GHI_interval / clear_GHI_interval − 0.05) / 0.95, 0, 1)^2.1
```

Both interval inputs are mean W/m² over the same UTC forecast interval. Integrate
the clear horizontal irradiance over that interval, including nighttime zeroes.
Use the target hour's clear normal irradiance; local 10:00 uses the hour ending
11:00. Clear radiation uses NOAA solar geometry, four midpoint samples, Ineichen
and the package's monthly Linke turbidity. Forecast altitude is zero because the
published points contain a temperature height correction, not absolute heights.

## Measurements

`results.json` retains every region and interval. Eight training archive tiles
and eight spatially separate held-out tiles each contribute a 3×3 point patch;
coordinates repeat where the selected centre meets an archive tile boundary.
Training: Málaga, Rotterdam, Singapore, Denver, Cape Town, Marrakech, London,
Sydney. Held out: San Diego, Chania, Bangkok, Lima, Tokyo, Oslo, Nairobi, Vancouver.

The archive is anonymous S3 bucket `earthmover-icechunk-era5`, prefix `icechunkV2`,
region `us-east-1`, immutable snapshot `TZ2KX6A24CJ5DG17T7MG`, group
`single/temporal`. Chunk 82 supplies 8,736 hours, 2021-09-20 through 2022-09-18.
Both SSRD and fdir are hourly accumulated J/m², divided by 3,600. Reference DNI
is fdir divided by the hour's integrated positive solar cosine. We aggregate
SSRD into UTC 3/6-hour intervals, then estimate each hour. Counts use DNI ≥ 300
W/m² during IANA local 10:00–21:00. Incomplete boundary days are excluded.

A grid search over floor 0–0.6 (0.05 steps) and power 0.5–2.5 (0.1 steps)
minimizes training daily sunny-count RMSE for 3-hour input. Held-out tiles do not
select the parameters. Metrics average the eight held-out regions:

| SSRD interval | Sunny-count MAE | RMSE | Bias | Hour classification | DNI MAE |
|---|---:|---:|---:|---:|---:|
| 1 h | 0.62 h/day | 0.98 h | −0.27 h | 92.5% | 61 W/m² |
| 3 h | 0.71 h/day | 1.08 h | −0.24 h | 90.6% | 76 W/m² |
| 6 h | 0.79 h/day | 1.20 h | −0.23 h | 89.6% | 84 W/m² |

This measures decomposition and time disaggregation on one ERA5 year, rather
than IFS forecast skill. Moving clouds within an interval cannot be recovered.
Unusual smoke/aerosols and mountain pressure are not separately validated.

## Reproduce

Use Python with numpy, icechunk, zarr and numcodecs[pcodec], and Go on PATH
(the project's `devenv` provides Go):

```sh
python internal/clearsky/calibration/calibrate_dni.py --fetch
python internal/clearsky/calibration/calibrate_dni.py
python internal/clearsky/calibration/calibrate_dni.py --verify-go --output internal/clearsky/calibration/parity.json
```

`--data-dir` changes the cache directory. The default is the external SSD's
`Code/nice-weather-data/forecast-calibration/`; NPZ inputs and large downloads
stay outside git. `--output` changes the compact JSON destination. Missing tiles
fail the calibration; `--fetch` resumes existing complete NPZ files. The archive
needs no account or credential. `--go /path/to/go` selects another Go binary.

`verify.go` calls the actual Go package for 7,776 retained place/hour samples,
including UTC midnight, winter/summer and local daylight/night. `parity.json`
records measured Python/Go differences. The maximum estimated-DNI difference is
0.243 W/m², mean 0.0049 W/m²; outside UTC midnight the maximum is 0.000082 W/m².
At midnight, Go correctly dates the extraterrestrial factor to the previous UTC
day while the original calibration uses the hour-ending day. That small known
difference is retained in the measurements. It is far below the measured
76–84 W/m² estimation error.
