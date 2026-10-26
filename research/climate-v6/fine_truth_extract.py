"""Extract full-period native score weather beside the raw NAS tiles.

Written by AI on 2026.10.19 by model GPT-6.1 Sol.
Independent measurement files only; the production reducers are imported read-only.
"""
import argparse
import datetime as dt
import json
import os
import pathlib
import sys
import time

os.environ.setdefault('OMP_NUM_THREADS', '1')
os.environ.setdefault('OPENBLAS_NUM_THREADS', '1')
import numpy as np


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', type=pathlib.Path, required=True)
    parser.add_argument('--inputs', type=pathlib.Path, required=True)
    parser.add_argument('--raw-root', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args()
    sys.path[:0] = [str(args.repo/'research/fine'), str(args.repo/'research/climate-v6')]
    import worldwide_replay as replay
    pipeline = replay.climate_pipeline()
    inputs = json.loads(args.inputs.read_text())
    groups = {}
    for item in inputs['native']:
        row, column = divmod(item['fine'], 1440)
        name = f'r{row//12:02d}c{((column+720)%1440)//12:03d}.npz'
        groups.setdefault(name, []).append(item)
    args.output.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    for name, members in groups.items():
        pending = [item for item in members if not (args.output/(item['key'].replace('/', '_')+'.bin')).exists()]
        if not pending:
            continue
        tile_started = time.monotonic()
        records = {item['key']: [] for item in pending}
        raw_bytes = 0
        for chunk in range(61, 86):
            path = args.raw_root/f'chunk{chunk}'/name
            raw_bytes += path.stat().st_size
            with np.load(path) as saved:
                fields = {key: saved[key].astype(np.float32) for key in ('t2m', 'd2m', 'u10', 'v10', 'tp', 'fdir')}
            wind = np.hypot(fields['u10'], fields['v10'])*3.6
            for item in pending:
                row, column = divmod(item['fine'], 1440)
                r, c = row%12, ((column+720)%1440)%12
                epoch, dates, indices, fractions, utc, offsets = replay.sampling(chunk, item['zone'])
                def sample(field, accumulation=False):
                    base = indices+int(accumulation)
                    return field[base, r, c]*(1-fractions)+field[base+1, r, c]*fractions
                air24 = sample(fields['t2m'])
                air, dew = air24[:, 10:22], sample(fields['d2m'])[:, 10:22]
                sampled_wind = sample(wind)[:, 10:22]
                rain, direct = sample(fields['tp'], True)[:, 10:22], sample(fields['fdir'], True)[:, 10:22]
                latitude, longitude = 90-row*.25, -180+column*.25
                sun, shade, _, dni = replay.apparent_temperatures(epoch, utc, np.array([latitude]), np.array([longitude]), air[:, :, None], dew[:, :, None], sampled_wind[:, :, None], direct[:, :, None])
                sun, shade, dni = sun[:, :, 0], shade[:, :, 0], dni[:, :, 0]
                numbers = np.array([date.timetuple().tm_yday for date in dates])
                _, equation, declination = pipeline.solar(numbers, 12, latitude, longitude)
                angle = np.deg2rad(latitude)
                argument = np.cos(np.deg2rad(90.833))/(np.cos(angle)*np.cos(declination))-np.tan(angle)*np.tan(declination)
                sunset = (720-4*longitude-equation+4*np.rad2deg(np.arccos(np.clip(argument, -1, 1))))/60
                daylight = np.clip(np.ceil(sunset+offsets-24*np.rint((offsets-longitude/15)/24))-10, 1, 11)
                daylight = np.where(argument >= 1, 1, np.where(argument <= -1, 11, daylight))
                mask = np.arange(11) < daylight[:, None]
                suffix = np.column_stack((air24.max(1), air24.min(1), rain[:, :11].sum(1), sampled_wind.mean(1), ((dni[:, :11] >= 300)&mask).sum(1), daylight))
                records[item['key']].append(np.column_stack((air, dew, sun, shade, suffix)).astype('<f4'))
            del fields, wind
        for item in pending:
            array = np.concatenate(records[item['key']])
            if array.shape != (9000, 54) or not np.isfinite(array).all():
                raise ValueError(f'Incomplete ground truth: {item["key"]}: {array.shape}')
            target = args.output/(item['key'].replace('/', '_')+'.bin')
            temporary = target.with_suffix('.partial')
            array.tofile(temporary)
            temporary.replace(target)
        receipt = dict(tile=name, nativePoints=len(pending), rawBytes=raw_bytes, seconds=time.monotonic()-tile_started, elapsedSeconds=time.monotonic()-started)
        with (args.output/'progress.jsonl').open('a') as log:
            log.write(json.dumps(receipt)+'\n')
        print(json.dumps(receipt), flush=True)
    (args.output/'complete.json').write_text(json.dumps(dict(nativePoints=len(inputs['native']), chunks=list(range(61, 86)), days=9000, seconds=time.monotonic()-started, completedUTC=dt.datetime.now(dt.timezone.utc).isoformat()), indent=2)+'\n')


if __name__ == '__main__':
    main()
