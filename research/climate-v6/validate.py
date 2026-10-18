"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Independently decode and inspect the published v6 climate files.
"""

import argparse
import contextlib
import datetime as dt
import importlib.util
import json
import os
import pathlib
import struct
import time

import numpy as np
from PIL import Image, ImageDraw


ROOT = pathlib.Path(__file__).resolve().parents[2]
DEFAULT_DATA = pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data')
POINT_DTYPE = np.dtype([('fine', '<u4'), ('source', '<u4'), ('shift', '<i2'), ('cell', '<u2')])
PLACES = {
    'Málaga': (36.721, -4.421),
    'Rotterdam': (51.923, 4.479),
    'San Diego': (32.716, -117.162),
    'Cape Town': (-33.925, 18.424),
    'Singapore': (1.352, 103.820),
    'Las Palmas': (28.124, -15.437),
    'San Francisco': (37.775, -122.419),
}


def load_rule():
    spec = importlib.util.spec_from_file_location('nice_v6_reference', ROOT / 'research/taste/nice_v6.py')
    rule = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(rule)
    return rule


def score_records(records, offset=0):
    """The reference curves applied to the contract's interpolated felt values."""
    rule = load_rule()
    records = np.asarray(records)
    temperatures = records[..., :11].astype(float) / 3 - 30
    sun = np.stack([np.interp(np.arange(11), [0, 2, 4, 6, 8, 10], row[:6]) for row in temperatures.reshape(-1, 11)])
    shade = np.stack([np.interp(np.arange(12), [2, 5, 8, 11], row[6:10]) for row in temperatures.reshape(-1, 11)])
    sun += offset
    shade += offset
    flat = records.reshape(-1, 15)
    daylight = flat[:, 14].astype(float)
    hourly = np.minimum(rule.COLD_GRADE(sun), rule.HEAT_GRADE(shade[:, :11]))
    daytime = (hourly * (np.arange(11) < daylight[:, None])).sum(axis=1)
    evening = np.minimum(rule.EVENING_GRADE(shade[:, 11]), rule.HEAT_GRADE(shade[:, 11]))
    warmth = (daytime + 2 * evening) / (daylight + 2)
    score = warmth * rule.RAIN_FACTOR((flat[:, 11].astype(float) / 50) ** 2)
    score *= rule.WIND_FACTOR(flat[:, 12].astype(float) / 2)
    score *= rule.HUMIDITY_FACTOR(flat[:, 10].astype(float) / 3 - 30)
    score *= rule.SUN_FACTOR(flat[:, 13] / daylight)
    return score.reshape(records.shape[:-1])


def reference_months():
    table = {}
    for line in (ROOT / 'research/taste/anchor-places.md').read_text().splitlines():
        fields = [field.strip() for field in line.split('|')]
        if len(fields) != 18:
            continue
        name = fields[1].rstrip('*')
        if name in PLACES:
            table[name] = [int(value) for value in fields[3:15]]
    return table


