# ERA5 climate facts

Written by AI on 2026.10.16 by model GPT-6.1 Sol.

The runtime reads the anonymous Earthmover ERA5 archive, pinned to snapshot
`TZ2KX6A24CJ5DG17T7MG`. Its six hourly fields are `t2m`, `d2m`, `tp`, `fdir`,
`u10` and `v10`, on the native 0.25° grid. Temperature and dew point are kelvin;
precipitation is metres; direct horizontal solar energy is J/m²; winds are m/s.
Rain and radiation accumulate over the preceding hour, ending at `valid_time`.
Source metadata states these units and interval semantics.

`catalogue.go` reads the pinned snapshot and relevant manifests directly from
public S3. Icechunk metadata is Zstandard-compressed FlatBuffers. It locates
native chunk object IDs and byte ranges without a Python service or a generated
global index. The reader checks the native float32 / 8736×12×12 / Pcodec contract.

`codec.go` runs the embedded reference Pcodec decoder in Wazero. The executable
needs neither Python, Rust, WebAssembly tools, ecCodes nor GDAL at runtime.
The small decoder's reproducible build, pinned dependencies and licences are
in `pcodec-wasm/`. `ReadBytes` counts actual snapshot, manifest and weather payload
bytes received over HTTP. HTTP headers and TLS overhead are excluded.

The optional live check reads a real 1,257,984-float temperature chunk and compares
its entire decoded SHA-256 with an independent Icechunk / Xarray / Pcodec decode:

```sh
NWF_CLIMATE_ARCHIVE_CHECK=1 go test -v ./internal/climatefacts -run TestPublicArchive
```

That exact decoded native tile is time chunk 84, row 17, column 118. Its SHA-256
is `33dce0fc238ef0aca0a87b8ea7b0f87b00f9915d8e3a076c75d12d4153388bad`.

Terrain and the native land fraction are fixed preparation inputs, not hourly
pipeline dependencies. See `preparation/README.md` for source and regeneration.
The temperature selector searches the native 3×3 neighbourhood, uses the nearest
cell with at least 80% land, otherwise the largest land fraction with distance
as the tie break. It wraps longitude and corrects temperature to target ground
height at 6.5 °C/km. Other fields keep their original source cell.

Sources: [Earthmover ERA5 archive](https://github.com/earth-mover/era5-icechunk),
[Icechunk format](https://icechunk.io/en/stable/reference/spec-v2/),
[ERA5 accumulation convention](https://confluence.ecmwf.int/display/CKB/ERA5%3A+data+documentation#heading-Meanratesfluxesandaccumulations).
