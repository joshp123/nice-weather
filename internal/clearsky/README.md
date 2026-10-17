# Clear-sky radiation and sunshine

Written by AI on 2026.10.16 by model GPT-6.1 Sol.

The daily v6 forecast uses NOAA solar geometry with four midpoint samples per
hour, the Ineichen clear-sky model, and monthly Linke turbidity. It estimates
direct normal irradiance from IFS accumulated global radiation (SSRD).
[Calibration and measured accuracy](calibration/README.md) explain the rule and
provide the archive-based reproduction script.

`PrepareForecastHour` computes solar geometry once per UTC hour and reuses it
for all points. `Model.ForecastRadiation` returns clear global horizontal and
direct normal irradiance; `EstimateForecastDNI` applies the calibrated
transmission curve. All irradiances are W/m². The forecast uses sea-level
altitude because its published points contain a temperature height correction,
rather than absolute height.

## Static source

`linke-quarter-degree.bin.gz` contains 721 × 1440 × 12 unsigned bytes, latitude
90° to −90°, longitude 0° to 359.75°, then January to December. Divide each byte
by 20. It is sampled, without interpolation, from pvlib 0.16.1's bundled
`LinkeTurbidities.h5`, using `pvlib.tools._degrees_to_index` at each target node.
That function rounds the nearest 5-arcminute pixel centre with ties to even and
clamps the poles and dateline. The compressed file is 3,631,273 bytes; decoding
uses 12,458,880 bytes. No runtime HDF5, Python or network lookup is needed.

Source SHA-256: `34c7e4c0bf865416ecbbfc32b0c3e9b56039a6aadff6997cc3c9ccae8c63e931`.
Generated gzip SHA-256: `cdfe93463bd93013defb69c3e886da7eea376d7013ca1006be1c45320038f04c`.

The Ineichen equations are adapted from pvlib 0.16.1. Its BSD license is
retained in `LICENSE.pvlib`. Source and model documentation:

- <https://github.com/pvlib/pvlib-python/tree/v0.16.1/pvlib>
- <https://pvlib-python.readthedocs.io/en/stable/reference/generated/pvlib.clearsky.ineichen.html>
- <https://pvlib-python.readthedocs.io/en/stable/reference/generated/pvlib.clearsky.lookup_linke_turbidity.html>
