# ERA5 Pcodec decoder

Written by AI on 2026.10.16 by model GPT-6.1 Sol.

The public Earthmover ERA5 archive uses Zarr's `numcodecs.pcodec` compression.
Its [Python codec source](https://numcodecs.readthedocs.io/en/latest/_modules/numcodecs/pcodec.html)
decodes the buffer directly with `pcodec.standalone.simple_decompress`; it
adds no shape or byte-order header. The Zarr metadata supplies array shape.

`decoder.wasm` contains the upstream Rust Pco decoder, pinned at **1.0.4**,
plus the small allocation interface in `src/lib.rs`. Its Go caller embeds the
module and executes it with wazero. The climate executable needs no Python,
Rust installation, external process, network codec service or C library.

The module exports:

- `memory`;
- `allocate(byte_length) -> pointer`, aligned for f32;
- `deallocate(pointer, byte_length)`;
- `decode(input_pointer, input_bytes, output_pointer, output_capacity_floats)
  -> signed count`: a positive decoded f32 count, `-1` for malformed data,
  or `-2` if the output buffer is too small.

It has no imported functions. Decompression writes directly to the caller's
output slice. Array order is unchanged; the archive uses C order, with longitude
varying fastest. One regular archive chunk is 8,736 × 12 × 12 float32 values.

Rebuild from the repository root:

```bash
bash internal/climatefacts/pcodec-wasm/build.sh
```

The script uses the repository's pinned Nix development environment with
`rustup` added for this invocation. Rust **1.90.0**, its wasm32 standard library,
Cargo downloads and build products stay under `.cache/climate-decoder-build`.
`Cargo.lock` pins all transitive dependencies. The release module is 140,448
bytes; SHA-256:
`23a618731ac66d12ab00de336bd54975e824136daaac78d3950d83e594a112e3`.

## Upstream licences

Pco, `better_io` and `dtype_dispatch` are Apache-2.0. The other linked crates
(`cfg-if`, `half`, `rand_core`, `rand_xoshiro`, `zerocopy`) permit Apache-2.0
as one of their licence choices. Build-time procedural-macro crates
`proc-macro2`, `quote`, `syn`, `unicode-ident`, `zerocopy-derive` also permit
Apache-2.0. See `Cargo.lock` for versions and registry checksums,
`LICENSE.pco` for the Apache-2.0 text and `COPYRIGHT.rand` for the rand notice.
`unicode-ident` additionally includes Unicode-3.0; its text is retained in
`LICENSE.unicode`. Upstream repositories are linked in each crate's Cargo
metadata available from [crates.io](https://crates.io/).
