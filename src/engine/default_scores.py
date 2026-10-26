#!/usr/bin/env -S uv run --quiet --script
# /// script
# dependencies = ["numpy", "pillow"]
# ///
# Written by AI on 2026.10.17 by model gpt-6.1-sol.
# Reason images added by AI on 2026.10.26 by model GPT-6.
"""Usage: default_scores.py <v6 folder> [--images <output folder>] [--reasons-only].

Write monthly typical-day scores.bin, or typical-day score PNGs and scores.bin.
Periods count calendar days. Image values are floored after finding the typical day.
Each score image has a matching reason image. --reasons-only leaves scores untouched.
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


def nearest_temperature_offsets(offsets):
    """Extend measured offsets to the wrapped drawing grid; preserve the Antarctic cutoff."""
    offsets = offsets.reshape(FINE_HEIGHT, FINE_WIDTH)
    nearest_in_row = np.full(offsets.shape, -1, dtype=np.int32)
    row_distance = np.full(offsets.shape, np.inf)
    rows = np.arange(FINE_HEIGHT)
    nearest = np.full(FINE_HEIGHT, -3 * FINE_WIDTH)
    for column in range(-FINE_WIDTH, 2 * FINE_WIDTH):
        nearest = np.where(offsets[:, column % FINE_WIDTH] != 255, column, nearest)
        if 0 <= column < FINE_WIDTH:
            valid = nearest != -3 * FINE_WIDTH
            nearest_in_row[valid, column] = rows[valid] * FINE_WIDTH + nearest[valid] % FINE_WIDTH
            row_distance[valid, column] = (column - nearest[valid]) ** 2
    nearest.fill(3 * FINE_WIDTH)
    for column in range(2 * FINE_WIDTH - 1, -FINE_WIDTH - 1, -1):
        nearest = np.where(offsets[:, column % FINE_WIDTH] != 255, column, nearest)
        if 0 <= column < FINE_WIDTH:
            valid = (nearest != 3 * FINE_WIDTH) & ((column - nearest) ** 2 < row_distance[:, column])
            nearest_in_row[valid, column] = rows[valid] * FINE_WIDTH + nearest[valid] % FINE_WIDTH
            row_distance[valid, column] = (column - nearest[valid]) ** 2
    source_rows = np.empty(FINE_HEIGHT, dtype=int)
    boundaries = np.empty(FINE_HEIGHT + 1)
    result = offsets.copy().reshape(-1)
    for column in range(FINE_WIDTH):
        last = -1
        for row in np.flatnonzero(np.isfinite(row_distance[:, column])):
            boundary = -np.inf
            while last >= 0:
                previous = source_rows[last]
                boundary = (row_distance[row, column] + row * row - row_distance[previous, column] - previous * previous) / (2 * (row - previous))
                if boundary > boundaries[last]:
                    break
                last -= 1
            last += 1
            source_rows[last] = row
            boundaries[last] = -np.inf if last == 0 else boundary
            boundaries[last + 1] = np.inf
        nearest_rows = source_rows[np.searchsorted(boundaries[1:last + 1], np.arange(601), side='left')]
        result[np.arange(601) * FINE_WIDTH + column] = offsets.reshape(-1)[nearest_in_row[nearest_rows, column]]
    result[601 * FINE_WIDTH:] = 255
    return result


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


def cell_level_day_scores(records, include_reasons=False):
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
    reasons = np.empty(scores.shape, dtype=np.uint8) if include_reasons else None
    for level in range(SHIFT_LEVELS):
        offset = SHIFT_MINIMUM + level * SHIFT_STEP
        cold = reference.COLD_GRADE(sun + offset)
        heat = reference.HEAT_GRADE(shade[:, :, :11] + offset)
        evening_cold = reference.EVENING_GRADE(shade[:, :, 11] + offset)
        evening_heat = reference.HEAT_GRADE(shade[:, :, 11] + offset)
        hourly = np.minimum(cold, heat)
        evening = np.minimum(evening_cold, evening_heat)
        warmth = ((hourly * in_daylight).sum(axis=2) + reference.EVENING_HOURS_WORTH * evening) / (daylight + reference.EVENING_HOURS_WORTH)
        scores[:, level] = warmth * factors
        if include_reasons:
            lost_to_cold = (np.where(cold <= heat, 100 - cold, 0) * in_daylight).sum(axis=2)
            lost_to_heat = (np.where(cold > heat, 100 - heat, 0) * in_daylight).sum(axis=2)
            lost_to_cold += np.where(evening_cold <= evening_heat, 100 - evening_cold, 0) * reference.EVENING_HOURS_WORTH
            lost_to_heat += np.where(evening_cold > evening_heat, 100 - evening_heat, 0) * reference.EVENING_HOURS_WORTH
            leading = (lost_to_heat > lost_to_cold).astype(np.uint8)
            smallest = warmth / 100
            multipliers = (reference.RAIN_FACTOR((records[:, :, 11] / 50) ** 2),
                           reference.WIND_FACTOR(records[:, :, 12] / 2),
                           reference.HUMIDITY_FACTOR(records[:, :, 10] / 3 - 30),
                           reference.SUN_FACTOR(records[:, :, 13] / daylight))
            for reason, multiplier in enumerate(multipliers, start=2):
                leading = np.where(multiplier < smallest, reason, leading)
                smallest = np.minimum(smallest, multiplier)
            reasons[:, level] = leading
    return (scores, reasons) if include_reasons else scores


def cell_level_scores(records):
    return cell_level_day_scores(records).mean(axis=2)


def fine_scores(level_scores, offsets, ordinals, level_reasons=None):
    """Nearest land cell and nearest measured felt shift, matching the browser."""
    offsets = nearest_temperature_offsets(offsets)
    indices = np.flatnonzero(offsets != 255)
    rows, columns = np.divmod(indices, FINE_WIDTH)
    cells = ordinals[np.minimum(rows // 4, 179), columns // 4]
    level = np.clip(((offsets[indices].astype(float) - 128) / 10 - SHIFT_MINIMUM) / SHIFT_STEP, 0, SHIFT_LEVELS - 1)
    lower = np.minimum(np.floor(level).astype(int), SHIFT_LEVELS - 2)
    fraction = level - lower
    reasons = level_reasons[cells, lower + (fraction >= .5)] if level_reasons is not None else None
    if level_scores.ndim == 3:
        fraction = fraction[:, None]
    scores = canonical_scores(level_scores[cells, lower] * (1 - fraction) + level_scores[cells, lower + 1] * fraction)
    return (indices, scores, reasons) if level_reasons is not None else (indices, scores)


MONTH_DAYS = np.array([31,28,31,30,31,30,31,31,30,31,30,31])
THRESHOLDS = np.array([minimum for minimum, _ in reversed(reference.BANDS)] + [100])


def canonical_scores(scores):
    return np.floor(scores * 10000 + .5) / 10000


def typical_scores(shares):
    """Same threshold crossing as sampling.ts and the GPU resolve pass."""
    result = np.zeros(shares.shape[:-1])
    for threshold in range(6):
        low, high = shares[..., threshold], shares[..., threshold + 1]
        fraction = np.divide(low - .5, low - high, out=np.zeros_like(low), where=low != high)
        score = 85 + (low - .5) * 30 if threshold == 5 else THRESHOLDS[threshold] + (THRESHOLDS[threshold + 1] - THRESHOLDS[threshold]) * fraction
        result = np.where(low >= .5 - 1e-9, np.maximum(THRESHOLDS[threshold], score), result)
    return canonical_scores(result)


def reason_bytes(scores, points_lost):
    leading = np.argmax(points_lost, axis=-1).astype(np.uint8)
    severity = np.where(scores >= 55, 0, np.where(scores >= 40, 1, np.where(scores >= reference.AWFUL, 2, 3)))
    return (leading * 4 + severity).astype(np.uint8)


def write_default_scores(folder, image_folder=None, reasons_only=False):
    started = time.monotonic()
    cells = np.fromfile(folder / 'cells.bin', dtype='<u2').reshape(-1, 2)
    ordinals = nearest_land_ordinals(cells)
    output = np.full((FINE_WIDTH * FINE_HEIGHT, 12), 255, dtype=np.uint8)
    monthly_shares = np.zeros((FINE_WIDTH * FINE_HEIGHT, 12, 7), dtype=np.float32) if image_folder else None
    monthly_points_lost = np.zeros((FINE_WIDTH * FINE_HEIGHT, 12, 6), dtype=np.float32) if image_folder else None
    for month in range(1, 13):
        records = np.fromfile(folder / f'climate/month-{month:02d}.bin', dtype=np.uint8).reshape(len(cells), 8, 15)
        with Image.open(folder / f'climate/offsets-{month:02d}.png') as image:
            if image.mode != 'L' or image.size != (FINE_WIDTH, FINE_HEIGHT):
                raise ValueError(f'offsets-{month:02d}.png must be 1440 × 721, 8-bit greyscale')
            offsets = np.asarray(image).reshape(-1)
        if image_folder:
            level_scores, level_reasons = cell_level_day_scores(records, include_reasons=True)
            indices, days, reasons = fine_scores(level_scores, offsets, ordinals, level_reasons)
        else:
            indices, days = fine_scores(cell_level_day_scores(records), offsets, ordinals)
        shares = (days[:, :, None] >= THRESHOLDS).mean(axis=1)
        scores = typical_scores(shares)
        output[indices, month - 1] = np.floor(scores).astype(np.uint8)
        if monthly_shares is not None:
            monthly_shares[indices, month - 1] = shares
            for reason in range(6):
                monthly_points_lost[indices, month - 1, reason] = (np.maximum(0, 100 - days) * (reasons == reason)).mean(axis=1)
        print(f'Month {month:02d}: {len(indices):,} land points', flush=True)
    if image_folder:
        image_folder.mkdir(parents=True, exist_ok=True)
        words = 'january february march april may june july august september october november december'.split()
        periods = {word: [index] for index, word in enumerate(words)}
        for start in range(12):
            for length in range(2, 13):
                end = (start + length - 1) % 12
                periods[f'{words[start][:3]}-{words[end][:3]}'] = [(start + step) % 12 for step in range(length)]
        periods.update({'dec-feb': [11, 0, 1], 'mar-may': [2, 3, 4], 'jun-aug': [5, 6, 7], 'sep-nov': [8, 9, 10], 'year': list(range(12))})
        image_bytes = 0
        reason_image_bytes = 0
        for word, months in periods.items():
            values = np.full(FINE_WIDTH * FINE_HEIGHT, 255, dtype=np.uint8)
            valid = (output[:, months] != 255).all(axis=1)
            weights = MONTH_DAYS[months] / MONTH_DAYS[months].sum()
            shares = np.einsum("pmt,m->pt", monthly_shares[valid][:, months], weights)
            period_scores = typical_scores(shares)
            if not reasons_only:
                values[valid] = np.floor(period_scores).astype(np.uint8)
                image_path = image_folder / f'scores-typical-day-{word}.png'
                Image.fromarray(values.reshape(FINE_HEIGHT, FINE_WIDTH)).save(image_path, optimize=True)
                image_bytes += image_path.stat().st_size
            points_lost = np.einsum("pmr,m->pr", monthly_points_lost[valid][:, months], weights)
            values[valid] = reason_bytes(period_scores, points_lost)
            image_path = image_folder / f'reasons-typical-day-{word}.png'
            Image.fromarray(values.reshape(FINE_HEIGHT, FINE_WIDTH)).save(image_path, optimize=True)
            reason_image_bytes += image_path.stat().st_size
        if not reasons_only:
            output.tofile(image_folder / 'scores.bin')
        print(f'{image_folder}: {len(periods)} reason images, {reason_image_bytes:,} bytes; {image_bytes:,} score-image bytes written; {time.monotonic() - started:.1f} s', flush=True)
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
        Image.fromarray(values.reshape(FINE_HEIGHT, FINE_WIDTH)).save(image_folder / f'scores-typical-day-day-{day:02d}.png', optimize=True)
    valid = np.isfinite(daily).all(axis=1)
    shares = (daily[valid, :, None] >= THRESHOLDS).mean(axis=1)
    values = np.full(FINE_WIDTH * FINE_HEIGHT, 255, dtype=np.uint8)
    values[valid] = np.floor(typical_scores(shares)).astype(np.uint8)
    Image.fromarray(values.reshape(FINE_HEIGHT, FINE_WIDTH)).save(image_folder / 'scores-typical-day-all.png', optimize=True)
    print(f'{image_folder}: 15 forecast images', flush=True)


if __name__ == '__main__':
    import argparse
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('folder', type=pathlib.Path)
    parser.add_argument('--images', type=pathlib.Path)
    parser.add_argument('--forecast', type=pathlib.Path, help='Forecast run folder; requires --images')
    parser.add_argument('--reasons-only', action='store_true', help='Write climate reason images without touching score files; requires --images')
    arguments = parser.parse_args()
    if arguments.reasons_only and (not arguments.images or arguments.forecast):
        parser.error('--reasons-only requires --images and does not take --forecast')
    if arguments.forecast:
        if not arguments.images:
            parser.error('--forecast requires --images')
        write_forecast_scores(arguments.folder, arguments.forecast, arguments.images)
    else:
        write_default_scores(arguments.folder, arguments.images, arguments.reasons_only)
