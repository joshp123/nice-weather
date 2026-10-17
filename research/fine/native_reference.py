"""Prepare a global native ERA5 temperature reference, including polar land.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
Reuse every local raw tile. Download t2m alone for missing tiles whose ERA5
land fraction exceeds 0.001. The resulting private reference points also
include ocean points within those tiles: a fine coastal pixel never borrows
an inland point's temperature. These are not the published v6 land points.
"""
import argparse
import concurrent.futures
import datetime as dt
import json
import pathlib
import time

import numpy as np

from native_normals import build_normals,file_sha256
from static import POINT_DTYPE

SNAPSHOT = 'TZ2KX6A24CJ5DG17T7MG'


def reference_tiles(source_root,chunks,tile_list=None):
    if tile_list:
        return sorted(set(json.loads(pathlib.Path(tile_list).read_text())))
    lsm = np.load(source_root/'era5/static_lsm.npy')
    names = {path.name for chunk in chunks for path in (source_root/f'era5/chunk{chunk}').glob('r*.npz')
             if '.partial' not in path.name}
    for row in range(61):
        for column in range(120):
            if np.any(lsm[row*12:min(721,row*12+12),column*12:column*12+12]>.001):
                names.add(f'r{row:02d}c{column:03d}.npz')
    return sorted(names)


def prepare(root,source_root,chunks,tile_list=None,legacy_root=None):
    root.mkdir(parents=True,exist_ok=True)
    names = reference_tiles(source_root,chunks,tile_list)
    configuration = dict(snapshot=SNAPSHOT,chunks=sorted(chunks),tiles=names,
        source_root=str(source_root.resolve()),land_fraction_threshold=None if tile_list else .001,
        tile_list_sha256=file_sha256(tile_list) if tile_list else None,
        legacy_timezones_sha256=file_sha256(legacy_root/'climate-v6-work/timezones.npy') if legacy_root else None,
        lsm_sha256=file_sha256(source_root/'era5/static_lsm.npy'))
    manifest = root/'reference-configuration.json'
    if manifest.exists() and json.loads(manifest.read_text()) != configuration:
        raise ValueError('Reference inputs differ; choose a new folder')
    manifest.write_text(json.dumps(configuration,indent=2)+'\n')
    (root/'era5').mkdir(exist_ok=True)
    for name in ('static_lsm.npy','static_z.npy','latitude.npy','longitude.npy'):
        target = root/'era5'/name
        if not target.exists(): target.symlink_to(source_root/'era5'/name)
    (root/'v6').mkdir(exist_ok=True)
    (root/'climate-v6-work').mkdir(exist_ok=True)
    point_path = root/'v6/points.bin'
    if not point_path.exists():
        fine = []
        for name in names:
            tile_row,tile_column = int(name[1:3]),int(name[4:7])
            rows,columns = np.meshgrid(np.arange(tile_row*12,min(721,tile_row*12+12)),
                (np.arange(tile_column*12,tile_column*12+12)+720)%1440,indexing='ij')
            fine.extend((rows*1440+columns).ravel())
        fine = np.unique(fine)
        points = np.zeros(len(fine),dtype=POINT_DTYPE)
        points['fine'] = fine
        points['source'] = fine
        with point_path.open('wb') as stream:
            np.array([len(points)],dtype='<u4').tofile(stream)
            points.tofile(stream)
        (root/'v6/REFERENCE-ONLY.txt').write_text(
            'Private all-point temperature reference. Not v6 publication/static data.\n')
    points = np.fromfile(point_path,dtype=POINT_DTYPE,offset=4)
    zones_path = root/'climate-v6-work/timezones.npy'
    if not zones_path.exists():
        from timezonefinder import TimezoneFinder
        finder = TimezoneFinder(in_memory=True)
        legacy_zones = {}
        if legacy_root:
            legacy_points = np.fromfile(legacy_root/'v6/points.bin',dtype=POINT_DTYPE,offset=4)
            legacy_names = np.load(legacy_root/'climate-v6-work/timezones.npy')
            legacy_zones = dict(zip(legacy_points['fine'].tolist(),legacy_names.tolist()))
        zones = []
        for fine in points['fine']:
            row,column = divmod(int(fine),1440)
            latitude,longitude = 90-row*.25,-180+column*.25
            name = legacy_zones.get(int(fine)) or finder.timezone_at(lat=latitude,lng=longitude)
            if name is None:
                offset = int(np.clip(np.rint(longitude/15),-12,12))
                name = f'Etc/GMT{-offset:+d}' if offset else 'Etc/GMT'
            zones.append(name)
        np.save(zones_path,np.array(zones))
    pending = []
    for chunk in chunks:
        (root/f'era5/chunk{chunk}').mkdir(exist_ok=True)
        for name in names:
            destination = root/f'era5/chunk{chunk}'/name
            if destination.exists(): continue
            existing = source_root/f'era5/chunk{chunk}'/name
            if existing.exists(): destination.symlink_to(existing)
            else: pending.append((chunk,name))
    return points,pending


