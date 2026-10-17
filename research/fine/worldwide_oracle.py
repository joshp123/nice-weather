"""Independently replay proof queries through the original point-weather path.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
Private full hourly JSON fixtures; not input to published values.
"""
import argparse
import functools
import gzip
import json
import multiprocessing as mp
import os
import pathlib
import struct
import time

import numpy as np
from b1_data import build_packet
from fine_city_probe import NativeReferences


@functools.lru_cache(maxsize=1)
def reference(raw_root, reference_root, chunks, cache):
    return NativeReferences(pathlib.Path(raw_root), pathlib.Path(reference_root), list(chunks), pathlib.Path(cache))


def fixture(job):
    args, config, query, fine, zone = job
    destination = args.output/'hourly'/f"{query['slug']}.json.gz"
    if destination.exists():
        return dict(query=query, fine=fine, zone=zone, path=str(destination.relative_to(args.output)))
    while not (args.replay/f'native/{fine}/{zone}.nwfp.gz').exists(): time.sleep(2)
    row, column = divmod(fine, 1440)
    name = f'r{row//12:02d}c{((column+720)%1440)//12:03d}.npz'
    combined = args.output/'combined-raw'
    for chunk in config['chunks']:
        original_path = args.reference_root/f'era5/chunk{chunk}'/name
        destination_path = combined/f'chunk{chunk}'/name
        if destination_path.exists(): continue
        destination_path.parent.mkdir(parents=True, exist_ok=True)
        with np.load(original_path) as original:
            missing = {'d2m', 'u10', 'v10', 'tp', 'fdir'}-set(original.files)
        if not missing:
            try: destination_path.symlink_to(original_path)
            except FileExistsError: pass
        else:
            supplement = args.weather_cache/f'chunk{chunk}'/name
            while not supplement.exists(): time.sleep(2)
            with np.load(original_path) as original:
                arrays = {field: original[field] for field in original.files}
            with np.load(supplement) as extra:
                arrays.update({field: extra[field] for field in missing})
            temporary = destination_path.with_name(destination_path.stem+f'.{os.getpid()}.partial.npz')
            np.savez_compressed(temporary, **arrays)
            temporary.replace(destination_path)
    native = reference(str(combined), str(args.reference_root), tuple(config['chunks']), str(args.output/'native-reference-cache'/str(os.getpid())))
    index = np.searchsorted(native.fine, fine)
    own_name = str(native.zones[index])
    target_name = config['timezoneNames'][zone]
    native.point.cache_clear()
    native.zone = lambda _fine: target_name
    packet = build_packet(native, args.native_root, fine, config['generation'])
    clock_delta = np.zeros((12, 2))
    if own_name != target_name:
        native.point.cache_clear()
        native.zone = lambda _fine: own_name
        own = native.point(fine)
        months = np.array([int(date[5:7]) for date in packet['dates']])
        for month in range(1, 13):
            selected = months == month
            clock_delta[month-1] = [own['high'][selected].mean()-np.asarray(packet['highC'])[selected].mean(),
                                   own['low'][selected].mean()-np.asarray(packet['lowC'])[selected].mean()]
    packet['clockBaseOffsetC'] = clock_delta.tolist()
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(gzip.compress(json.dumps(packet, separators=(',', ':')).encode(), mtime=0))
    return dict(query=query, fine=fine, zone=zone, path=str(destination.relative_to(args.output)))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--replay', type=pathlib.Path, required=True)
    parser.add_argument('--reference-root', type=pathlib.Path, required=True)
    parser.add_argument('--native-root', type=pathlib.Path, required=True)
    parser.add_argument('--queries', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    parser.add_argument('--workers', type=int, default=2)
    parser.add_argument('--weather-cache', type=pathlib.Path)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    config = json.loads((args.replay/'manifest.json').read_text())
    queries = json.loads(args.queries.read_text())
    jobs, skipped = [], []
    zone_cache = {}
    for query in queries:
        row = int(np.floor((90-query['lat'])*120))
        column = int(np.floor((query['lon']+180)*120))%43200
        key = (row//256, column//256)
        if key not in zone_cache:
            zone_cache[key] = gzip.decompress((args.replay/f'zones/{key[0]:03d}/{key[1]:03d}.nwz.gz').read_bytes())
        zone = struct.unpack_from('<H', zone_cache[key], 32+((row%256)*256+column%256)*2)[0]
        fine = int(np.floor((row+.5)/30+.5))*1440+int(np.floor((column+.5)/30+.5))%1440
        native_row, native_column = divmod(fine, 1440)
        name = f'r{native_row//12:02d}c{((native_column+720)%1440)//12:03d}.npz'
        with np.load(args.reference_root/f"era5/chunk{config['chunks'][0]}"/name) as raw:
            full = 'd2m' in raw.files
        if not full and not args.weather_cache:
            skipped.append(dict(query=query, reason='Independent original point-weather path requires original full six-field tile'))
            continue
        jobs.append((args, config, query, fine, zone))
    jobs.sort(key=lambda job: not (args.replay/f'native/{job[3]}/{job[4]}.nwfp.gz').exists())
    results = []
    started = time.monotonic()
    with mp.get_context('spawn').Pool(args.workers) as pool:
        for result in pool.imap_unordered(fixture, jobs):
            results.append(result)
            (args.output/'manifest.json').write_text(json.dumps(dict(complete=False, expected=len(jobs), cases=results, skipped=skipped), indent=2)+'\n')
            print('Independent full hourly fixture', len(results), '/', len(jobs), result['query']['slug'], flush=True)
    (args.output/'manifest.json').write_text(json.dumps(dict(complete=True, expected=len(jobs), cases=results, skipped=skipped,
        seconds=time.monotonic()-started, method='Original b1_data.build_packet / compare_step_a.point_weather, target timezone, no bulk builder calls'), indent=2)+'\n')


if __name__ == '__main__': main()
