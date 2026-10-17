"""Acquire only the five missing worldwide ERA5 replay fields anonymously.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
Original raw temperature and other workers' caches are never changed.
"""
import argparse
import concurrent.futures
import datetime as dt
import json
import os
import pathlib
import time

import numpy as np
from native_normals import file_sha256

SNAPSHOT = 'TZ2KX6A24CJ5DG17T7MG'
FIELDS = {'d2m': lambda a: a-273.15, 'u10': lambda a: a, 'v10': lambda a: a,
          'tp': lambda a: a*1000, 'fdir': lambda a: a/3600}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inventory', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    parser.add_argument('--workers', type=int, default=24)
    parser.add_argument('--priority-queries', type=pathlib.Path)
    parser.add_argument('--direct-public-source', action='store_true', help='Read anonymous public S3 directly in this process, without inherited model-client proxies')
    parser.add_argument('--max-files', type=int, help='Bound an acquisition throughput pilot; completed files remain useful')
    args = parser.parse_args()
    if args.direct_public_source:
        for name in ('HTTPS_PROXY', 'HTTP_PROXY', 'ALL_PROXY', 'https_proxy', 'http_proxy', 'all_proxy'):
            os.environ.pop(name, None)
    started = time.monotonic()
    inventory = json.loads(args.inventory.read_text())
    args.output.mkdir(parents=True, exist_ok=True)
    config = dict(snapshot=SNAPSHOT, chunks=inventory['chunks'], fields=list(FIELDS),
                  inventory_sha256=file_sha256(args.inventory), builder_sha256=file_sha256(__file__))
    config_path = args.output/'configuration.json'
    if config_path.exists():
        old = json.loads(config_path.read_text())
        if {key: value for key, value in old.items() if key != 'builder_sha256'} != {key: value for key, value in config.items() if key != 'builder_sha256'}:
            raise ValueError('Worldwide weather cache source configuration differs')
        history_path = args.output/'builder-history.json'
        history = json.loads(history_path.read_text()) if history_path.exists() else []
        for digest in (old.get('builder_sha256'), config['builder_sha256']):
            if digest and digest not in history: history.append(digest)
        history_path.write_text(json.dumps(history, indent=2)+'\n')
    config_path.write_text(json.dumps(config, indent=2)+'\n')
    jobs = []
    for chunk in inventory['chunks']:
        folder = args.output/f'chunk{chunk}'
        folder.mkdir(exist_ok=True)
        for name in inventory['raw'][str(chunk)]['incompleteTileNames']:
            if not (folder/name).exists(): jobs.append((chunk, name))
    priority = set()
    if args.priority_queries:
        for query in json.loads(args.priority_queries.read_text()):
            row = int(np.floor((np.floor((90-query['lat'])*120)+.5)/30+.5))
            column = int(np.floor((np.floor((query['lon']+180)*120)+.5)/30+.5))%1440
            priority.add(f'r{row//12:02d}c{((column+720)%1440)//12:03d}.npz')
    jobs.sort(key=lambda job: (job[1] not in priority, job[1], job[0]))
    missing_before = len(jobs)
    if args.max_files is not None: jobs = jobs[:args.max_files]
    print('Missing five-field tile-chunks:', len(jobs), flush=True)
    import icechunk
    import pcodec
    import zarr
    storage = icechunk.s3_storage(bucket='earthmover-icechunk-era5', prefix='icechunkV2', region='us-east-1', anonymous=True)
    session = icechunk.Repository.open(storage).readonly_session(snapshot_id=SNAPSHOT)
    group = zarr.open_group(session.store, path='single/temporal', mode='r')
    archive_fields = {name: group[name] for name in FIELDS}
    field_pool = concurrent.futures.ThreadPoolExecutor(max_workers=args.workers*5)

    def fetch(job):
        chunk, name = job
        read_started = time.monotonic()
        row, column = int(name[1:3]), int(name[4:7])
        index = (slice(chunk*8736, (chunk+1)*8736), slice(row*12, min(721, row*12+12)), slice(column*12, column*12+12))
        def read(name):
            for attempt in range(6):
                try:
                    array = FIELDS[name](archive_fields[name][index]).astype(np.float16)
                    if not np.isfinite(array).all(): raise ValueError('Nonfinite archive weather')
                    return name, array
                except Exception:
                    if attempt == 5: raise
                    time.sleep(min(30, 2**attempt))
        arrays = dict(field_pool.map(read, FIELDS))
        read_seconds = time.monotonic()-read_started
        save_started = time.monotonic()
        path = args.output/f'chunk{chunk}'/name
        temporary = path.with_suffix('.partial.npz')
        np.savez_compressed(temporary, **arrays)
        with np.load(temporary) as saved:
            if set(saved.files) != set(FIELDS): raise ValueError('Incomplete saved weather fields')
            for key, value in arrays.items():
                if not np.array_equal(saved[key], value): raise ValueError('Saved weather differs from archive read')
        temporary.replace(path)
        return path.stat().st_size, read_seconds, time.monotonic()-save_started

    downloaded = byte_count = 0
    read_times, save_times = [], []
    failures = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        futures = {pool.submit(fetch, job): job for job in jobs}
        for future in concurrent.futures.as_completed(futures):
            try:
                size, read_seconds, save_seconds = future.result()
                byte_count += size
                read_times.append(read_seconds); save_times.append(save_seconds)
                downloaded += 1
            except Exception as error:
                failures.append(dict(job=futures[future], error=str(error)))
                print('Weather download failed', futures[future], type(error).__name__, str(error)[:250], flush=True)
            if (downloaded+len(failures))%25 == 0:
                progress = dict(completed=downloaded, pending=len(jobs)-downloaded-len(failures),
                                failures=len(failures), bytes=byte_count, seconds=time.monotonic()-started,
                                recentMeanReadSeconds=float(np.mean(read_times[-25:])), recentMeanSaveSeconds=float(np.mean(save_times[-25:])))
                (args.output/'progress.json').write_text(json.dumps(progress, indent=2)+'\n')
                print(json.dumps(progress), flush=True)
    field_pool.shutdown()
    timing = dict(downloaded=downloaded, bytes=byte_count, failures=failures, seconds=time.monotonic()-started,
                  remainingTileChunks=missing_before-downloaded,
                  route='direct anonymous public S3' if args.direct_public_source else 'inherited network environment',
                  completedUTC=dt.datetime.now(dt.timezone.utc).isoformat(), configuration=config)
    (args.output/'timing.json').write_text(json.dumps(timing, indent=2)+'\n')
    if failures: raise RuntimeError(f'{len(failures)} weather downloads failed; rerun retains complete files')
    print(json.dumps(timing), flush=True)


if __name__ == '__main__': main()
