#!/bin/sh
# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
set -eu
cd /Users/josh/code/nice-weather-finder
exec nix shell --impure --expr 'let pkgs = import (builtins.getFlake "nixpkgs") {}; in pkgs.python3.withPackages (p: [p.numpy p.pillow p.timezonefinder])' -c python3 research/climate-v6/pipeline.py "$@"
