Written by AI on 2026.10.18 by model GPT-6.1 Sol.

The worldwide exact-place carrier keeps the native weather separate from the
30 arc-second temperature correction. Its output is `replay/` in the new
`v6-fine-b1-20261018` SSD release folder. It does not change the overview map,
deploy the site, or modify the retained raw files.

The supported land contains 320,420,484 fine pixels. They use 376,591 own native
ERA5 quarter-degree points. Sampling those points in each fine pixel's local
clock requires 397,686 point/clock packets. The independent timezone lookup has
6,633 compressed raster tiles, totalling 3,749,341 bytes. Zone preparation took
128.22 seconds. The packet manifest is marked complete only after every required
packet exists and has been hashed.

Each packet contains 96 representative days with twelve hourly air, dew, sun
and shade temperatures; daily high/low, rain, wind, sunny and daylight hours;
six response values for every assembled day; twelve monthly native weather
facts; and monthly high/low reference differences between the native and target
clocks. No station temperatures are included. Daytime correction remains the
frozen ordered model; the shared consumer applies one uniform correction at
21:00 everywhere. High/low reference differences keep the original anchor
unchanged where the target clock crosses a timezone boundary.

The binary contract and its JavaScript decoder are documented in
`hosting/sites/FINE_REPLAY.md`. Packet keys are
`native/<fineIndex>/<timezoneIndex>.nwfp.gz`; timezone keys are
`zones/<row:03d>/<column:03d>.nwz.gz`. The small served manifest contains the
shared 1,800-day date axis and timezone names. `packet-index.bin` is a sorted
little-endian uint64 array of `(fineIndex << 16) | timezoneIndex`.
`BUILD-INVENTORY.json.gz` and `files.sha256` contain the complete file audit;
they are separate from the served manifest.

To rebuild, use the existing project development environment through `run.sh`.
The input inventory is produced by `worldwide_inventory.py`; it identifies the
five missing ERA5 fields without changing existing temperature files.

```sh
SSD='/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data'
sh research/fine/run.sh worldwide_weather.py \
  --inventory "$SSD/fine-validation/b1/worldwide-inventory.json" \
  --output "$SSD/fine-sources/b1-worldwide-weather" \
  --workers 128

sh research/fine/run.sh worldwide_replay.py prepare \
  --tiles "$SSD/fine-30s" \
  --reference-root "$SSD/fine-build/reference-global" \
  --native-root "$SSD/fine-build/native" \
  --land-support "$SSD/fine-build/land-support-v2" \
  --weather-cache "$SSD/fine-sources/b1-worldwide-weather" \
  --work "$SSD/fine-build/b1-worldwide" \
  --output "$SSD/v6-fine-b1-20261018/replay"

sh research/fine/run.sh worldwide_replay.py build \
  --tiles "$SSD/fine-30s" \
  --reference-root "$SSD/fine-build/reference-global" \
  --native-root "$SSD/fine-build/native" \
  --land-support "$SSD/fine-build/land-support-v2" \
  --weather-cache "$SSD/fine-sources/b1-worldwide-weather" \
  --work "$SSD/fine-build/b1-worldwide" \
  --output "$SSD/v6-fine-b1-20261018/replay" \
  --workers 4
```

Acquisition and replay may run together. Replay waits for missing weather to be
atomically saved, then processes each spatial tile across all assembled chunks.
Both retain completed files on rerun. Source and generation settings must match
the existing work directory. A new chunk set needs a new correction generation
and work/output directories; the date axis, monthly references and representative
days then recompute from those chunks.

`worldwide_oracle.py` independently builds private full-hourly fixtures through
the original point-weather code for the thirteen cities and 384 additional
queries. These verify the actual compact files through the shared JavaScript
and Go consumers. The fixtures remain under `fine-validation`; they are not
production data or upload inputs.

The carrier's `ATTRIBUTION.txt` describes modified ERA5/C3S weather under CC BY
4.0. `timezones-licence/` contains the independently published ODbL timezone
lookup's attribution, licence, installed data hashes and primary-source evidence.
The correction tiles have separate source and CC BY-SA notices. Copy the whole
release, including those notices, as described by the release upload inventory.
