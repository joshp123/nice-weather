Written by AI (GPT-6.1 Sol), 2026-10-16.

This decoder supports the format observed in ECMWF IFS 0.25° open data: regular latitude/longitude grid template 0, instantaneous product template 0, accumulated rain product template 8, and unsigned preprocessed CCSDS representation template 42. The observed compression flags are 14, with 32-sample blocks and 128-block reference intervals. Other formats fail with an error.

The CCSDS code options and predictor follow [libaec](https://github.com/MathisRosenhauer/libaec). Its BSD notice is in `LICENSE.libaec` and must accompany binary distribution. The GRIB scaling fields follow [ECMWF template 5.42](https://codes.ecmwf.int/grib/format/grib2/templates/5/42/).

Verification performed on 2026-10-16 used the six fields `2t`, `2d`, `tcc`, `10u`, `10v`, `tp` from the ECMWF 2026-10-16 00 UTC run at steps 0, 3, 144, 150 and 360 hours. Messages were downloaded using the byte ranges in the corresponding public `.index` files, from:

```
https://storage.googleapis.com/ecmwf-open-data/20261016/00z/ifs/0p25/oper/20261016000000-<step>h-oper-fc.grib2
```

All 31,147,200 values across the 30 messages were exactly equal to ecCodes doubles, with zero tolerance and zero differences. This includes constant initial rain (zero bits), 8-bit cloud, 12-bit temperatures and wind, and 16-bit rain. The development command runs ecCodes independently and compares every sample:

```
go run ./cmd/compare-ifs-grib path/to/*.grib2
```

The previous ecCodes-repack/GDAL pipeline was also run for the six step-3 fields. All 6,229,440 float32 values matched the new decoder's float32 conversion in the same cell order, with zero differences. Scoring and point serialization are unchanged.