def validate_intermediate(path, return_errors=False):
    """Compare every actual hourly cell day with independently decoded records."""
    import pipeline
    rule = load_rule()
    with np.load(path) as saved:
        features = saved['sums'] / saved['counts'][:, None, None]
        cells = saved['cells']
    daylight = np.rint(features[..., 27])
    daylight_mask = np.arange(11) < daylight[..., None]
    daytime = np.sum(np.minimum(rule.COLD_GRADE(features[..., :11]), rule.HEAT_GRADE(features[..., 11:22])) * daylight_mask, axis=-1)
    evening = np.minimum(rule.EVENING_GRADE(features[..., 22]), rule.HEAT_GRADE(features[..., 22]))
    full = (daytime + 2 * evening) / (daylight + 2)
    full *= rule.HUMIDITY_FACTOR(features[..., 23]) * rule.RAIN_FACTOR(features[..., 24])
    full *= rule.WIND_FACTOR(features[..., 25]) * rule.SUN_FACTOR(features[..., 26] / daylight)
    compact = score_records(pipeline.encode_records(features))
    chunk = int(path.parent.name.removeprefix('chunk'))
    epoch = dt.date(1940, 1, 1) + dt.timedelta(days=chunk * 364)
    months = np.array([(epoch + dt.timedelta(days=day + 2)).month for day in range(features.shape[1])])
    proof = []
    all_errors = []
    for month in range(1, 13):
        error = np.abs(full[:, months == month].mean(axis=1) - compact[:, months == month].mean(axis=1))
        all_errors.append(error)
        worst = int(np.argmax(error))
        proof.append({'month': month, 'mean_absolute_error': round(float(error.mean()), 3),
                      'max_absolute_error': round(float(error[worst]), 3),
                      'worst_cell_ordinal': int(cells[worst]), 'above_3': int((error > 3).sum())})
    report = {'intermediate': str(path), 'cell_count': len(cells), 'day_count': features.shape[1],
              'full_hourly_reference': 'Cell mean hourly felt values, integer rounded cell daylight as required by contract; unquantized nuisance means',
              'maximum_full_hourly_score': float(full.max()), 'monthly_compact_proof': proof}
    return (report, np.stack(all_errors, axis=1), cells) if return_errors else report


def validate_available_intermediates(data):
    paths = sorted(path for path in (data / 'climate-v6-work').glob('chunk*/r*.npz') if '.partial' not in path.name)
    cells = np.fromfile(data / 'v6/cells.bin', dtype='<u2').reshape(-1, 2)
    errors = []
    worst = []
    for path in paths:
        report, tile_errors, cell_ordinals = validate_intermediate(path, return_errors=True)
        assert np.all(np.isfinite(tile_errors)), f'Non-finite score errors: {path}'
        errors.append(tile_errors.reshape(-1))
        candidates = np.argsort(tile_errors.reshape(-1))[-20:]
        for candidate in candidates:
            cell, month = np.unravel_index(candidate, tile_errors.shape)
            ordinal = int(cell_ordinals[cell])
            row, column = cells[ordinal]
            worst.append({'chunk': int(path.parent.name.removeprefix('chunk')), 'tile': path.name,
                          'cell_ordinal': ordinal, 'cell_center_latitude': 89.5 - int(row),
                          'cell_center_longitude': int(column) - 179.5, 'month': int(month + 1),
                          'absolute_error': round(float(tile_errors[cell, month]), 4)})
        worst = sorted(worst, key=lambda item: -item['absolute_error'])[:20]
    combined = np.concatenate(errors)
    return {'snapshot_tiles': len(paths), 'cell_month_comparisons': len(combined),
            'full_hourly_reference': 'Cell mean hourly felt values; integer rounded cell daylight; unquantized nuisance means',
            'chunks': sorted({int(path.parent.name.removeprefix('chunk')) for path in paths}),
            'mean_absolute_error': round(float(combined.mean()), 4),
            'percentiles': {str(percentile): round(float(np.percentile(combined, percentile)), 4) for percentile in (50, 90, 95, 99, 99.9, 100)},
            'above_3': int((combined > 3).sum()), 'above_3_percent': round(float((combined > 3).mean() * 100), 4),
            'worst_cell_months': worst}


