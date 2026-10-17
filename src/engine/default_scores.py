#!/usr/bin/env -S uv run --quiet --script
# /// script
# dependencies = ["numpy", "pillow"]
# ///
# Written by AI on 2026.10.17 by model gpt-6.1-sol.
"""Usage: default_scores.py <v6 folder>. Write the browser's default fine-point monthly means."""
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


def cell_level_scores(records):
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
    scores = np.empty((len(records), SHIFT_LEVELS))
    for level in range(SHIFT_LEVELS):
        offset = SHIFT_MINIMUM + level * SHIFT_STEP
        hourly = np.minimum(reference.COLD_GRADE(sun + offset), reference.HEAT_GRADE(shade[:, :, :11] + offset))
        evening = np.minimum(reference.EVENING_GRADE(shade[:, :, 11] + offset), reference.HEAT_GRADE(shade[:, :, 11] + offset))
        warmth = ((hourly * in_daylight).sum(axis=2) + reference.EVENING_HOURS_WORTH * evening) / (daylight + reference.EVENING_HOURS_WORTH)
        scores[:, level] = (warmth * factors).mean(axis=1)
    return scores


def fine_scores(level_scores, offsets, ordinals):
    """Resolve each land point's own offset and four cell centres, without the map spline."""
    indices = np.flatnonzero(offsets != 255)
    rows, columns = np.divmod(indices, FINE_WIDTH)
    cell_x, cell_y = columns / 4 - 0.5, rows / 4 - 0.5
    base_x, base_y = np.floor(cell_x).astype(int), np.floor(cell_y).astype(int)
    fraction_x, fraction_y = cell_x - base_x, cell_y - base_y
    level = np.clip(((offsets[indices].astype(float) - 128) / 10 - SHIFT_MINIMUM) / SHIFT_STEP, 0, SHIFT_LEVELS - 1)
    lower = np.minimum(np.floor(level).astype(int), SHIFT_LEVELS - 2)
    fraction_level = level - lower
    scores = np.zeros(len(indices))
    for row in range(2):
        for column in range(2):
            cells = ordinals[np.clip(base_y + row, 0, 179), (base_x + column) % 360]
            spatial_weight = (fraction_x if column else 1 - fraction_x) * (fraction_y if row else 1 - fraction_y)
            for upper in range(2):
                weight = spatial_weight * (fraction_level if upper else 1 - fraction_level)
                scores += level_scores[cells, lower + upper] * weight
    return indices, scores


def write_default_scores(folder):
    started = time.monotonic()
    cells = np.fromfile(folder / 'cells.bin', dtype='<u2').reshape(-1, 2)
    ordinals = nearest_land_ordinals(cells)
    output = np.full((FINE_WIDTH * FINE_HEIGHT, 12), 255, dtype=np.uint8)
    for month in range(1, 13):
        records = np.fromfile(folder / f'climate/month-{month:02d}.bin', dtype=np.uint8).reshape(len(cells), 8, 15)
        with Image.open(folder / f'climate/offsets-{month:02d}.png') as image:
            if image.mode != 'L' or image.size != (FINE_WIDTH, FINE_HEIGHT):
                raise ValueError(f'offsets-{month:02d}.png must be 1440 × 721, 8-bit greyscale')
            offsets = np.asarray(image).reshape(-1)
        indices, scores = fine_scores(cell_level_scores(records), offsets, ordinals)
        output[indices, month - 1] = np.floor(scores + 0.5).astype(np.uint8)
        print(f'Month {month:02d}: {len(indices):,} land points', flush=True)
    path = folder / 'climate/scores.bin'
    output.tofile(path)
    print(f'{path}: {path.stat().st_size:,} bytes in {time.monotonic() - started:.1f} s', flush=True)


if __name__ == '__main__':
    if len(sys.argv) != 2:
        raise SystemExit(__doc__)
    write_default_scores(pathlib.Path(sys.argv[1]))