def fetch_missing(root,pending,workers):
    import icechunk
    import pcodec
    import zarr
    storage = icechunk.s3_storage(bucket='earthmover-icechunk-era5',prefix='icechunkV2',
                                 region='us-east-1',anonymous=True)
    session = icechunk.Repository.open(storage).readonly_session(snapshot_id=SNAPSHOT)
    field = zarr.open_group(session.store,path='single/temporal',mode='r')['t2m']
    started = time.monotonic()

    def fetch(job):
        chunk,name = job
        row,column = int(name[1:3]),int(name[4:7])
        for attempt in range(3):
            try:
                values = (field[chunk*8736:(chunk+1)*8736,
                    row*12:min(721,row*12+12),column*12:column*12+12]-273.15).astype(np.float16)
                break
            except Exception:
                if attempt == 2: raise
                # The anonymous archive has returned zero-byte-throughput
                # manifest reads during this build; retry that same input.
                time.sleep(2**attempt)
        if not np.isfinite(values).all(): raise ValueError(f'Incomplete native reference: {chunk}/{name}')
        destination = root/f'era5/chunk{chunk}'/name
        temporary = destination.with_suffix('.partial.npz')
        np.savez_compressed(temporary,t2m=values)
        temporary.replace(destination)
        return destination.stat().st_size

    stored_bytes = 0
    failures = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        futures = {pool.submit(fetch,job):job for job in pending}
        for number,future in enumerate(concurrent.futures.as_completed(futures),1):
            try:
                stored_bytes += future.result()
            except Exception as error:
                failures.append((futures[future],str(error)))
                print(f'Native reference read failed {futures[future]}: {error}',flush=True)
            if number%25 == 0 or number == len(pending):
                print(f'native reference {number}/{len(pending)} {stored_bytes:,} stored bytes '
                      f'{time.monotonic()-started:.1f}s',flush=True)
    if failures:
        raise RuntimeError(f'{len(failures)} native reference reads failed; rerun to retry only missing tiles: {failures[:3]}')
    return dict(snapshot=SNAPSHOT,downloaded_tiles=len(pending),stored_bytes=stored_bytes,
                seconds=time.monotonic()-started)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-root',type=pathlib.Path,required=True)
    parser.add_argument('--root',type=pathlib.Path,required=True)
    parser.add_argument('--meta',type=pathlib.Path,required=True)
    parser.add_argument('--workers',type=int,default=8)
    parser.add_argument('--count-only',action='store_true')
    parser.add_argument('--download-only',action='store_true',help='Warm missing t2m cache; do not build reference points or normals yet')
    parser.add_argument('--prepare-only',action='store_true',help='Prepare exact reference coverage and timezones without fetching or reducing weather')
    parser.add_argument('--tile-list',type=pathlib.Path,help='Fine-land support preflight reference-tiles.json')
    parser.add_argument('--legacy-root',type=pathlib.Path,help='Preserve the native rebuild timezones for existing v6 points')
    args = parser.parse_args()
    chunks = sorted(json.loads(args.meta.read_text())['chunks'])
    if args.count_only or args.download_only:
        names = reference_tiles(args.source_root,chunks,args.tile_list)
        missing = [(chunk,name) for chunk in chunks for name in names
                   if not (args.source_root/f'era5/chunk{chunk}'/name).exists()
                   and not (args.root/f'era5/chunk{chunk}'/name).exists()]
        print(json.dumps(dict(tiles=len(names),missing_temperature_tiles=len(missing),chunks=chunks)),flush=True)
        if args.download_only:
            for chunk in chunks: (args.root/f'era5/chunk{chunk}').mkdir(parents=True,exist_ok=True)
            timing = fetch_missing(args.root,missing,args.workers)
            timing.update(chunks=chunks,definition='Pinned native t2m only; reusable reference input cache',
                          completed_utc=dt.datetime.now(dt.timezone.utc).isoformat())
            (args.root/'reference-temperature-cache.json').write_text(json.dumps(timing,indent=2)+'\n')
        return
    points,pending = prepare(args.root,args.source_root,chunks,args.tile_list,args.legacy_root)
    if args.prepare_only:
        print(json.dumps(dict(points=len(points),missing_temperature_tiles=len(pending),chunks=chunks)),flush=True)
        return
    timing = fetch_missing(args.root,pending,args.workers)
    build_normals(args.root,args.root/'normals',chunks,min(args.workers,4))
    timing.update(points=len(points),completed_utc=dt.datetime.now(dt.timezone.utc).isoformat())
    (args.root/'reference-complete.json').write_text(json.dumps(timing,indent=2)+'\n')


if __name__ == '__main__':
    main()
