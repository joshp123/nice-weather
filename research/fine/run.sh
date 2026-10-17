#!/bin/sh
# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
set -eu
cd "$(dirname "$0")/../.."
export UV_CACHE_DIR="${UV_CACHE_DIR:-/tmp/uv-cache}"
case "$1" in
  land_support.py|audit_land.py|settlement_sources.py|settlement_anchors.py|worldwide_replay.py) set -- --with rasterio --with pyshp --with shapely --with numpy --with pandas --with pillow --with timezonefinder --with h3==4.3.1 --with tifffile --with imagecodecs --with icechunk --with zarr --with pcodec python "research/fine/$@" ;;
  hourly_stations.py|compare_step_a.py) set -- --with pyarrow --with numpy --with pandas --with pillow --with timezonefinder --with h3==4.3.1 --with tifffile --with imagecodecs --with icechunk --with zarr --with pcodec python "research/fine/$@" ;;
  *) set -- --with numpy --with pandas --with pillow --with timezonefinder --with h3==4.3.1 --with tifffile --with imagecodecs --with icechunk --with zarr --with pcodec python "research/fine/$@" ;;
esac
exec devenv shell -- uv run --python 3.12 "$@"
