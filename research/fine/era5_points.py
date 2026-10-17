"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Fetch bounded, account-free ERA5 point histories; retain points, not global tiles.
"""
import argparse
import concurrent.futures
import json
import pathlib
import time

import icechunk
import numpy as np
import pcodec  # Registers the archive's compression codec.
import zarr

SNAPSHOT = 'TZ2KX6A24CJ5DG17T7MG'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stations', type=pathlib.Path, default=pathlib.Path('/tmp/nwf-agents/round4/fine/stations/evaluation_stations.json'))
    parser.add_argument('--scratch', type=pathlib.Path, default=pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-phase1'))
    parser.add_argument('--live-points', type=pathlib.Path, default=pathlib.Path('/tmp/nwf-agents/round4/fine/live_points.json'))
    parser.add_argument('--workers', type=int, default=4)
    parser.add_argument('--kind', choices=['all','native','product'],default='all')
    args = parser.parse_args()
    args.scratch.mkdir(exist_ok=True, parents=True)
    started = time.monotonic()
    storage = icechunk.s3_storage(bucket='earthmover-icechunk-era5', prefix='icechunkV2', region='us-east-1', anonymous=True)
    session = icechunk.Repository.open(storage).readonly_session(snapshot_id=SNAPSHOT)
    group = zarr.open_group(session.store, mode='r', path='single/temporal')
    time_values = group['valid_time'][:]
    if not np.issubdtype(time_values.dtype, np.datetime64):
        units = group['valid_time'].attrs.get('units', '')
        unit, origin = units.split(' since ')
        time_values = np.datetime64(origin) + time_values.astype('timedelta64['+{'hours':'h','seconds':'s','days':'D'}[unit]+']')
    lower = int(np.searchsorted(time_values, np.datetime64('1960-01-01')))
    upper = int(np.searchsorted(time_values, np.datetime64('2026-01-02')))
    jobs, destinations = {}, []
    live_document = json.loads(args.live_points.read_text()) if args.live_points.exists() else {}
    live = live_document.get('stations',live_document)
    for station in json.loads(args.stations.read_text()):
        if not station.get('station'):
            continue
        row = int(np.floor((90-station['lat'])*4+.5))
        column = int(np.floor((station['lon']%360)*4+.5))%1440
        coordinates = [('native', row, column, lower)]
        selected = live.get(station['station'], live.get(station['slug']))
        if selected and selected.get('source') is not None:
            source = selected['source']
            coordinates.append(('product', source//1440, (source%1440+720)%1440,
                                int(np.searchsorted(time_values,np.datetime64('2015-12-30')))))
        for kind, row, column, first in coordinates:
            if args.kind != 'all' and kind != args.kind:
                continue
            path = args.scratch/f"era5_{station['station']}_{kind}.npz"
            if path.exists():
                cached = np.load(path)
                expected_coordinates = (90-row/4, (column/4+180)%360-180)
                if (float(cached['latitude']), float(cached['longitude'])) != expected_coordinates:
                    raise ValueError(f'{path}: cached coordinates differ from {expected_coordinates}; '
                                     'preserve the old file and rerun into a separate scratch folder')
                continue
            target = dict(station=station, kind=kind, row=row, column=column, first=first,
                          values=np.full(upper-first, np.nan, np.float32), path=path)
            destinations.append(target)
            for chunk in range(first//8736, (upper-1)//8736+1):
                jobs.setdefault((chunk,row//12,column//12),[]).append(target)
    print('ERA5 point jobs',len(jobs),'destinations',len(destinations),flush=True)

    def fetch(job):
        (chunk,row,column), targets = job
        first = chunk*8736
        last = min(len(time_values),first+8736)
        for attempt in range(4):
            try:
                values = group['t2m'][first:last,row*12:min(721,(row+1)*12),column*12:(column+1)*12]
                break
            except Exception:
                if attempt == 3:
                    raise
                time.sleep(attempt+1)
        for target in targets:
            lo, hi = max(target['first'],first), min(upper,last)
            target['values'][lo-target['first']:hi-target['first']] = values[lo-first:hi-first,target['row']%12,target['column']%12]-273.15
        return values.nbytes

    decoded = 0
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as pool:
        for completed, byte_count in enumerate(pool.map(fetch,jobs.items()),1):
            decoded += byte_count
            if completed%50 == 0:
                print('ERA5',completed,'/',len(jobs),'seconds',round(time.monotonic()-started,1),flush=True)
    for target in destinations:
        if not np.isfinite(target['values']).all():
            raise ValueError(target['path'])
        np.savez_compressed(target['path'],temperature_c=target['values'],
                            times=time_values[target['first']:upper],latitude=90-target['row']/4,
                            longitude=(target['column']/4+180)%360-180,snapshot=SNAPSHOT)
    (args.scratch/f'era5_run_{args.kind}.json').write_text(json.dumps(dict(snapshot=SNAPSHOT, jobs=len(jobs),
        decoded_bytes=decoded, seconds=time.monotonic()-started, paths=[str(t['path']) for t in destinations]),indent=2))


if __name__ == '__main__':
    main()
