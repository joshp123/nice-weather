"""Written by AI on 2026.10.16 by model GPT-6.1 Sol.

Offline conversion of frozen research inputs. The climate runtime is Go and
does not import or invoke this script. Inputs retain their own source metadata:
ERA5 static snapshot TZ2KX6A24CJ5DG17T7MG, WorldClim 2.1 2.5' elevation
and NOAA ETOPO1 ice surface for the missing polar/coastal targets.
"""

import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import zipfile

import numpy as np
import tifffile

parser = argparse.ArgumentParser()
parser.add_argument("--era5", type=Path, required=True)
parser.add_argument("--worldclim", type=Path, required=True)
parser.add_argument("--etopo", type=Path, required=True, help="NOAA ETOPO1 ice-surface quarter-degree float32LE grid")
parser.add_argument("--out", type=Path, required=True)
args = parser.parse_args()
era5 = np.load(args.era5)
land = era5["lsm"].astype("<f4")
source = era5["elevation"].astype("<f4")
with zipfile.ZipFile(args.worldclim) as archive:
    name = next(name for name in archive.namelist() if name.endswith(".tif"))
    terrain = tifffile.imread(io.BytesIO(archive.read(name))).astype(float)
terrain[terrain == -32768] = np.nan
latitude = 90 - np.arange(721) * .25
longitude = np.arange(1440) * .25
longitude[longitude >= 180] -= 360
row_center = (90 - latitude) * 24 - .5
column_center = (longitude + 180) * 24 - .5
nearest_row = np.rint(row_center).astype(int)
nearest_column = np.rint(column_center).astype(int)
distance_so_far = np.full((721, 1440), np.inf)
ground = np.full((721, 1440), np.nan)
for row_offset in range(-3, 4):
    for column_offset in range(-3, 4):
        rows = nearest_row + row_offset
        columns = (nearest_column + column_offset) % 8640
        valid_rows = (rows >= 0) & (rows < 4320)
        values = terrain[np.clip(rows, 0, 4319)[:, None], columns[None, :]]
        distance = ((rows-row_center)[:, None]/24)**2 + ((nearest_column+column_offset-column_center)[None, :]/24*np.cos(np.radians(latitude))[:, None])**2
        nearer_land = valid_rows[:, None] & np.isfinite(values) & (distance < distance_so_far)
        ground[nearer_land] = values[nearer_land]
        distance_so_far[nearer_land] = distance[nearer_land]
missing = ~np.isfinite(ground)
etopo = np.fromfile(args.etopo, dtype="<f4").reshape(721, 1440)
assert np.isfinite(etopo).all()
# ETOPO includes bathymetry. A missing WorldClim pixel with a negative ETOPO
# height is a sea-level coastal/sea target, never the bottom of the ocean.
complement = np.maximum(etopo, 0)
ground[missing] = complement[missing]
assert all(a.shape == (721, 1440) for a in (land, source, ground))
assert all(np.isfinite(a).all() for a in (land, source, ground))
raw = b"NWFSTAT1" + land.tobytes() + source.tobytes() + ground.tobytes()
with args.out.open("wb") as target:
    with gzip.GzipFile(fileobj=target, mode="wb", mtime=0, compresslevel=6) as compressed:
        compressed.write(raw)
metadata = {
    "writtenBy": "AI, GPT-6.1 Sol, 2026-10-16",
    "era5Snapshot": "TZ2KX6A24CJ5DG17T7MG",
    "groundSource": "WorldClim 2.1 elevation, 2.5 arcminutes",
    "groundSourceURL": "https://geodata.ucdavis.edu/climate/worldclim/2_1/base/wc2.1_2.5m_elev.zip",
    "groundSampling": "nearest valid terrain pixel in the surrounding 7x7, geographic distance; longitude wraps",
    "groundComplementSource": "NOAA ETOPO1 Ice Surface, 1 arcminute source sampled at exact 0.25-degree nodes",
    "groundComplementURL": "https://data.pmel.noaa.gov/socat/erddap/griddap/etopo180.csv?altitude[0:15:10800][0:15:21585]",
    "groundComplementDatum": "WGS84 horizontal, mean sea level vertical; top of Antarctic and Greenland ice sheets",
    "groundComplementBathymetry": "negative values clamped to sea level only where WorldClim is missing; valid WorldClim below-sea-level ground is retained",
    "groundComplementSHA256": hashlib.sha256(args.etopo.read_bytes()).hexdigest(),
    "groundComplementPositiveWithERA5Land": int((missing & (land > 0) & (etopo > 0)).sum()),
    "groundComplementSeaLevelWithERA5Land": int((missing & (land > 0) & (etopo <= 0)).sum()),
    "groundMissingCellsBeforeComplement": int(missing.sum()),
    "groundMissingWithERA5LandBeforeComplement": int((missing & (land > 0)).sum()),
    "landFractionCountNote": "lsm > 0 includes tiny nonzero ocean fractions; it is not the application land mask",
    "worldclimInputSHA256": hashlib.sha256(args.worldclim.read_bytes()).hexdigest(),
    "era5InputSHA256": hashlib.sha256(args.era5.read_bytes()).hexdigest(),
    "sha256": hashlib.sha256(args.out.read_bytes()).hexdigest(),
    "bytes": args.out.stat().st_size,
}
args.out.with_suffix(args.out.suffix + ".json").write_text(json.dumps(metadata, indent=2))
print(json.dumps(metadata))
