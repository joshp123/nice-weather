#!/usr/bin/env python3
# Written by AI on 2026.10.19 by model GPT-6.1 Sol.
"""Recover retained all-day features, then count default-taste days without sampling.

Run with the same NumPy/Pillow environment as pipeline.py. Both commands resume:
  default_all_days.py cache --root DATA --cache CACHE
  default_all_days.py counts --cache CACHE --climate OLD_CLIMATE --output NEW_CLIMATE
"""
import argparse
import importlib.util
import json
import os
import pathlib
import time

import numpy as np
from PIL import Image

import pipeline
from static import POINT_DTYPE

SCORER_PATH = pathlib.Path(__file__).parents[2] / 'src/engine/default_scores.py'
spec = importlib.util.spec_from_file_location('default_scores', SCORER_PATH)
default_scores = importlib.util.module_from_spec(spec)
spec.loader.exec_module(default_scores)
THRESHOLDS = default_scores.THRESHOLDS


def atomic_json(path, value):
    temporary = path.with_suffix('.partial.json')
    temporary.write_text(json.dumps(value, indent=2) + '\n')
    temporary.replace(path)


def build_cache(root, cache):
    cache.mkdir(parents=True, exist_ok=True)
    for name in ('cells.bin', 'points.bin'):
        destination = cache / name
        if not destination.exists():
            os.link(root / 'v6' / name, destination)
    cells = np.fromfile(cache / 'cells.bin', dtype='<u2').reshape(-1, 2)
    points = np.fromfile(cache / 'points.bin', dtype=POINT_DTYPE, offset=4)
    expected = {f'r{row:02d}c{column:03d}.npz' for row, column in zip(
        points['fine'] // 1440 // 12, ((points['fine'] % 1440 + 720) % 1440) // 12)}
    started = time.monotonic()
    for folder in sorted((root / 'climate-v6-work').glob('chunk*'), key=lambda path: int(path.name[5:])):
        target = cache / folder.name
        if (target / 'complete.json').exists():
            continue
        if not all((folder / name).exists() for name in expected):
            continue
        target.mkdir(exist_ok=True)
        chunk_started = time.monotonic()
        features, valid, dates = pipeline.aggregate_chunk_features(folder, points, len(cells), expected)
        records = pipeline.encode_records(features)
        for name, array in (('features', features), ('records', records), ('valid', valid),
                            ('dates', np.asarray(dates, dtype='datetime64[D]'))):
            temporary = target / f'{name}.partial.npy'
            np.save(temporary, array)
            temporary.replace(target / f'{name}.npy')
        receipt = {'chunk': int(folder.name[5:]), 'cells': len(cells), 'days': len(dates),
                   'allCellsValid': bool(valid.all()), 'seconds': time.monotonic() - chunk_started,
                   'bytes': sum(path.stat().st_size for path in target.glob('*.npy'))}
        atomic_json(target / 'complete.json', receipt)
        print(json.dumps(receipt), flush=True)
        del features, records
    receipts = [json.loads(path.read_text()) for path in sorted(cache.glob('chunk*/complete.json'))]
    atomic_json(cache / 'cache.json', {'chunks': [row['chunk'] for row in receipts],
                                     'seconds': time.monotonic() - started, 'receipts': receipts})


def count_scores(features, offset):
    """Use all hourly features; neither the 15-byte encoding nor shift interpolation."""
    daylight = np.rint(features[..., 27])
    mask = np.arange(11) < daylight[..., None]
    thermal = (np.sum(np.minimum(pipeline.taste.COLD_GRADE(features[..., :11] + offset),
                                pipeline.taste.HEAT_GRADE(features[..., 11:22] + offset)) * mask, axis=-1)
               + 2 * np.minimum(pipeline.taste.EVENING_GRADE(features[..., 22] + np.asarray(offset)[..., 0]),
                                pipeline.taste.HEAT_GRADE(features[..., 22] + np.asarray(offset)[..., 0]))) / (daylight + 2)
    score = (thermal * pipeline.taste.HUMIDITY_FACTOR(features[..., 23])
             * pipeline.taste.RAIN_FACTOR(features[..., 24]) * pipeline.taste.WIND_FACTOR(features[..., 25])
             * pipeline.taste.SUN_FACTOR(features[..., 26] / daylight))
    return default_scores.canonical_scores(score)


def write_counts(cache, climate, output, batch_size):
    output.mkdir(parents=True, exist_ok=True)
    cells = np.fromfile(cache / 'cells.bin', dtype='<u2').reshape(-1, 2)
    points = np.fromfile(cache / 'points.bin', dtype=POINT_DTYPE, offset=4)
    folders = sorted(cache.glob('chunk*/complete.json'), key=lambda path: int(path.parent.name[5:]))
    groups = []
    for month in range(1, 13):
        offsets = np.asarray(Image.open(climate / f'offsets-{month:02d}.png')).reshape(-1)[points['fine']]
        if (offsets == 255).any():
            raise ValueError('Every retained land point needs a temperature offset')
        keys, inverse = np.unique(points['cell'].astype(np.int64) * 256 + offsets, return_inverse=True)
        groups.append((keys // 256, (keys % 256 - 128) / 10, inverse))
    started = time.monotonic()
    partials = output / 'default-count-work'
    partials.mkdir(exist_ok=True)
    for receipt_path in folders:
        folder = receipt_path.parent
        target = partials / f'{folder.name}.npz'
        if target.exists():
            continue
        chunk_started = time.monotonic()
        features = np.load(folder / 'features.npy', mmap_mode='r')
        valid = np.load(folder / 'valid.npy')
        dates = np.load(folder / 'dates.npy')
        months = dates.astype('datetime64[M]').astype(int) % 12 + 1
        cell_counts = np.zeros((len(cells), 12, 7), dtype='<u2')
        weather_sums = np.zeros((len(cells), 12, 9), dtype=np.float64)
        days = np.zeros((len(cells), 12), dtype='<u2')
        point_counts = np.zeros((len(points), 12, 7), dtype='<u2')
        for month in range(1, 13):
            selection = months == month
            days[:, month - 1] = valid * selection.sum()
            group_cells, shifts, inverse = groups[month - 1]
            group_counts = np.zeros((len(group_cells), 7), dtype='<u2')
            for first in range(0, len(cells), batch_size):
                last = min(first + batch_size, len(cells))
                scores = count_scores(features[first:last][:, selection], np.zeros((last - first, 1, 1)))
                cell_counts[first:last, month - 1] = (scores[..., None] >= THRESHOLDS).sum(axis=1) * valid[first:last, None]
                full = features[first:last][:, selection]
                weather = np.stack((full[..., 4], full[..., 16], full[..., 22], full[..., 23],
                                    full[..., 24], full[..., 24] >= 1, full[..., 25],
                                    full[..., 26] / full[..., 27], full[..., 27]), axis=-1)
                weather_sums[first:last, month - 1] = weather.sum(axis=1) * valid[first:last, None]
            for first in range(0, len(group_cells), batch_size):
                last = min(first + batch_size, len(group_cells))
                scores = count_scores(features[group_cells[first:last]][:, selection], shifts[first:last, None, None])
                group_counts[first:last] = (scores[..., None] >= THRESHOLDS).sum(axis=1) * valid[group_cells[first:last], None]
            point_counts[:, month - 1] = group_counts[inverse]
        temporary = target.with_suffix('.partial.npz')
        np.savez_compressed(temporary, cell_counts=cell_counts, point_counts=point_counts, days=days,
                            weather_sums=weather_sums,
                            seconds=time.monotonic() - chunk_started)
        temporary.replace(target)
        print(json.dumps({'chunk': folder.name, 'seconds': time.monotonic() - chunk_started,
                          'pointOffsetGroups': sum(len(group[0]) for group in groups)}), flush=True)
    cell_counts = np.zeros((len(cells), 12, 7), dtype=np.uint32)
    point_counts = np.zeros((len(points), 12, 7), dtype=np.uint32)
    days = np.zeros((len(cells), 12), dtype=np.uint32)
    weather_sums = np.zeros((len(cells), 12, 9), dtype=np.float64)
    for receipt_path in folders:
        with np.load(partials / f'{receipt_path.parent.name}.npz') as data:
            cell_counts += data['cell_counts']; point_counts += data['point_counts']; days += data['days']
            weather_sums += data['weather_sums']
    if not np.all(days == days[0]):
        raise ValueError('Per-cell coverage differs: global month denominators would be incorrect')
    cell_counts.astype('<u2').tofile(output / 'default-cell-counts.bin')
    days[0].astype('<u2').tofile(output / 'default-month-days.bin')
    (weather_sums / days[..., None]).astype('<f4').tofile(output / 'default-cell-weather.bin')
    destination = output / 'default-point-counts.partial.bin'
    grid_counts = np.memmap(destination, dtype='<u2', mode='w+', shape=(721 * 1440, 12, 7))
    grid_counts[:] = 0
    grid_counts[points['fine']] = point_counts
    grid_counts.flush()
    del grid_counts
    destination.replace(output / 'default-point-counts.bin')
    atomic_json(output / 'default-counts.json', {
        'source': 'all retained hourly features; exact default taste at each published felt offset',
        'chunks': [int(path.parent.name[5:]) for path in folders], 'daysPerMonth': days[0].tolist(),
        'thresholds': THRESHOLDS.tolist(), 'countType': 'uint16 little endian',
        'cellShape': [len(cells), 12, 7], 'pointShape': [721 * 1440, 12, 7],
        'seconds': time.monotonic() - started, 'sampleDaysUsed': False})


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['cache', 'counts'])
    parser.add_argument('--root', type=pathlib.Path)
    parser.add_argument('--cache', type=pathlib.Path, required=True)
    parser.add_argument('--climate', type=pathlib.Path)
    parser.add_argument('--output', type=pathlib.Path)
    parser.add_argument('--batch-size', type=int, default=1024)
    arguments = parser.parse_args()
    if arguments.command == 'cache':
        build_cache(arguments.root, arguments.cache)
    else:
        write_counts(arguments.cache, arguments.climate, arguments.output, arguments.batch_size)
