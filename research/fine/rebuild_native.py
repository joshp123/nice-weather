"""Rebuild native ERA5 intermediates in an isolated data root; never assemble.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
Raw tile and static-array symlinks are read only inputs. All generated files,
including the point/timezone selection and tile intermediates, belong to --root.
Run with the numpy/timezonefinder environment used by research/climate-v6.
"""
import argparse
import datetime
import hashlib
import json
import multiprocessing
import pathlib
import sys
import time

import numpy as np

CLIMATE_DIRECTORY = pathlib.Path(__file__).resolve().parents[1] / 'climate-v6'
sys.path.insert(0, str(CLIMATE_DIRECTORY))
import pipeline
from static import POINT_DTYPE, build_static

LOCAL_CHUNKS = [76, 78, 80, 82, 84]
STATIC_INPUTS = ['static_lsm.npy', 'static_z.npy', 'latitude.npy', 'longitude.npy']


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def expected_tiles(points):
    rows = points['fine'] // 1440 // 12
    columns = ((points['fine'] % 1440 + 720) % 1440) // 12
    return {f'r{row:02d}c{column:03d}.npz' for row, column in zip(rows, columns)}


def prepare(root, source_root, chunks):
    root, source_root = root.resolve(), source_root.resolve()
    if root == source_root or (root / 'v6').resolve() == (source_root / 'v6').resolve():
        raise ValueError('The rebuild root must be separate from the existing data root and v6')
    root.mkdir(parents=True, exist_ok=True)
    inputs = root / 'era5'
    inputs.mkdir(exist_ok=True)
    for name in STATIC_INPUTS + [f'chunk{chunk}' for chunk in chunks]:
        source = source_root / 'era5' / name
        target = inputs / name
        if not source.exists():
            raise FileNotFoundError(source)
        if target.exists() or target.is_symlink():
            if not target.is_symlink() or target.resolve() != source.resolve():
                raise ValueError(f'Rebuild input already points elsewhere: {target}')
        else:
            target.symlink_to(source, target_is_directory=source.is_dir())
    configuration = dict(temperature='native', chunks=sorted(chunks), source_root=str(source_root),
        pipeline_sha256=sha256(CLIMATE_DIRECTORY / 'pipeline.py'),
        static_sha256=sha256(CLIMATE_DIRECTORY / 'static.py'),
        static_input_sha256={name: sha256(inputs / name) for name in STATIC_INPUTS})
    manifest = root / 'native-rebuild.json'
    work = root / 'climate-v6-work'
    if manifest.exists():
        if json.loads(manifest.read_text())['configuration'] != configuration:
            raise ValueError('Existing native rebuild uses different inputs or processor; choose a fresh root')
    else:
        if (root / 'v6').exists() or work.exists():
            raise ValueError('Existing output has no native rebuild provenance; choose a fresh root')
        work.mkdir()
        points = build_static(root)
        manifest.write_text(json.dumps(dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',
            configuration=configuration, cells_sha256=sha256(root / 'v6/cells.bin'),
            points_sha256=sha256(root / 'v6/points.bin'), points=len(points),
            prepared_at_utc=datetime.datetime.now(datetime.timezone.utc).isoformat()), indent=2) + '\n')
    points = np.fromfile(root / 'v6/points.bin', dtype=POINT_DTYPE, offset=4)
    if np.any(points['source'] != points['fine']) or np.any(points['shift'] != 0):
        raise ValueError('Rebuild points must use their own native temperature and zero shift')
    pipeline.timezone_map(root, points)
    return root, points


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=pathlib.Path, required=True)
    parser.add_argument('--source-root', type=pathlib.Path, default=pipeline.ROOT)
    parser.add_argument('--chunks', type=int, nargs='+', default=LOCAL_CHUNKS)
    parser.add_argument('--workers', type=int, default=4)
    parser.add_argument('--prepare-only', action='store_true')
    parser.add_argument('--tile', action='append', help='Process only this tile name (repeat for bounded probes)')
    args = parser.parse_args()
    started = time.monotonic()
    root, points = prepare(args.root, args.source_root, args.chunks)
    required = expected_tiles(points)
    chosen = set(args.tile) if args.tile else required
    if chosen - required:
        raise ValueError(f'Tiles have no selected land points: {sorted(chosen-required)}')
    if args.prepare_only:
        print(json.dumps(dict(root=str(root), prepared=True, points=len(points),
            required_tiles_per_chunk=len(required), seconds=time.monotonic()-started)), flush=True)
        return
    jobs, missing, completed = [], [], 0
    for chunk in args.chunks:
        destination = root / f'climate-v6-work/chunk{chunk}'
        destination.mkdir(exist_ok=True)
        for name in sorted(chosen):
            source = root / f'era5/chunk{chunk}' / name
            if not source.exists():
                missing.append(str(source))
            elif (destination / name).exists():
                completed += 1
            else:
                jobs.append((str(root), chunk, source))
    if missing:
        raise FileNotFoundError(f'{len(missing)} missing required raw tiles; first: {missing[0]}')
    print(json.dumps(dict(root=str(root), pending_tiles=len(jobs), already_complete=completed)), flush=True)
    with multiprocessing.Pool(args.workers) as pool:
        for number, (path, seconds) in enumerate(pool.imap_unordered(pipeline.process_tile, jobs), 1):
            if number % 25 == 0 or number == len(jobs):
                print(json.dumps(dict(processed=number, total=len(jobs), tile=path,
                    tile_seconds=seconds, elapsed_seconds=time.monotonic()-started)), flush=True)
    if chosen == required:
        for chunk in args.chunks:
            folder = root / f'climate-v6-work/chunk{chunk}'
            epoch, dates = pipeline.chunk_dates(chunk)
            completion = dict(temperature='native', chunk=chunk, tiles=len(required),
                points_sha256=sha256(root / 'v6/points.bin'), cells_sha256=sha256(root / 'v6/cells.bin'),
                raw_root=str((root / f'era5/chunk{chunk}').resolve()), raw_start_utc=epoch.isoformat(),
                first_scored_day=str(dates[0]), last_scored_day=str(dates[-1]),
                completed_at_utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),
                intermediate_sha256={name: sha256(folder / name) for name in sorted(required)})
            (folder / 'complete.json').write_text(json.dumps(completion, indent=2) + '\n')
    result = dict(completed_at_utc=datetime.datetime.now(datetime.timezone.utc).isoformat(),
        chunks=args.chunks, tiles_requested_per_chunk=len(chosen), tiles_processed=len(jobs),
        already_complete=completed, seconds=time.monotonic()-started, assembly_run=False)
    with (root / 'climate-v6-work/native-timings.jsonl').open('a') as stream:
        stream.write(json.dumps(result) + '\n')
    if chosen == required:
        completion = dict(result, temperature='native', points_sha256=sha256(root / 'v6/points.bin'),
            cells_sha256=sha256(root / 'v6/cells.bin'), chunk_manifests={str(chunk):
                sha256(root / f'climate-v6-work/chunk{chunk}/complete.json') for chunk in args.chunks})
        (root / 'native-complete.json').write_text(json.dumps(completion, indent=2) + '\n')
    print(json.dumps(result), flush=True)


if __name__ == '__main__':
    main()
