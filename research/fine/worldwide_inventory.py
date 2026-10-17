"""Inventory actual worldwide B1 inputs without decoding or changing raw weather.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
Run inside the project devenv with NumPy. This does not build publishable data.
"""
import argparse
import datetime as dt
import json
import pathlib
import shutil
import time
import zipfile

import numpy as np

POINT_DTYPE = np.dtype([('fine', '<u4'), ('source', '<u4'), ('shift', '<i2'), ('cell', '<u2')])
RAW_FIELDS = {'t2m.npy', 'd2m.npy', 'u10.npy', 'v10.npy', 'tp.npy', 'fdir.npy'}


def raw_tile_name(fine):
    row, column = divmod(int(fine), 1440)
    return f'r{row//12:02d}c{((column+720)%1440)//12:03d}.npz'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--data-root', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args()
    started = time.monotonic()
    root = args.data_root
    reference_root = root/'fine-build/reference-global'
    configuration = json.loads((root/'fine-30s/configuration.json').read_text())
    land = np.memmap(root/'fine-build/land-support-v2/land.u8', mode='r', dtype='u1', shape=(21600, 43200))
    native_point_counts = np.zeros(721*1440, dtype=np.uint32)
    for first in range(0, 21600, 256):
        rows, columns = np.nonzero(land[first:first+256])
        rows += first
        fine = (np.floor((rows+.5)/30+.5).astype(int)*1440 +
                np.floor((columns+.5)/30+.5).astype(int)%1440)
        native_point_counts += np.bincount(fine, minlength=len(native_point_counts)).astype(np.uint32)
    required_points = np.flatnonzero(native_point_counts)
    required_tiles = sorted({raw_tile_name(fine) for fine in required_points})
    inventory = {}
    for chunk in configuration['chunks']:
        full_tiles, incomplete_tiles, missing_tiles, field_counts = [], [], [], {}
        compressed_bytes = full_bytes = incomplete_bytes = 0
        for name in required_tiles:
            path = reference_root/f'era5/chunk{chunk}'/name
            if not path.exists():
                missing_tiles.append(name)
                continue
            with zipfile.ZipFile(path) as archive:
                fields = set(archive.namelist())
            size = path.stat().st_size
            compressed_bytes += size
            for field in fields:
                field_counts[field] = field_counts.get(field, 0)+1
            if RAW_FIELDS <= fields:
                full_tiles.append(name)
                full_bytes += size
            else:
                incomplete_tiles.append(name)
                incomplete_bytes += size
        incomplete_set = set(incomplete_tiles+missing_tiles)
        missing_point_weather = np.array([raw_tile_name(fine) in incomplete_set for fine in required_points])
        inventory[str(chunk)] = dict(requiredTiles=len(required_tiles), files=len(full_tiles)+len(incomplete_tiles),
            fullWeatherTiles=len(full_tiles), temperatureOnlyOrIncompleteTiles=len(incomplete_tiles),
            missingTiles=missing_tiles, fields=field_counts, compressedBytes=compressed_bytes,
            fullWeatherBytes=full_bytes, incompleteBytes=incomplete_bytes, incompleteTileNames=incomplete_tiles,
            nativeFullWeatherPoints=int((~missing_point_weather).sum()),
            nativeMissingOtherFieldsPoints=int(missing_point_weather.sum()),
            finePixelsMissingOtherFields=int(native_point_counts[required_points[missing_point_weather]].sum()))
    points = np.fromfile(reference_root/'v6/points.bin', dtype=POINT_DTYPE, offset=4)
    zones = np.load(reference_root/'climate-v6-work/timezones.npy')
    positions = np.searchsorted(points['fine'], required_points)
    exists = positions < len(points)
    exists[exists] = points['fine'][positions[exists]] == required_points[exists]
    tiles = list((root/'fine-30s/tiles').rglob('*.gz'))
    packets = list((root/'fine-b1-diagnostic/native').glob('*.gz'))
    result = dict(written_by='AI, GPT-6.1 Sol, 2026.10.18',
        measuredUTC=dt.datetime.now(dt.timezone.utc).isoformat(), seconds=time.monotonic()-started,
        chunks=configuration['chunks'], generation=configuration['generation'],
        supportedFinePixels=int(native_point_counts.sum()), requiredNativePoints=len(required_points),
        referenceNativePoints=len(points), missingNativePoints=int((~exists).sum()),
        nativeTimezones=len(set(zones.tolist())), requiredNativeTimezones=len(set(zones[positions[exists]].tolist())),
        requiredTileNames=required_tiles, raw=inventory,
        correctionTiles=dict(files=len(tiles), bytes=sum(path.stat().st_size for path in tiles),
                             manifestExists=(root/'fine-30s/manifest.json').exists()),
        diagnosticPackets=dict(files=len(packets), bytes=sum(path.stat().st_size for path in packets)),
        diskFree=shutil.disk_usage(root).free,
        limits='ZIP central-directory fields only, not raw payload verification. Native timezones are not exact 1km target-place timezone coverage.')
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2)+'\n')
    print(json.dumps({key: value for key, value in result.items() if key not in ('raw', 'requiredTileNames')}, indent=2))
    print('Full per-chunk counts and file names:', args.output)


if __name__ == '__main__':
    main()
