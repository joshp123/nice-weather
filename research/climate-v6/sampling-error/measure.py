"""Written by AI on 2026.10.19 by model Claude Opus 5.5.

How far are the site's day counts from the 25-year truth?

The site keeps 8 quantile-sampled days per month for each overview cell and
counts nice, perfect and awful days from those 8. This scores every one of the
9,000 days per cell with the same scoring and compares, per cell, the counts a
visitor would read for the year and for November to March.

Usage: measure.py <climate-build.json> <assembled v6 folder> <output.json>
"""
import json, pathlib, sys
import numpy as np

REPOSITORY = pathlib.Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPOSITORY / 'research/climate-v6'))
from pipeline import aggregate_chunk_features, decode_records, encode_records, score_features
from static import POINT_DTYPE

MONTH_DAYS = np.array([31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31])
NICE, PERFECT, AWFUL = 55, 85, 15
ANCHORS = {'Málaga': (36.72, -4.42), 'Lisbon': (38.72, -9.14), 'Seville': (37.39, -5.98),
           'Tenerife south (Arona)': (28.10, -16.68), 'Funchal': (32.65, -16.91),
           'San Diego': (32.72, -117.16), 'Los Angeles': (34.05, -118.24), 'San Francisco': (37.77, -122.42),
           'Cape Town': (-33.92, 18.42), 'Cairo': (30.04, 31.24), 'Austin': (30.27, -97.74),
           'Sydney': (-33.87, 151.21), 'Rotterdam': (51.92, 4.48), 'Singapore': (1.35, 103.82)}


def counts_by_month(scores, months, valid_days):
    """Per cell and month: scored days, nice, perfect and awful days."""
    out = np.zeros((scores.shape[0], 12, 4), np.int64)
    for month in range(1, 13):
        s = scores[:, months == month]
        out[:, month - 1, 0] += s.shape[1] * valid_days
        out[:, month - 1, 1] += ((s >= NICE).sum(1)) * valid_days
        out[:, month - 1, 2] += ((s >= PERFECT).sum(1)) * valid_days
        out[:, month - 1, 3] += ((s < AWFUL).sum(1)) * valid_days
    return out


def main():
    build, folder, output = json.load(open(sys.argv[1])), pathlib.Path(sys.argv[2]), pathlib.Path(sys.argv[3])
    points = np.fromfile(folder / 'points.bin', dtype=POINT_DTYPE, offset=4)
    cell_count = (folder / 'cells.bin').stat().st_size // 4
    exact = np.zeros((cell_count, 12, 4), np.int64)       # the product's scoring (encoded records), all days
    exact_full = np.zeros((cell_count, 12, 4), np.int64)  # full-precision features, all days
    for chunk, path in sorted(build['chunkFolders'].items(), key=lambda item: int(item[0])):
        chunk_folder = pathlib.Path(path)
        expected = {p.name for p in chunk_folder.glob('r*.npz') if '.partial' not in p.name}
        features, valid, dates = aggregate_chunk_features(chunk_folder, points, cell_count, expected)
        months = np.array([d.month for d in dates])
        exact_full += counts_by_month(score_features(features), months, valid)
        exact += counts_by_month(score_features(decode_records(encode_records(features))), months, valid)
        print('chunk', chunk, 'cells valid', int(valid.sum()), flush=True)
        del features
    sampled = np.zeros((cell_count, 12, 3))
    for month in range(1, 13):
        records = np.fromfile(folder / f'climate/month-{month:02d}.bin', dtype=np.uint8).reshape(cell_count, 8, 15)
        s = score_features(decode_records(records))
        sampled[:, month - 1] = [(s >= NICE).mean(1), (s >= PERFECT).mean(1), (s < AWFUL).mean(1)] if False else np.stack([(s >= NICE).mean(1), (s >= PERFECT).mean(1), (s < AWFUL).mean(1)], 1)

    def visitor_days(shares, month_list):
        return (shares[:, month_list] * MONTH_DAYS[month_list][None, :, None]).sum(1)

    def exact_days(table, month_list):
        share = table[:, month_list, 1:] / np.maximum(table[:, month_list, :1], 1)
        return (share * MONTH_DAYS[month_list][None, :, None]).sum(1)

    report = {'cells': cell_count, 'scoredDaysPerCell': int(exact[:, :, 0].max(1).max() if False else exact[0, :, 0].sum()), 'periods': {}, 'anchors': {}}
    periods = {'year': list(range(12)), 'nov-mar': [10, 11, 0, 1, 2]}
    for name, month_list in periods.items():
        truth, truth_full, estimate = exact_days(exact, month_list), exact_days(exact_full, month_list), visitor_days(sampled, month_list)
        entry = {}
        for index, kind in enumerate(['nice', 'perfect', 'awful']):
            error = np.abs(estimate[:, index] - truth[:, index])
            encoding = np.abs(truth[:, index] - truth_full[:, index])
            entry[kind] = {'samplingErrorDays': {'median': float(np.median(error)), 'p95': float(np.quantile(error, .95)), 'p99': float(np.quantile(error, .99)), 'max': float(error.max()), 'cellsOver10Days': int((error > 10).sum())},
                           'encodingErrorDays': {'median': float(np.median(encoding)), 'p95': float(np.quantile(encoding, .95)), 'max': float(encoding.max())}}
        report['periods'][name] = entry
        for anchor, (latitude, longitude) in ANCHORS.items():
            fine = int(round((90 - latitude) * 4)) * 1440 + int(round((longitude + 180) * 4)) % 1440
            match = np.nonzero(points['fine'] == fine)[0]
            if not len(match):
                continue
            cell = int(points['cell'][match[0]])
            report['anchors'].setdefault(anchor, {'cell': cell})[name] = {
                'exactNice': round(float(truth[cell, 0]), 1), 'siteNice': round(float(estimate[cell, 0]), 1),
                'exactPerfect': round(float(truth[cell, 1]), 1), 'sitePerfect': round(float(estimate[cell, 1]), 1),
                'exactAwful': round(float(truth[cell, 2]), 1), 'siteAwful': round(float(estimate[cell, 2]), 1)}
    output.write_text(json.dumps(report, indent=1, ensure_ascii=False))
    print(json.dumps(report, indent=1, ensure_ascii=False))


if __name__ == '__main__':
    main()
