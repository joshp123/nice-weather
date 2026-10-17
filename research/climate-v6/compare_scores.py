#!/usr/bin/env python3
# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
"""Read published climate files; save evidence and compare with an earlier capture.

Run in the same Nix Python environment as the climate pipeline:
  python3 research/climate-v6/compare_scores.py artifacts/v6 --output baseline.json
  python3 research/climate-v6/compare_scores.py artifacts/v6 --baseline baseline.json --output latest.json
Scores are the place card's unrounded default score, plus the published integer.
This command never writes to the supplied v6 folder.
"""

import argparse
import calendar
import datetime as dt
import hashlib
import importlib.util
import json
import math
import pathlib

import numpy as np
from PIL import Image


REPOSITORY = pathlib.Path(__file__).resolve().parents[2]
PLACES = {
    'Lisbon': (38.722, -9.139),
    'Málaga': (36.721, -4.421),
    'Rotterdam': (51.923, 4.479),
    'Tenerife south coast': (28.12, -16.73),
    'San Diego': (32.716, -117.162),
    'Cape Town': (-33.925, 18.424),
}


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def nearest_land_index(latitude, longitude, offsets):
    """Match src/engine/sampling.ts, including JavaScript rounding and ties."""
    column = ((longitude + 180) * 4) % 1440
    row = (90 - latitude) * 4
    centre_column, centre_row = math.floor(column + .5), math.floor(row + .5)
    nearest, nearest_distance = None, math.inf
    for candidate_row in range(centre_row - 2, centre_row + 3):
        if not 0 <= candidate_row <= 600:
            continue
        for candidate_column in range(centre_column - 2, centre_column + 3):
            index = candidate_row * 1440 + candidate_column % 1440
            if offsets[index] == 255:
                continue
            distance = (candidate_row - row) ** 2 + (candidate_column - column) ** 2
            if distance < nearest_distance:
                nearest, nearest_distance = index, distance
    if nearest is None:
        raise ValueError(f'No fine land point near {latitude}, {longitude}')
    return nearest


def capture(folder):
    source = REPOSITORY / 'src/engine/default_scores.py'
    spec = importlib.util.spec_from_file_location('default_scores', source)
    engine = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(engine)
    metadata_path = folder / 'climate/meta.json'
    metadata_bytes = metadata_path.read_bytes()
    metadata = json.loads(metadata_bytes)
    cells = np.fromfile(folder / 'cells.bin', dtype='<u2').reshape(-1, 2)
    ordinals = engine.nearest_land_ordinals(cells)
    report = {
        'writtenBy': 'AI on 2026.10.17 by model GPT-6.1 Sol',
        'capturedAt': dt.datetime.now(dt.timezone.utc).isoformat(),
        'meaning': 'Default-taste place scores out of 100: nearest fine land point; monthly own felt offset; cell and felt-level interpolation; eight sampled real days. No map spline. publishedScore is rounded to the nearest integer.',
        'metadata': metadata,
        'inputSha256': {'cells.bin': sha256(folder / 'cells.bin'),
                        'climate/meta.json': hashlib.sha256(metadata_bytes).hexdigest()},
        'scoringSourceSha256': {'src/engine/default_scores.py': sha256(source),
                               'research/taste/nice_v6.py': sha256(REPOSITORY / 'research/taste/nice_v6.py')},
        'places': [{'name': name, 'latitude': latitude, 'longitude': longitude, 'months': []}
                   for name, (latitude, longitude) in PLACES.items()],
    }
    published_path = folder / 'climate/scores.bin'
    published = np.fromfile(published_path, dtype=np.uint8).reshape(1440 * 721, 12)
    report['inputSha256']['climate/scores.bin'] = sha256(published_path)
    for month in range(1, 13):
        records_path = folder / f'climate/month-{month:02d}.bin'
        offsets_path = folder / f'climate/offsets-{month:02d}.png'
        image_path = folder / f'climate/scores-{calendar.month_name[month].lower()}.png'
        records = np.fromfile(records_path, dtype=np.uint8).reshape(len(cells), 8, 15)
        with Image.open(offsets_path) as image:
            offsets = np.asarray(image).reshape(-1)
        with Image.open(image_path) as image:
            image_scores = np.asarray(image).reshape(-1)
        indices, scores = engine.fine_scores(engine.cell_level_scores(records), offsets, ordinals)
        for place in report['places']:
            index = nearest_land_index(place['latitude'], place['longitude'], offsets)
            score = float(scores[np.searchsorted(indices, index)])
            published_score = int(published[index, month - 1])
            if math.floor(score + .5) != published_score:
                raise ValueError(f"Published scores.bin disagrees with records at {place['name']}, month {month}: {published_score} vs {score}")
            if int(image_scores[index]) != published_score:
                raise ValueError(f"Published score image disagrees with scores.bin at {place['name']}, month {month}")
            row, column = divmod(index, 1440)
            place['months'].append({'month': month, 'score': round(score, 6),
                                    'publishedScore': published_score,
                                    'fineLatitude': 90 - row / 4,
                                    'fineLongitude': -180 + column / 4,
                                    'feltOffsetC': (int(offsets[index]) - 128) / 10})
        for path in (records_path, offsets_path, image_path):
            report['inputSha256'][str(path.relative_to(folder))] = sha256(path)
    if metadata_path.read_bytes() != metadata_bytes:
        raise RuntimeError('Assembly changed during capture; rerun against its immutable snapshot')
    return report


def print_table(current, baseline=None):
    if baseline and current['scoringSourceSha256'] != baseline['scoringSourceSha256']:
        print('Scoring code changed since the baseline; differences may include those code changes.\n')
    months = ' | '.join(calendar.month_abbr[month] for month in range(1, 13))
    print(f'| Place | Assembly | {months} |')
    print('| --- | --- | ' + ' | '.join(['---:'] * 12) + ' |')
    for index, place in enumerate(current['places']):
        if baseline:
            earlier = baseline['places'][index]
            if (earlier['name'], earlier['latitude'], earlier['longitude']) != (place['name'], place['latitude'], place['longitude']):
                raise ValueError('Baseline place coordinates differ')
            rows = [(earlier, f"{len(baseline['metadata']['chunks'])} chunks"),
                    (place, f"{len(current['metadata']['chunks'])} chunks"),
                    (None, 'Change')]
        else:
            rows = [(place, f"{len(current['metadata']['chunks'])} chunks")]
        for row, label in rows:
            values = ([f"{item['score']:.1f}" for item in row['months']] if row else
                      [f"{now['score'] - before['score']:+.1f}" for now, before in zip(place['months'], earlier['months'])])
            print(f"| {place['name']} | {label} | {' | '.join(values)} |")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('folder', type=pathlib.Path)
    parser.add_argument('--baseline', type=pathlib.Path)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args()
    report = capture(args.folder)
    baseline = json.loads(args.baseline.read_text()) if args.baseline else None
    if baseline:
        report['sameScoringCodeAsBaseline'] = report['scoringSourceSha256'] == baseline['scoringSourceSha256']
    args.output.write_text(json.dumps(report, indent=2, ensure_ascii=False) + '\n')
    print_table(report, baseline)


if __name__ == '__main__':
    main()
