# Clear-sky radiation and sunshine

Written by AI on 2026.10.16 by model GPT-6.1 Sol.

This package reproduces the measured physical-ratio sunshine method in
`/tmp/nwf-agents/round3/data2/sun/ratio.py` and `refine_daylight.py`. It makes no
weather judgement.

`PrepareHour` calculates NREL SPA solar ephemerides at four midpoint samples in
the preceding hour. Reuse the result for every cell. `Model.Hour` combines them
with the place's ground height and monthly Linke turbidity through the Ineichen
clear-sky model. `SunshineHours` applies the measured 0.1 transmission floor and
120 W/m² clear normal-beam eligibility threshold. The daytime measure is summed
sunshine hours divided by summed geometric sun-up hours, using local 10–20.

SPA uses pvlib's defaults: delta-T 67 seconds, air temperature 12 °C, atmospheric
refraction 0.5667°, pressure calculated from altitude. Monthly Linke values are
interpolated at UTC calendar-day resolution between month middles. Leap days
are retained. The horizon overlap follows the research's NOAA approximation,
not SPA's apparent horizon. Irradiances are W/m²; the incoming direct radiation
must represent the preceding hour's average, rather than its accumulated J/m².

`New` looks up the 0.25° grid's monthly climatology. For off-grid research places,
`NewWithMonthlyTurbidity` takes the native 5-arcminute values so comparison does
not conflate coordinate sampling with the solar calculation.

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

The solar coefficients and equations are adapted from pvlib 0.16.1. Its BSD
license is retained in `LICENSE.pvlib`. Source and model documentation:

- <https://github.com/pvlib/pvlib-python/tree/v0.16.1/pvlib>
- <https://pvlib-python.readthedocs.io/en/stable/reference/generated/pvlib.clearsky.ineichen.html>
- <https://pvlib-python.readthedocs.io/en/stable/reference/generated/pvlib.clearsky.lookup_linke_turbidity.html>

## Verification

`go test ./internal/clearsky` checks 1,440 retained research hours across all 12
places: New Year, the leap-year solstice and both European DST transitions.
These cover radiation, daylight overlap and fractional sunshine separately.

The optional complete-reference test reads a converted copy of the retained
research arrays. Set `NWF_CLEARSKY_REFERENCE` to their common path prefix. The
`.json` file supplies coordinates, ground altitude, monthly Linke values,
counts and time zones. The `.bin` file concatenates each place's hourly records,
each containing an int64 Unix timestamp followed by float64 direct radiation,
clear horizontal radiation, clear normal radiation, sun-up hours and sunshine
hours, all little endian. `.daily.csv` is the unchanged research `daily_sun.csv`.
This tests every retained hour and every complete local daytime against the
actual daily CSV, rather than a recomputed expected daily result.
