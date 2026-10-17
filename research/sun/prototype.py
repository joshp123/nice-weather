"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Replay the published browser maths at the API-selected fine pixel, with an
additional monthly sunshine offset. The API and a city click can select different
land pixels; the measurement's browser-selection-audit.csv records that issue.
Only sunshine share changes: temperature, radiation warmth, rain, humidity, wind,
the retained days, thermal lookup interpolation and spatial weights stay fixed.
All outputs are research probes on the SSD, never live product data.
"""
import argparse
import csv
import importlib.util
import json
from pathlib import Path
import sys
import unicodedata

import numpy as np
from PIL import Image

PROJECT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(PROJECT / 'research/climate-v6'))
import pipeline


def slug_for_name(name):
    plain = unicodedata.normalize('NFKD', name).encode('ascii', 'ignore').decode()
    return plain.lower().replace(' ', '-').replace('_', '-')


def offset_for_target(shares, weights, target):
    """Find one fine-point offset before the existing bilinear cell blend."""
    lower, upper = -1.0, 1.0
    for _ in range(40):
        middle = (lower + upper) / 2
        achieved = np.sum(np.clip(shares + middle, 0, 1).mean(axis=1) * weights)
        if achieved < target:
            lower = middle
        else:
            upper = middle
    return float(np.rint((lower + upper) / 2 * 100) / 100)


def score_month(records, weights, thermal_offset_c, sunshine_offset=0):
    """Match both temperature lookup levels, not a new temperature model."""
    level = float(np.clip((thermal_offset_c + 15) / 3, 0, 10))
    lower = min(int(np.floor(level)), 9)
    fraction = level - lower
    share = records[..., 13] / records[..., 14]
    corrected_share = np.clip(share + sunshine_offset, 0, 1)
    score, nice = 0.0, 0.0
    for shift, level_weight in [(lower * 3 - 15, 1 - fraction), ((lower + 1) * 3 - 15, fraction)]:
        features = pipeline.decode_records(records, shift)
        features[..., 26] = corrected_share * features[..., 27]
        judged = pipeline.score_features(features)
        score += float(np.sum(judged.mean(axis=1) * weights)) * level_weight
        nice += float(np.sum((judged >= 55).mean(axis=1) * weights)) * level_weight
    sunny = float(np.sum(corrected_share.mean(axis=1) * weights))
    return score, nice, sunny


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=pipeline.ROOT / 'sun')
    parser.add_argument('--satellite-monthly', type=Path)
    arguments = parser.parse_args()
    root = arguments.root
    output = root / 'prototype'
    output.mkdir(exist_ok=True)
    metadata = json.loads((root / 'era5/provenance.json').read_text())
    monthly = list(csv.DictReader((root / 'era5/monthly.csv').open()))
    satellite_path = arguments.satellite_monthly or root / 'satellite/monthly.json'
    satellite = {(slug_for_name(row['place']), int(row['month'])): row
                 for row in json.loads(satellite_path.read_text())} if satellite_path.exists() else {}
    result, offsets, worst_baseline_error = [], {}, 0.0
    for slug, place in metadata['places'].items():
        with np.load(root / f'era5/{slug}.npz') as arrays:
            for month in range(1, 13):
                observed = next(row for row in monthly if row['slug'] == slug and int(row['month']) == month)
                records = arrays['bilinear_records'][month - 1]
                weights = arrays['bilinear_weights']
                thermal_offset = float(arrays['fine_offset_c'][month - 1])
                baseline, baseline_nice, baseline_sunny = score_month(records, weights, thermal_offset)
                expected = float(arrays['published_score_bilinear'][month - 1])
                worst_baseline_error = max(worst_baseline_error, abs(baseline - expected))
                native_target = float(observed['selected_native_sunny_percent']) / 100
                native_offset = offset_for_target(records[..., 13] / records[..., 14], weights, native_target)
                native_score, native_nice, native_sunny = score_month(records, weights, thermal_offset, native_offset)
                source = satellite.get((slug, month))
                target = float(source['sunny_share_percent']) / 100 if source else baseline_sunny
                offset = offset_for_target(records[..., 13] / records[..., 14], weights, target) if source else 0.0
                score, nice, sunny = score_month(records, weights, thermal_offset, offset)
                offsets[slug, month] = offset
                result.append(dict(slug=slug, place=place['name'], month=month,
                                   before_card_sunny_percent=int(observed['published_fact_sunny_percent']),
                                   before_engine_sunny_percent=100 * baseline_sunny,
                                   before_score=baseline, before_nice_share=baseline_nice,
                                   native_sunny_percent=100 * native_sunny, native_score=native_score,
                                   source=source.get('source', 'NSRDB satellite') if source else 'unchanged; no satellite sample',
                                   source_years=source.get('source_years', '') if source else '',
                                   target_sunny_percent=100 * target,
                                   after_sunny_percent=100 * sunny, after_score=score, after_nice_share=nice,
                                   sunshine_offset_percentage_points=100 * offset,
                                   source_days=int(source['days']) if source else 0))
    if worst_baseline_error > .0001:
        raise ValueError(f'Published score replay differs by {worst_baseline_error:.6f} points')
    with (output / 'monthly.csv').open('w') as stream:
        writer = csv.DictWriter(stream, fieldnames=list(result[0]))
        writer.writeheader()
        writer.writerows(result)
    png_sizes = []
    for month in range(1, 13):
        # Sparse probe only: unmeasured land is neutral and is not a global fit.
        with Image.open(root / f'era5/baseline-climate/offsets-{month:02}.png') as original:
            image = np.where(np.asarray(original) == 255, 255, 128).astype(np.uint8)
        for slug, place in metadata['places'].items():
            image.flat[place['fine']] = 128 + round(offsets[slug, month] * 100)
        target = output / f'sun-offsets-probe-{month:02}.png'
        Image.fromarray(image).save(target, optimize=True)
        png_sizes.append(target.stat().st_size)
    (output / 'metadata.json').write_text(json.dumps(dict(
        written_by='AI, GPT-6.1 Sol, 2026.10.17',
        baseline_build=json.loads((root / 'era5/meta-baseline.json').read_text())['buildId'],
        method='One monthly percentage-point offset added to every retained sunny/daylight fraction, clamped to 0..1, before daily SUN and score. Fine-point offset fitted against the existing four-cell blend; temperature, other factors and sampled days held fixed.',
        offset_encoding='byte 128 means zero; one byte step = 1 percentage point; 255 = sea. Probe pixels only, unmeasured land neutral.',
        worst_baseline_replay_error_points=worst_baseline_error,
        worst_sunny_target_quantization_error_percentage_points=max(abs(row['after_sunny_percent']-row['target_sunny_percent']) for row in result),
        sparse_probe_png_total_bytes=sum(png_sizes),
        scope='Research only, at the Worker/API-selected published pixel. A fractional-coordinate browser click can choose another pixel. Satellite source period and daily covariance are not corrected by this monthly-offset experiment. No thermal correction for the changed radiation is applied. No production files modified.'
    ), indent=2) + '\n')
    print(f'{len(result)} place-months; baseline replay error {worst_baseline_error:.9f} score points; {sum(png_sizes):,} sparse probe PNG bytes')


if __name__ == '__main__':
    main()
