Written by AI on 2026.10.18 by model GPT-6.1 Sol.

The worldwide loader reads the same exact temperature pixel for the app,
Worker place API and link preview. Its small native manifest declares
`format: "nwfp-v1"`, the correction generation, `eveningModel: "uniform"`,
the assembled ISO date axis and `timezoneNames`. The full packet checksum
list is a separate build artifact, not part of each browser's manifest.

The target-clock tile is `zones/RRR/CCC.nwz.gz`, with 256 × 256 little-endian
uint16 timezone indices. A value of 65535 is unsupported. Its 32-byte NWZT
header contains magic, version, row, column, tile width, generation and pixel
count. The lookup is an independently attributed timezone property.

The native-weather packet is `native/<fineIndex>/<zoneIndex>.nwfp.gz`.
Weather stays at the place's nearest own native ERA5 point. The target
clock comes from the containing fine pixel's timezone, including near
timezone borders; it is not silently taken from the native point.

The 32-byte NWFP header has these little-endian fields:

| Byte | Field |
|---:|---|
| 0 | Four-byte magic `NWFP` |
| 4 | uint8 version, 1 |
| 5 | uint8 flags, 1 |
| 6 | uint16 representative-day count |
| 8 | uint32 native fine index |
| 12 | uint16 target timezone index |
| 14 | uint16 full-period day count |
| 16 | 16-byte correction generation |

The body contains representative date indices as uint16, followed by
float32 response values:

- Each representative day: twelve air, dew, felt-sun and felt-shade values;
  24-hour high and low; rain, mean wind, sunny hours and daylight hours.
- Each assembled day: 24-hour high and low, daytime air maximum, air and dew
  at 21:00, and native shade felt temperature at 21:00.
- Each month: mean uncapped native dew, wet-day percentage, mean daily
  sunshine percentage and mean wind.
- Each month: the differences between native-clock and target-clock high
  and low normals. The loader adds these to the tile's two offsets before
  replay. Same-clock differences are zero.

Only the representative days need full hourly weather for score and count
calculations. The six-value daily response recovers full-period physical
facts for arbitrary high and low offsets. The ordered daytime transform is
monotone, including its uniform fallback, so its native daytime maximum is
sufficient. Uniform 21:00 correction still applies the native dew cap. The
packet occupies 64,448 bytes before compression for 96 representative days
and 1,800 assembled days.

`placeNumbers` also accepts the existing forecast compact-record inputs:
`{source: "forecast", records, offsetsC, weights}`. Each record is the
existing 15-byte weather day, each offset is its existing felt shift, and
weights sum to the period's calendar-day count. It preserves interpolation
between the existing 3 °C scoring levels. This is a shared judgement path;
it does not apply monthly ERA5 climate anchors to forecast weather. The
Worker uses this path after its existing forecast point and cell lookup.
The app's forecast GPU retains its existing implementation and resolution;
the exact-pixel typical-year app path imports this shared module directly.
