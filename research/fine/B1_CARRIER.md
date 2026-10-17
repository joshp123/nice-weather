Written by AI on 2026.10.18 by model GPT-6.1 Sol.

The worldwide exact-place carrier keeps the native weather separate from the
30 arc-second temperature correction. Its output is `replay/` in the new
`v6-fine-b1-20261018` SSD release folder. It does not change the overview map,
deploy the site, or modify the retained raw files.

The supported land contains 320,420,484 fine pixels. They use 376,591 own native
ERA5 quarter-degree points. Sampling those points in each fine pixel's local
clock requires 397,686 point/clock packets. The independent timezone lookup has
6,633 compressed raster tiles, totalling 3,749,341 bytes. Zone preparation took
128.22 seconds. All required packets exist and have been hashed; the replay
manifest is complete.

The complete replay build, including its final checksums, took 7,194.80 seconds
(119.91 minutes), while waiting for acquisition in parallel. Its 397,686
compressed packets occupy 15,752,993,536 bytes. The full replay component,
including zones, indexes, audit inventories and notices, occupies
15,815,945,109 bytes in 404,342 regular files.

The final full audit passed in 75.26 seconds. It decompressed and checked every
packet, verified all timezone tiles, and checked that every supported fine
pixel resolves to the required native-point/local-clock packet. There were no
missing packets or checksum failures. The final resumed acquisition completed
4,616 additional files (26,954,112,818 bytes) in 5,159.78 seconds, with no
failures. This duration excludes the useful downloads retained from earlier
runs; it is not a from-zero acquisition time.

The release inventory before sealing is below. It includes the final root
attribution. All files are regular files; there are no symlinks, temporary
files or partial outputs. The carrier is complete; application release approval
and the final release seal remain separate.

| Component | Files | Bytes |
| --- | ---: | ---: |
| `replay/` | 404,342 | 15,815,945,109 |
| `temperature/` | 79,657 | 2,727,800,868 |
| `labels/` | 4 | 882,594 |
| `ATTRIBUTION.txt` | 1 | 4,889 |
| Total before sealing | 484,004 | 18,544,633,460 |

The private audit records are
`fine-validation/b1-worldwide/replay-audit.json`,
`weather-complete-inventory.json` and `regular-file-inventory.json` on the SSD.
The audited replay manifest SHA-256 is
`6b3cb1fd84e84692771345fecd9612da27c2b4495046bb1e22bcde578be6962a`.

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
  --tiles "$SSD/v6-fine-b1-20261018/temperature" \
  --reference-root "$SSD/fine-build/reference-global" \
  --native-root "$SSD/fine-build/native" \
  --land-support "$SSD/fine-build/land-support-v2" \
  --weather-cache "$SSD/fine-sources/b1-worldwide-weather" \
  --work "$SSD/fine-build/b1-worldwide" \
  --output "$SSD/v6-fine-b1-20261018/replay"

sh research/fine/run.sh worldwide_replay.py build \
  --tiles "$SSD/v6-fine-b1-20261018/temperature" \
  --reference-root "$SSD/fine-build/reference-global" \
  --native-root "$SSD/fine-build/native" \
  --land-support "$SSD/fine-build/land-support-v2" \
  --weather-cache "$SSD/fine-sources/b1-worldwide-weather" \
  --work "$SSD/fine-build/b1-worldwide" \
  --output "$SSD/v6-fine-b1-20261018/replay" \
  --workers 4

sh research/fine/run.sh audit_replay.py \
  --replay "$SSD/v6-fine-b1-20261018/replay" \
  --land-support "$SSD/fine-build/land-support-v2" \
  --output "$SSD/fine-validation/b1-worldwide/replay-audit.json" \
  --workers 8
```

Acquisition and replay may run together. Replay waits for missing weather to be
atomically saved, then processes each spatial tile across all assembled chunks.
Both retain completed files on rerun. Source and generation settings must match
the existing work directory. A new chunk set needs a new correction generation
and work/output directories; the date axis, monthly references and representative
days then recompute from those chunks.

Retain these inputs on the SSD for a rebuild without downloading again:

- `fine-validation/b1/worldwide-inventory.json`: the acquisition's frozen input
  inventory. Keep its original bytes; a new inspection includes a new timestamp
  and must be written to another path.
- `fine-build/reference-global/era5/chunk{76,78,80,82,84}`: original temperature
  and any existing complete six-field raw tiles, untouched by this build.
- `fine-sources/b1-worldwide-weather/chunk{76,78,80,82,84}`: the five supplemental
  fields for 1,163 tiles per chunk; 5,815 files and 35,781,604,823 bytes in total.
  The source is pinned to Earthmover snapshot `TZ2KX6A24CJ5DG17T7MG`.
- `fine-build/reference-global/v6/points.bin` and
  `fine-build/reference-global/climate-v6-work/timezones.npy`: native-point and
  native-clock identities.
- `fine-build/native/v6/cells.bin` and its `climate-v6-work/chunk*` intermediates:
  geometric-cell representative-day selection from the rebuilt native weather.
- `fine-build/land-support-v2/land.u8` and `complete.json`: exact fine land support.
- `v6-fine-b1-20261018/temperature/configuration.json`: the frozen generation and
  actual assembled chunk set.

`fine-build/b1-worldwide` retains the prepared point/clock pairs and progress.
The scripts can regenerate that preparation; the installed timezone package
versions and data hashes are recorded in configuration and attribution.

`worldwide_oracle.py` independently builds private full-hourly fixtures through
the original point-weather code for the thirteen cities and 384 additional
queries. These verify the actual compact files through the shared JavaScript
and Go consumers. The fixtures remain under `fine-validation`; they are not
production data or upload inputs.

The independent oracle contains all 397 queries. The actual worldwide packets,
timezone lookup and correction tiles passed the shared JavaScript and Go paths
with identical default scores and day counts. This includes newly downloaded
weather, timezone boundaries and Antarctic pixels. The thirteen accepted city
temperature corrections were retained unchanged.

A bounded network comparison retained 100 useful new files (824,578,257 bytes)
from direct anonymous S3 in 316.45 seconds, with no failed downloads. The first
100 files through the inherited network environment were 669,554,378 bytes in
208.28 seconds. Different latitudes and a bounded final batch prevent treating
those as a sustained throughput comparison; direct access did not demonstrate
a speed improvement. Full acquisition resumed through the prior environment.

The carrier's `ATTRIBUTION.txt` describes modified ERA5/C3S weather under CC BY
4.0. `timezones-licence/` contains the independently published ODbL timezone
lookup's attribution, licence, installed data hashes and primary-source evidence.
The correction tiles have separate source and CC BY-SA notices. Copy the whole
release, including those notices, as described by the release upload inventory.
