Written by AI (GPT-6.1 Sol), 2026-10-17.

# Poster assets

The runner embeds these files. It needs no browser, network font service or map
service to render an outlook.

## Geometry

Natural Earth **v5.1.2**, 1:50 million scale, from its
[official repository](https://github.com/nvkelso/natural-earth-vector/tree/f1890d9f152c896d250a77557a5751a93d494776/geojson):

- `land.bin`: `ne_50m_land.geojson`, closed land rings including surviving holes.
- `borders.bin`: `ne_50m_admin_0_boundary_lines_land.geojson`, national boundary lines.
- `states.bin`: `ne_50m_admin_1_states_provinces_lines.geojson`, only `ADM0_A3 = USA`.

Land includes Alaska and Hawaii. Natural Earth's borders include disputed
boundaries; the files contain geometry only, without place names or political
classification. They are [public domain](https://www.naturalearthdata.com/about/terms-of-use/).

Coordinates are WGS84 longitude and latitude, with no map projection. Paths are
simplified using Douglas-Peucker with a 0.025 degree tolerance, then rounded to
0.01 degree. Degenerate rings and duplicate adjacent points are removed. Path
order and ring winding are retained. Fill all land rings together with an
even-odd fill; stroke border and state paths as open polylines. Projection and
region cropping belong to the renderer.

Each binary file has this little-endian layout, with no padding or metadata:

```text
uint32 pathCount
repeat pathCount times:
    uint32 pointCount
    repeat pointCount times:
        int16 longitudeHundredths
        int16 latitudeHundredths
```

The three geometry files total 167,760 bytes.

## Font

`Barlow-Regular.ttf` and `Barlow-Bold.ttf` are unmodified Barlow fonts from
[Google Fonts](https://github.com/google/fonts/tree/47d54ca1ebb14fd864695f1d39dc734f39017c33/ofl/barlow),
pinned to that revision. The SIL Open Font License 1.1 is bundled as `OFL.txt`.

## Rebuild

`python3 outlook/assets/prepare.py` downloads the pinned sources, verifies their
SHA-256 hashes, then rebuilds all six assets using Python's standard library.
Use `--cache /tmp/nwf-outlook-assets` to retain downloads outside the repository.
The daily automation uses the committed assets and does not run this script.
