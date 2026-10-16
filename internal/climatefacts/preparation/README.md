# Prepare the static climate grids

Written by AI on 2026.10.16 by model GPT-6.1 Sol.

These are development-time conversions. The cloud climate runtime reads one
compressed static file. It needs neither Python nor these source services.

`go run ./internal/climatefacts/preparation -out etopo-quarter-degree.bin`
downloads NOAA's anonymous ERDDAP ETOPO1 **Ice Surface** subset and converts it
to float32 little endian, 721 × 1440, latitude 90° to −90°, longitude 0° to
359.75°. It validates every input coordinate, height and record count.
`-csv downloaded.csv` replays an already downloaded source subset.

The source is a 1-arcminute global relief model in metres above mean sea level,
including the **top** of Antarctic and Greenland ice sheets. It also includes
negative ocean bathymetry. It is free to use and redistribute. Its metadata is
at <https://data.pmel.noaa.gov/socat/erddap/info/etopo180/index.html>.
The downloader requests every fifteenth source node, exactly matching the
climate grid. It does not spatially average or reinterpret ice as bedrock.

Then, in a development environment containing NumPy and tifffile:

```sh
python internal/climatefacts/prepare_static.py \
  --era5 era5_mirror_static.npz \
  --worldclim wc2.1_2.5m_elev.zip \
  --etopo etopo-quarter-degree.bin \
  --out static.bin.gz
```

WorldClim's nearest valid 2.5-arcminute land pixel in the surrounding 7 × 7
remains the primary ground height. ETOPO fills only missing WorldClim nodes.
Where both WorldClim is missing and ETOPO is negative, the target is treated as
sea level, rather than adjusting coastal temperature to the sea floor. Valid
WorldClim below-sea-level land is retained. There is no fallback to ERA5's
coarse source elevation.

The 2026-10-16 preparation found 669,115 missing WorldClim nodes in the full
global rectangle. Of those, 27,927 have ERA5 land fraction greater than zero;
that includes tiny nonzero ocean fractions. Against the application's actual
land mask, only **431 of 364,523 land cells** need the complement: 42 have
positive ETOPO surface height and 389 have negative ETOPO height. Those 389
are a remaining coastline or island representation limit and use sea level.
WorldClim already covers the Antarctic interior.

Observed checks: all 1,038,240 NOAA subset nodes are finite; the pole at 90°S
is 2,745 m. The downloaded CSV SHA-256 is
`df8f76783f7ade5f7afae0652ba368e712695cd95e53efe7b03ad51e41c73e10`;
the converted ETOPO grid SHA-256 is
`cf94ca6689dfd2ba4f06cffe164b3df7e96fcc7bb36703dcfabe9c9b28aad4df`.
The combined static file was 4,737,119 bytes compressed. Its adjacent JSON
records the source choices, missing-node counts and output checksum.
