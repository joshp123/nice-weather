# Shared physical day measures

Written by AI on 2026.10.16 by model GPT-6.1 Sol.

`HourlyFacts` is the source boundary. Temperatures and dew point are °C, rain
is mm and wind is km/h. `Time` labels instantaneous state. Rain, sunshine time
and sun-up time cover the **preceding** UTC hour. `IntervalMissing` allows the
forecast's first state without pretending to know its preceding precipitation.

`Aggregate` samples temperature, dew point and wind at the ten local civil
hours 10:00 through 19:00. A fractional UTC offset uses linear interpolation of
the surrounding hourly states. It integrates precipitation and sun over
10:00–20:00; whole-day precipitation covers local midnight to the following
local midnight, including 23-hour and 25-hour daylight-saving days. Rain and
sun intervals crossing window edges are apportioned by elapsed overlap.

Sun share is summed estimated sunshine hours divided by summed sun-up hours.
No sunshine or no sun-up coverage is different from a measured zero:
`SunAvailable` says whether the ten-hour window has complete sun data and a
nonzero denominator. `WholeDayRainHours` exposes incomplete whole-day totals.
Consumers must require full local-day rain coverage before counting wet days.

No dew-point clipping, lapse correction, radiation estimator or judgement
lives here. Sources supply those physical input decisions.

## Stream format

The stream begins with eight bytes: ASCII `NWDM`, little-endian u16 version
`1`, little-endian u16 record size `48`. Every subsequent record is below.
Gzip compression belongs to the caller so records can be read one at a time.
An unsupported header or truncated record is an error.

| Offset | Type | Meaning |
|---:|---|---|
| 0 | u32 | Global cell index: north-to-south 721 rows, 1440 columns; longitude −180°, latitude +90°, spacing 0.25° |
| 4 | i32 | Local calendar date, represented as UTC days since 1970-01-01 |
| 8–27 | ten i16 | Local 10:00–19:00 temperatures, hundredths °C |
| 28 | i16 | Daytime mean dew point, hundredths °C |
| 30 | u16 | Daytime rain, tenths mm |
| 32 | u16 | Whole-day rain, tenths mm |
| 34 | u16 | Sun share, ten-thousandths |
| 36 | u16 | Daytime mean wind, hundredths km/h |
| 38 | u16 | Daytime maximum gust, hundredths km/h |
| 40 | u16 | Daytime interval coverage, hundredths hour |
| 42 | u16 | Whole-day rain interval coverage, hundredths hour |
| 44 | u16 | Daytime sun-up time, hundredths hour |
| 46 | u16 | Flags: bit 0 sun available; bit 1 gust available |

All multibyte fields use little-endian order. Rounding error is at most
0.005 °C, 0.05 mm, 0.00005 sun share, 0.005 km/h and 0.005 hour. Writing an
unrepresentable or nonfinite value fails rather than saturating it.
Rain's representable range is 0–6553.5 mm per day.

The format is a fixed binary physical-data contract, with no variable-length
objects or scores. The writer and reader share this one Go schema. A later
judge can stream it independently of the archive and presentation.

## Forecast input availability

`dailyforecast.MeasuresForDay` accepts ECMWF point fields in native units
(`2t`, `2d`, `10u`, `10v`, cumulative `tp`). It linearly interpolates state,
distributes accumulated rain across the 3-hour or 6-hour source interval and
feeds this same aggregator. It is opt-in. The current forecast job is unchanged.

The real ECMWF 2026-10-16 00 UTC, +3 hour
[open-data index](https://storage.googleapis.com/ecmwf-open-data/20261016/00z/ifs/0p25/oper/20261016000000-3h-oper-fc.index)
was retrieved on 2026-10-16 (40,029 bytes). `ssrd`, `tcc` and `10fg` exist;
`fdir`, `cdir`, `dsrp`, `ssrdc` and sunshine duration do not. ECMWF's
[open-data parameter table](https://www.ecmwf.int/en/forecasts/datasets/open-data)
agrees that direct and clear-sky direct radiation are outside the open subset.

`ssrd` includes **direct plus diffuse** downward radiation, as defined in the
[ECMWF parameter database](https://codes.ecmwf.int/grib/param-db/169).
It cannot supply the climate direct-beam ratio without a separate radiation
decomposition approximation. No such approximation is introduced here.
Forecast sun therefore remains explicitly unavailable in the shared measure.
Cloud amount remains an available source field, but is not relabelled sunshine.

The same run's actual `10fg` GRIB message was downloaded by its indexed byte
range. ecCodes reported `shortName=10fg`, `stepType=max`, `stepRange=2-3`,
`startStep=2`, `endStep=3`. It describes just the last hour of a three-hour
interval. A full daytime maximum gust cannot be recovered from those sparse
maxima, so forecast gust remains unavailable too.

The research comparison scripts used eleven state samples (10:00 **through
20:00 inclusive**) for their old dew/wind means and candidate high. This
contract follows the requested **ten** samples. Preceding-hour rain and sun
still integrate the same 10:00–20:00 window. Compare the retained hourly
research arrays with these ten samples; do not claim old eleven-sample means
are identical.
