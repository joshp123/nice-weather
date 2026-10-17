#!/usr/bin/env -S uv run --quiet --script
# /// script
# dependencies = ["numpy", "pillow"]
# ///
# Written by AI on 2026.10.17 by model gpt-6.1-sol.
"""Usage: default_scores.py <v6 folder> [--images <output folder>].

Write monthly typical-day scores.bin, or typical-day score PNGs and scores.bin.
Periods count calendar days. Image values are floored after finding the typical day.
The image option writes only to the specified output folder.
"""
import importlib.util
import pathlib
import sys
import time

import numpy as np
from PIL import Image

REFERENCE_PATH = pathlib.Path(__file__).resolve().parents[2] / 'research/taste/nice_v6.py'
spec = importlib.util.spec_from_file_location('nice_v6', REFERENCE_PATH)
reference = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reference)

SHIFT_MINIMUM, SHIFT_STEP, SHIFT_LEVELS = -15, 3, 11
FINE_WIDTH, FINE_HEIGHT = 1440, 721


def nearest_land_ordinals(cells):
    """Match data.ts's wrapped Euclidean nearest cell, including deterministic ties."""
    land = np.full((180, 360), -1, dtype=np.int32)
    land[cells[:, 0], cells[:, 1]] = np.arange(len(cells))
    row_nearest = np.full_like(land, -1)
    row_distance = np.full((180, 360), np.inf)
    for row in range(180):
        nearest = -1080
        for column in range(-360, 720):
            wrapped = column % 360
            if land[row, wrapped] >= 0:
                nearest = column
            if 0 <= column < 360 and nearest != -1080:
                row_nearest[row, column] = land[row, nearest % 360]
                row_distance[row, column] = (column - nearest) ** 2
        nearest = 1080
        for column in range(719, -361, -1):
            wrapped = column % 360
            if land[row, wrapped] >= 0:
                nearest = column
            if 0 <= column < 360 and nearest != 1080 and (column - nearest) ** 2 < row_distance[row, column]:
                row_nearest[row, column] = land[row, nearest % 360]
                row_distance[row, column] = (column - nearest) ** 2
    ordinals = np.empty_like(land)
    for row in range(180):
        source_rows = np.argmin((np.arange(180)[:, None] - row) ** 2 + row_distance, axis=0)
        ordinals[row] = row_nearest[source_rows, np.arange(360)]
    return ordinals