def validate_static(data):
    started = time.monotonic()
    published = data / 'v6'
    cells = np.fromfile(published / 'cells.bin', dtype='<u2').reshape(-1, 2)
    point_path = published / 'points.bin'
    point_count = struct.unpack('<I', point_path.read_bytes()[:4])[0]
    assert point_path.stat().st_size == 4 + 12 * point_count
    points = np.memmap(point_path, dtype=POINT_DTYPE, mode='r', offset=4, shape=(point_count,))
    assert len(cells) > 0 and len(cells) < 65536
    keys = cells[:, 0].astype(int) * 360 + cells[:, 1]
    assert np.all(np.diff(keys) > 0), 'Cells are not unique and row-major'
    assert np.all(cells[:, 0] <= 150) and np.all(cells[:, 1] < 360)
    fine_rows, fine_cols = np.divmod(points['fine'], 1440)
    assert np.all(points['fine'] < 1440 * 721)
    assert len(np.unique(points['fine'])) == point_count
    assert np.all(points['cell'] < len(cells))
    assert np.all(np.minimum(fine_rows // 4, 179) == cells[points['cell'], 0])
    assert np.all(fine_cols // 4 == cells[points['cell'], 1])
    assert np.all(fine_rows <= 600), 'Points south of 60S included'
    land_sea = np.roll(np.load(data / 'era5/static_lsm.npy'), 720, axis=1).reshape(-1)
    heights = np.roll(np.load(data / 'era5/static_z.npy'), 720, axis=1).reshape(-1) / 9.80665
    all_fine = np.arange(1440 * 721)
    all_rows, all_columns = np.divmod(all_fine, 1440)
    all_cell_keys = np.minimum(all_rows // 4, 179) * 360 + all_columns // 4
    strong_cells = np.bincount(all_cell_keys[(land_sea >= .5) & (all_rows <= 600)], minlength=64800) > 0
    expected_fine = all_fine[(all_rows <= 600) & ((land_sea >= .5) | ((land_sea >= .05) & ~strong_cells[all_cell_keys]))]
    assert np.array_equal(np.sort(points['fine']), expected_fine), 'Selected land points are incomplete'
    assert np.array_equal(keys, np.unique(all_cell_keys[expected_fine])), 'Land cells are incomplete'
    assert np.all(land_sea[points['fine']] >= .05)
    strong_land_count = np.bincount(points['cell'], weights=land_sea[points['fine']] >= .5, minlength=len(cells))
    assert np.all((land_sea[points['fine']] >= .5) | (strong_land_count[points['cell']] == 0))
    shifted = points['fine'] != points['source']
    assert np.all(land_sea[points['source'][shifted]] >= .8)
    source_rows, source_cols = np.divmod(points['source'], 1440)
    assert np.all(np.abs(source_rows.astype(int) - fine_rows) <= 1)
    assert np.all(np.minimum(np.abs(source_cols.astype(int) - fine_cols), 1440 - np.abs(source_cols.astype(int) - fine_cols)) <= 1)
    # Coastal points are corrected to sea level from their temperature source's height (at most 600 m); others are not shifted.
    corrected = points['shift'] != 0
    sea_level_shift = .0065 * np.clip(heights[points['source']], 0, 600)
    assert np.all(np.abs(points['shift'][corrected] / 100 - sea_level_shift[corrected]) <= .0051)
    point_counts = np.bincount(points['cell'], minlength=len(cells))
    assert np.all(point_counts > 0)
    return cells, points, {
        'cell_count': len(cells), 'point_count': point_count,
        'coastal_sources_changed': int(shifted.sum()),
        'points_per_cell_min_max': [int(point_counts.min()), int(point_counts.max())],
        'cells_bytes': (published / 'cells.bin').stat().st_size,
        'points_bytes': point_path.stat().st_size,
        'validation_seconds': round(time.monotonic() - started, 3),
    }


def offsets_preview(offsets, output, month):
    regions = [('Canaries', 25, 31, -19, -12), ('California', 30, 42, -125, -114), ('Alps', 43, 49, 5, 17)]
    canvas = Image.new('RGB', (990, 420), 'white')
    draw = ImageDraw.Draw(canvas)
    for column, (name, south, north, west, east) in enumerate(regions):
        crop = offsets[int((90 - north) * 4):int((90 - south) * 4) + 1,
                       int((west + 180) * 4):int((east + 180) * 4) + 1]
        values = crop.astype(float) - 128
        colour = np.zeros((*crop.shape, 3), dtype=np.uint8)
        colour[..., 0] = np.clip(175 + values * 3, 0, 255)
        colour[..., 1] = np.clip(175 - np.abs(values), 0, 255)
        colour[..., 2] = np.clip(175 - values * 3, 0, 255)
        colour[crop == 255] = [30, 40, 55]
        image = Image.fromarray(colour)
        image.thumbnail((310, 320), Image.Resampling.NEAREST)
        scale = min(310 / image.width, 320 / image.height)
        image = image.resize((int(image.width * scale), int(image.height * scale)), Image.Resampling.NEAREST)
        canvas.paste(image, (column * 330 + 10, 45))
        land_values = values[crop != 255] / 10
        draw.text((column * 330 + 10, 10), f'{name}, month {month:02}', fill='black')
        draw.text((column * 330 + 10, 28), f'{land_values.min():+.1f} to {land_values.max():+.1f} C', fill='black')
    draw.text((10, 385), 'Blue: colder than cell. Red: warmer. Dark: sea / no data. Range +/- 12.7 C.', fill='black')
    canvas.save(output)


def validate_climate(data, cells, points, output):
    names = [f'month-{month:02}.bin' for month in range(1, 13)]
    names += [f'offsets-{month:02}.png' for month in range(1, 13)]
    names += ['facts.bin', 'meta.json']
    with contextlib.ExitStack() as stack:
        directory = os.open(data / 'v6/climate', os.O_RDONLY)
        stack.callback(os.close, directory)
        files = {name: stack.enter_context(os.fdopen(os.open(name, os.O_RDONLY, dir_fd=directory), 'rb')) for name in names}
        report = validate_climate_files(files, cells, points, output)
        report['snapshot_directory_inode'] = os.fstat(directory).st_ino
        return report


def validate_climate_files(files, cells, points, output):
    references = reference_months()
    fine_lookup = {int(point['fine']): int(point['cell']) for point in points}
    point_latitudes = 90 - (points['fine'] // 1440) / 4
    point_longitudes = (points['fine'] % 1440) / 4 - 180
    places = {}
    for name, (latitude, longitude) in PLACES.items():
        row = int(round((90 - latitude) * 4))
        column = int(round((longitude + 180) * 4)) % 1440
        fine = row * 1440 + column
        nearest_grid_point_is_land = fine in fine_lookup
        distance_squared = (point_latitudes - latitude) ** 2 + ((point_longitudes - longitude) * np.cos(np.deg2rad(latitude))) ** 2
        selected = int(np.argmin(distance_squared))
        fine = int(points['fine'][selected])
        row, column = divmod(fine, 1440)
        places[name] = {'fine_index': fine, 'fine_latitude': 90 - row / 4,
                        'fine_longitude': column / 4 - 180, 'cell_ordinal': fine_lookup[fine],
                        'nearest_grid_point_is_land': nearest_grid_point_is_land,
                        'selected_land_distance_km': round(float(np.sqrt(distance_squared[selected]) * 111.2), 2),
                        'monthly_scores': [], 'monthly_offsets_c': [], 'reference_scores': references[name]}
    sizes = {}
    selected_land = np.zeros(721 * 1440, dtype=bool)
    selected_land[points['fine']] = True
    for month in range(1, 13):
        record_name = f'month-{month:02}.bin'
        record_file = files[record_name]
        assert os.fstat(record_file.fileno()).st_size == len(cells) * 8 * 15
        records = np.memmap(record_file, mode='r', dtype='u1', shape=(len(cells), 8, 15))
        assert np.all((records[:, :, 14] >= 1) & (records[:, :, 14] <= 11))
        assert np.all(records[:, :, 13] <= records[:, :, 14])
        with Image.open(files[f'offsets-{month:02}.png']) as image:
            assert image.mode == 'L' and image.size == (1440, 721)
            offsets = np.asarray(image)
        assert np.array_equal(offsets.reshape(-1) != 255, selected_land), 'Offset land mask does not match static selected points'
        for place in places.values():
            value = int(offsets.reshape(-1)[place['fine_index']])
            assert value != 255
            offset = (value - 128) / 10
            place['monthly_offsets_c'].append(offset)
            place['monthly_scores'].append(round(float(score_records(records[place['cell_ordinal']], offset).mean()), 2))
        sizes[record_name] = os.fstat(record_file.fileno()).st_size
        sizes[f'offsets-{month:02}.png'] = os.fstat(files[f'offsets-{month:02}.png'].fileno()).st_size
        if month in (1, 7):
            offsets_preview(offsets, output / f'offsets-{month:02}-regions.png', month)
    assert os.fstat(files['facts.bin'].fileno()).st_size == 1440 * 721 * 72
    facts = np.memmap(files['facts.bin'], dtype='u1', mode='r', shape=(721 * 1440, 12, 6))
    assert not np.any(facts[~selected_land]), 'Sea facts are not zero'
    assert np.all(facts[:, :, 3:5] <= 100), 'Percent facts exceed 100'
    for place in places.values():
        differences = np.array(place['monthly_scores']) - place['reference_scores']
        place['monthly_difference'] = np.round(differences, 2).tolist()
        place['mean_absolute_difference'] = round(float(np.abs(differences).mean()), 2)
        place['max_absolute_difference'] = round(float(np.abs(differences).max()), 2)
        place['facts'] = facts[place['fine_index']].tolist()
    sizes['facts.bin'] = os.fstat(files['facts.bin'].fileno()).st_size
    sizes['meta.json'] = os.fstat(files['meta.json'].fileno()).st_size
    metadata = json.load(files['meta.json'])
    lines = ['Written by AI on 2026.10.17 by model GPT-6.1 Sol.', '',
             f'Published generation: {metadata["buildTime"]}. Chunks: {metadata["chunks"]}.', '',
             '| Place | Jan | Feb | Mar | Apr | May | Jun | Jul | Aug | Sep | Oct | Nov | Dec |',
             '|---|' + '---:|' * 12]
    for name, place in places.items():
        lines.append('| ' + name + ' published | ' + ' | '.join(f'{score:.1f}' for score in place['monthly_scores']) + ' |')
        lines.append('| ' + name + ' ten-year reference | ' + ' | '.join(str(score) for score in place['reference_scores']) + ' |')
    lines += ['', 'Scores use each selected fine land point\'s monthly offset. The JSON records coordinates, distance from the requested city, monthly differences and facts.',
              'San Francisco reference uses downtown station temperatures; ERA5 land points may be warmer in summer.',
              'A single year can move monthly means by several points. Large differences require source inspection; they are not automatically sampling noise.', '']
    (output / 'KNOWN-PLACES.md').write_text('\n'.join(lines))
    return {'known_places': places, 'files_bytes': sizes, 'meta': metadata}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--data', type=pathlib.Path, default=DEFAULT_DATA)
    parser.add_argument('--output', type=pathlib.Path, default=pathlib.Path('/tmp/nwf-agents/overnight/climate'))
    parser.add_argument('--static-only', action='store_true')
    parser.add_argument('--intermediate', type=pathlib.Path, help='Audit one real processed tile against its full hourly cell features')
    parser.add_argument('--available-intermediates', action='store_true', help='Audit a snapshot of every currently processed tile')
    args = parser.parse_args()
    started = time.monotonic()
    args.output.mkdir(parents=True, exist_ok=True)
    if args.available_intermediates:
        report = validate_available_intermediates(args.data)
    elif args.intermediate:
        report = validate_intermediate(args.intermediate)
    else:
        cells, points, report = validate_static(args.data)
        if not args.static_only:
            report['climate'] = validate_climate(args.data, cells, points, args.output)
    report['validation_seconds'] = round(time.monotonic() - started, 2)
    output = args.output / ('available-intermediates-validation.json' if args.available_intermediates else 'intermediate-validation.json' if args.intermediate else 'static-validation.json' if args.static_only else 'validation.json')
    output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(report, ensure_ascii=False))


if __name__ == '__main__':
    main()
