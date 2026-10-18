#!/bin/sh
# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
set -eu
cd "$(dirname "$0")/../.."
export UV_CACHE_DIR="${UV_CACHE_DIR:-/tmp/uv-cache}"
script="$1"
shift
case "$script" in
  observations.py) set -- python "research/sun/$script" "$@" ;;
  satellite.py) set -- --with numpy --with h5py --with fsspec --with aiohttp python "research/sun/$script" "$@" ;;
  named_goes.py) set -- --with numpy --with scipy --with h5py --with fsspec --with aiohttp python "research/sun/$script" "$@" ;;
  named_profiles.py|named_satellite.py) set -- --with numpy python "research/sun/$script" "$@" ;;
  *) set -- --with numpy --with pillow python "research/sun/$script" "$@" ;;
esac
exec devenv shell -- uv run --python 3.12 "$@"
