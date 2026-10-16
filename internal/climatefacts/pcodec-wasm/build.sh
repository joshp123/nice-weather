#!/usr/bin/env bash
# Written by AI on 2026.10.16 by model GPT-6.1 Sol.
set -euo pipefail
repository_root="$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
cd "$repository_root"
export RUSTUP_HOME="$repository_root/.cache/climate-decoder-build/rustup"
export CARGO_HOME="$repository_root/.cache/climate-decoder-build/cargo"
export CARGO_TARGET_DIR="$repository_root/.cache/climate-decoder-build/target"
devenv shell --option packages:pkgs rustup -- rustup toolchain install 1.90.0 \
  --profile minimal --target wasm32-unknown-unknown --no-self-update
devenv shell --option packages:pkgs rustup -- cargo +1.90.0 build --locked \
  --release --target wasm32-unknown-unknown \
  --manifest-path internal/climatefacts/pcodec-wasm/Cargo.toml
cp "$CARGO_TARGET_DIR/wasm32-unknown-unknown/release/niceweather_pcodec_decoder.wasm" \
  internal/climatefacts/pcodec-wasm/decoder.wasm