def cell_level_day_scores(records):
    """Score the compact eight-day sample at the GPU's eleven uniform felt shifts."""
    sun_hours = np.arange(11)
    sun_lower = sun_hours // 2
    sun_upper = np.minimum(sun_lower + 1, 5)
    sun_fraction = (sun_hours % 2) / 2
    sun = records[:, :, sun_lower] / 3 - 30
    sun += (records[:, :, sun_upper] / 3 - 30 - sun) * sun_fraction
    shade_hours = np.arange(12)
    shade_lower = np.maximum(0, (shade_hours - 2) // 3)
    shade_upper = np.minimum(shade_lower + 1, 3)
    shade_fraction = np.maximum(0, shade_hours - 2 - shade_lower * 3) / 3
    shade = records[:, :, 6 + shade_lower] / 3 - 30
    shade += (records[:, :, 6 + shade_upper] / 3 - 30 - shade) * shade_fraction
    daylight = np.maximum(records[:, :, 14], 1)
    factors = (reference.RAIN_FACTOR((records[:, :, 11] / 50) ** 2)
               * reference.WIND_FACTOR(records[:, :, 12] / 2)
               * reference.HUMIDITY_FACTOR(records[:, :, 10] / 3 - 30)
               * reference.SUN_FACTOR(records[:, :, 13] / daylight))
    in_daylight = sun_hours < daylight[:, :, None]
    scores = np.empty((len(records), SHIFT_LEVELS, records.shape[1]))
    for level in range(SHIFT_LEVELS):
        offset = SHIFT_MINIMUM + level * SHIFT_STEP
        hourly = np.minimum(reference.COLD_GRADE(sun + offset), reference.HEAT_GRADE(shade[:, :, :11] + offset))
        evening = np.minimum(reference.EVENING_GRADE(shade[:, :, 11] + offset), reference.HEAT_GRADE(shade[:, :, 11] + offset))
        warmth = ((hourly * in_daylight).sum(axis=2) + reference.EVENING_HOURS_WORTH * evening) / (daylight + reference.EVENING_HOURS_WORTH)
        scores[:, level] = warmth * factors
    return scores


def cell_level_scores(records):
    return cell_level_day_scores(records).mean(axis=2)


def fine_scores(level_scores, offsets, ordinals):
    """Owning cell and selected fine point's own felt shift, matching the browser."""
    indices = np.flatnonzero(offsets != 255)
    rows, columns = np.divmod(indices, FINE_WIDTH)
    cells = ordinals[np.minimum(rows // 4, 179), columns // 4]
    level = np.clip(((offsets[indices].astype(float) - 128) / 10 - SHIFT_MINIMUM) / SHIFT_STEP, 0, SHIFT_LEVELS - 1)
    lower = np.minimum(np.floor(level).astype(int), SHIFT_LEVELS - 2)
    fraction = level - lower
    if level_scores.ndim == 3:
        fraction = fraction[:, None]
    return indices, level_scores[cells, lower] * (1 - fraction) + level_scores[cells, lower + 1] * fraction


MONTH_DAYS = np.array([31,28,31,30,31,30,31,31,30,31,30,31])
THRESHOLDS = np.array([0,25,40,55,70,85,100])


def typical_scores(shares):
    """Same threshold crossing as sampling.ts and the GPU resolve pass."""
    result = np.zeros(shares.shape[:-1])
    for threshold in range(6):
        low, high = shares[..., threshold], shares[..., threshold + 1]
        fraction = np.divide(low - .5, low - high, out=np.zeros_like(low), where=low != high)
        score = 85 + (low - .5) * 30 if threshold == 5 else THRESHOLDS[threshold] + (THRESHOLDS[threshold + 1] - THRESHOLDS[threshold]) * fraction
        result = np.where(low >= .5 - 1e-9, np.maximum(THRESHOLDS[threshold], score), result)
    return result


def write_default_scores(folder, image_folder=None):
    started = time.monotonic()
    cells = np.fromfile(folder / 'cells.bin', dtype='<u2').reshape(-1, 2)
    ordinals = nearest_land_ordinals(cells)
    output = np.full((FINE_WIDTH * FINE_HEIGHT, 12), 255, dtype=np.uint8)
    monthly_shares = np.zeros((FINE_WIDTH * FINE_HEIGHT, 12, 7), dtype=np.float32) if image_folder else None
    for month in range(1, 13):
        records = np.fromfile(folder / f'climate/month-{month:02d}.bin', dtype=np.uint8).reshape(len(cells), 8, 15)
        with Image.open(folder / f'climate/offsets-{month:02d}.png') as image:
            if image.mode != 'L' or image.size != (FINE_WIDTH, FINE_HEIGHT):
                raise ValueError(f'offsets-{month:02d}.png must be 1440 × 721, 8-bit greyscale')
            offsets = np.asarray(image).reshape(-1)
        indices, days = fine_scores(cell_level_day_scores(records), offsets, ordinals)
        shares = (days[:, :, None] >= THRESHOLDS).mean(axis=1)
        scores = typical_scores(shares)
        output[indices, month - 1] = np.floor(scores).astype(np.uint8)
        if monthly_shares is not None:
            monthly_shares[indices, month - 1] = shares
        print(f'Month {month:02d}: {len(indices):,} land points', flush=True)
    if image_folder:
        image_folder.mkdir(parents=True, exist_ok=True)
        words = 'january february march april may june july august september october november december'.split()
        periods = {word: [index] for index, word in enumerate(words)}
        for start in range(12):
            for length in range(2, 12):
                end = (start + length - 1) % 12
                periods[f'{words[start][:3]}-{words[end][:3]}'] = [(start + step) % 12 for step in range(length)]
        periods.update({'dec-feb': [11, 0, 1], 'mar-may': [2, 3, 4], 'jun-aug': [5, 6, 7], 'sep-nov': [8, 9, 10], 'year': list(range(12))})
        for word, months in periods.items():
            values = np.full(FINE_WIDTH * FINE_HEIGHT, 255, dtype=np.uint8)
            valid = (output[:, months] != 255).all(axis=1)
            weights = MONTH_DAYS[months] / MONTH_DAYS[months].sum()
            shares = np.einsum("pmt,m->pt", monthly_shares[valid][:, months], weights)
            values[valid] = np.floor(typical_scores(shares)).astype(np.uint8)
            Image.fromarray(values.reshape(FINE_HEIGHT, FINE_WIDTH)).save(image_folder / f'scores-{word}.png', optimize=True)
        output.tofile(image_folder / 'scores.bin')
        print(f'{image_folder}: {len(periods)} images in {time.monotonic() - started:.1f} s', flush=True)
    else:
        path = folder / 'climate/scores.bin'
        output.tofile(path)
        print(f'{path}: {path.stat().st_size:,} bytes in {time.monotonic() - started:.1f} s', flush=True)


def write_forecast_scores(folder, run_folder, image_folder):
    import datetime
    import json
    cells = np.fromfile(folder / 'cells.bin', dtype='<u2').reshape(-1, 2)
    ordinals = nearest_land_ordinals(cells)
    records = np.fromfile(run_folder / 'days.bin', dtype=np.uint8).reshape(len(cells), 14, 15)
    metadata = json.loads((run_folder / 'meta.json').read_text()) if (run_folder / 'meta.json').exists() else json.loads((folder / 'forecast/current.json').read_text())
    first_date = datetime.date.fromisoformat(metadata['firstDate'])
    months = [(first_date + datetime.timedelta(days=day)).month for day in range(14)]
    levels = cell_level_day_scores(records)
    daily = np.full((FINE_WIDTH * FINE_HEIGHT, 14), np.nan, dtype=np.float32)
    for month in sorted(set(months)):
        offsets = np.asarray(Image.open(folder / f'climate/offsets-{month:02d}.png')).reshape(-1)
        indices, scores = fine_scores(levels, offsets, ordinals)
        for day, day_month in enumerate(months):
            if day_month == month:
                daily[indices, day] = scores[:, day]
    image_folder.mkdir(parents=True, exist_ok=True)
    for day in range(14):
        values = np.full(FINE_WIDTH * FINE_HEIGHT, 255, dtype=np.uint8)
        valid = np.isfinite(daily[:, day])
        values[valid] = np.floor(daily[valid, day]).astype(np.uint8)
        Image.fromarray(values.reshape(FINE_HEIGHT, FINE_WIDTH)).save(image_folder / f'scores-day-{day:02d}.png', optimize=True)
    valid = np.isfinite(daily).all(axis=1)
    shares = (daily[valid, :, None] >= THRESHOLDS).mean(axis=1)
    values = np.full(FINE_WIDTH * FINE_HEIGHT, 255, dtype=np.uint8)
    values[valid] = np.floor(typical_scores(shares)).astype(np.uint8)
    Image.fromarray(values.reshape(FINE_HEIGHT, FINE_WIDTH)).save(image_folder / 'scores-all.png', optimize=True)
    print(f'{image_folder}: 15 forecast images', flush=True)


if __name__ == '__main__':
    import argparse
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('folder', type=pathlib.Path)
    parser.add_argument('--images', type=pathlib.Path)
    parser.add_argument('--forecast', type=pathlib.Path, help='Forecast run folder; requires --images')
    arguments = parser.parse_args()
    if arguments.forecast:
        if not arguments.images:
            parser.error('--forecast requires --images')
        write_forecast_scores(arguments.folder, arguments.forecast, arguments.images)
    else:
        write_default_scores(arguments.folder, arguments.images)
