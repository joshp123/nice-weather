"""Reduce actual assembled ERA5 days to native monthly air-temperature normals.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
Each date uses 24 local clock samples, interpolated for fractional time zones.
The 360 scored dates per chunk are exactly pipeline.chunk_dates, not a guessed
calendar-year range. No station observations enter this reduction.
"""
import argparse
import datetime as dt
import hashlib
import json
import multiprocessing
import pathlib
import sys
import time
import zoneinfo

import numpy as np

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1] / 'climate-v6'))
import pipeline
from static import POINT_DTYPE


def file_sha256(path):
    digest = hashlib.sha256()
    with pathlib.Path(path).open('rb') as stream:
        for block in iter(lambda: stream.read(8*1024*1024), b''):
            digest.update(block)
    return digest.hexdigest()


def tile_point_ids(points, name):
    row, column = int(name[1:3]), int(name[4:7])
    native_rows = points['fine']//1440
    native_columns = (points['fine']%1440+720)%1440
    return np.flatnonzero((native_rows//12 == row) & (native_columns//12 == column))


def local_hour_indices(chunk, zone_name, hours):
    epoch, dates = pipeline.chunk_dates(chunk)
    zone = zoneinfo.ZoneInfo(str(zone_name))
    utc = np.array([[(dt.datetime.combine(date, dt.time(hour), zone)
        .astimezone(dt.timezone.utc)-epoch).total_seconds()/3600
        for hour in hours] for date in dates])
    low = np.floor(utc).astype(int)
    return dates, low, utc-low


def sample_local(field, low, fraction, point_ids):
    return (field[low[:,:,None], point_ids]*(1-fraction[:,:,None]) +
            field[low[:,:,None]+1, point_ids]*fraction[:,:,None])


def reduce_tile(job):
    root, output, chunks, name = job
    root, output = pathlib.Path(root), pathlib.Path(output)
    destination = output/name
    if destination.exists():
        return name, False
    points = np.fromfile(root/'v6/points.bin', dtype=POINT_DTYPE, offset=4)
    selected = tile_point_ids(points, name)
    fine = points['fine'][selected]
    rows, columns = (fine//1440)%12, ((fine%1440+720)%1440)%12
    zones = np.load(root/'climate-v6-work/timezones.npy')[selected]
    sums = np.zeros((len(selected),12,3), np.float64)
    days = np.zeros(12, np.int32)
    for chunk in chunks:
        with np.load(root/f'era5/chunk{chunk}'/name) as raw:
            temperature = raw['t2m'][:,rows,columns].astype(np.float32)
        _, dates = pipeline.chunk_dates(chunk)
        months = np.array([date.month for date in dates])
        days += np.bincount(months-1, minlength=12)
        for zone in np.unique(zones):
            ids = np.flatnonzero(zones == zone)
            _, low, fraction = local_hour_indices(chunk, zone, range(24))
            air = sample_local(temperature, low, fraction, ids)
            daily = np.stack((air.max(1), air.min(1), air.mean(1)), axis=-1)
            for month in range(1,13):
                sums[ids,month-1] += daily[months == month].sum(0)
    temporary = destination.with_suffix('.partial.npz')
    np.savez_compressed(temporary, point_ids=selected, sums=sums, days=days)
    temporary.replace(destination)
    return name, True


def build_normals(root, output, chunks, workers=4):
    root, output = pathlib.Path(root), pathlib.Path(output)
    output.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    points = np.fromfile(root/'v6/points.bin', dtype=POINT_DTYPE, offset=4)
    configuration = dict(chunks=sorted(chunks), points_sha256=file_sha256(root/'v6/points.bin'),
        timezones_sha256=file_sha256(root/'climate-v6-work/timezones.npy'),
        reducer_sha256=file_sha256(__file__),
        definition='daily high/low/mean of 24 local clock hours; actual scored chunk dates')
    manifest = output/'configuration.json'
    if manifest.exists() and json.loads(manifest.read_text()) != configuration:
        raise ValueError('Normals cache belongs to a different chunk set or reducer; choose a new folder')
    manifest.write_text(json.dumps(configuration, indent=2)+'\n')
    names = sorted({f'r{row:02d}c{column:03d}.npz' for row,column in
        zip(points['fine']//1440//12, ((points['fine']%1440+720)%1440)//12)})
    jobs = [(str(root),str(output),chunks,name) for name in names]
    with multiprocessing.Pool(workers) as pool:
        for number, (name, changed) in enumerate(pool.imap_unordered(reduce_tile,jobs),1):
            if number%100 == 0 or number == len(jobs):
                print(f'native normals {number}/{len(jobs)} {time.monotonic()-started:.1f}s',flush=True)
    values = np.zeros((len(points),12,3), np.float64)
    expected_days = sum((np.bincount(np.array([d.month for d in pipeline.chunk_dates(chunk)[1]])-1,
                                   minlength=12) for chunk in chunks), np.zeros(12,dtype=int))
    for name in names:
        with np.load(output/name) as saved:
            if not np.array_equal(saved['days'], expected_days):
                raise ValueError(f'Incomplete dates: {name}')
            values[saved['point_ids']] = saved['sums']/saved['days'][None,:,None]
    np.savez_compressed(output/'normals.npz', fine=points['fine'], chunks=chunks,
        high_c=values[:,:,0].astype(np.float32), low_c=values[:,:,1].astype(np.float32),
        mean_c=values[:,:,2].astype(np.float32), days=expected_days)
    (output/'timing.json').write_text(json.dumps(dict(seconds=time.monotonic()-started,
        tiles=len(names),chunks=chunks),indent=2)+'\n')
    return output/'normals.npz'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--native-root',type=pathlib.Path,required=True)
    parser.add_argument('--output',type=pathlib.Path,required=True)
    parser.add_argument('--meta',type=pathlib.Path,help='Assembled climate/meta.json determines the chunk set')
    parser.add_argument('--chunks',nargs='+',type=int)
    parser.add_argument('--workers',type=int,default=4)
    args = parser.parse_args()
    chunks = json.loads(args.meta.read_text())['chunks'] if args.meta else args.chunks
    if not chunks:
        parser.error('Specify --meta or --chunks; never infer years from filenames alone')
    print(build_normals(args.native_root,args.output,chunks,args.workers),flush=True)


if __name__ == '__main__':
    main()
