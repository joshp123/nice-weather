#!/bin/sh
# Written by AI on 2026.10.26 by model Claude Fable 5.1.
# The build reads the published climate files (vite.config.ts via research/site/scoring-explainer-data.mjs).
# This fetches them from the live site into artifacts/v6, which is not in git. Run it once in a fresh checkout.
set -e
cd "$(dirname "$0")/../.."
mkdir -p artifacts/v6/climate
for key in cells.bin climate/meta.json $(seq -f 'climate/month-%02g.bin' 1 12); do
  [ -s "artifacts/v6/$key" ] || curl -sSf "https://nice.jjpcodes.com/data/v6/$key" -o "artifacts/v6/$key"
done
echo "build data ready in artifacts/v6"
