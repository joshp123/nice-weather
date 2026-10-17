#!/bin/sh
# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
set -eu
cd "$(dirname "$0")/../.."
export UV_CACHE_DIR="${UV_CACHE_DIR:-/tmp/uv-cache}"
case "$1" in
  hourly_stations.py) set -- --with pyarrow python "research/fine/$@" ;;
  *) set -- --with numpy --with pandas --with tifffile --with imagecodecs --with icechunk --with pcodec python "research/fine/$@" ;;
esac
exec devenv shell -- uv run --python 3.12 "$@"
