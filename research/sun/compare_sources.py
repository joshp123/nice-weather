"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Compare independent radiation on exactly the retained ERA5 local dates.
The monthly prototype targets remain explicitly observed-period sensitivity
experiments; paired-date differences distinguish source bias from year choice.
"""
import csv
import gzip
import json
from pathlib import Path

import numpy as np

from prototype import slug_for_name

ROOT = Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/sun')


def main():
    comparisons, colocations, holdout, merged_targets = [], [], [], {}
    places = json.loads((ROOT/'era5/provenance.json').read_text())['places']
    for source, folder in [('SARAH-3 via PVGIS', 'pvgis'), ('NSRDB', 'satellite')]:
        path = ROOT/f'{folder}/daily.json'
        if not path.exists():
            continue
        rows = json.loads(path.read_text())
        for slug in places:
            selected = [row for row in rows if slug_for_name(row['place']) == slug]
            if not selected:
                continue
            with np.load(ROOT/f'era5/{slug}.npz') as era5:
                dates = [str(date) for date in era5['dates']]
                native = era5['native_sunny_hours']/era5['native_daylight_hours']
                city_nearest = era5['nearby_sunny_hours'][:, int(era5['nearby_nearest_index'])]/era5['nearby_daylight_hours'][:, int(era5['nearby_nearest_index'])]
                cell = era5['production_records'][:, 13]/era5['production_records'][:, 14]
                by_date = dict(zip(dates, zip(native, city_nearest, cell)))
                colocated = [row for row in rows if slug_for_name(row['place']) == slug+'-era5-point']
                for month in range(1, 13):
                    paired_native = {row['date']: row for row in colocated
                                     if int(row['month']) == month and row['date'] in by_date}
                    paired_city = {row['date']: row for row in selected
                                   if row['date'] in paired_native}
                    common = sorted(paired_city)
                    if common:
                        colocations.append(dict(slug=slug, source=source, month=month, paired_days=len(common),
                                                source_city_percent=100*float(np.mean([paired_city[date]['sunny_share'] for date in common])),
                                                source_at_era5_point_percent=100*float(np.mean([paired_native[date]['sunny_share'] for date in common])),
                                                era5_at_same_point_percent=100*float(np.mean([by_date[date][0] for date in common])),
                                                era5_cell_percent=100*float(np.mean([by_date[date][2] for date in common]))))
                for month in range(1, 13):
                    observed = [row for row in selected if int(row['month']) == month]
                    paired = [row for row in observed if row['date'] in by_date]
                    current = [value for date, value in zip(dates, native) if int(date[5:7]) == month]
                    source_share = float(np.mean([row['sunny_share'] for row in observed]))
                    entry = dict(slug=slug, place=places[slug]['name'], source=source, month=month,
                                 source_years=';'.join(map(str, sorted({int(row['date'][:4]) for row in observed}))),
                                 source_days=len(observed), paired_days=len(paired),
                                 source_all_percent=100*source_share,
                                 era5_full_native_percent=100*float(np.mean(current)),
                                 source_paired_percent=100*float(np.mean([row['sunny_share'] for row in paired])) if paired else None,
                                 era5_paired_cell_percent=100*float(np.mean([by_date[row['date']][2] for row in paired])) if paired else None,
                                 era5_paired_native_percent=100*float(np.mean([by_date[row['date']][0] for row in paired])) if paired else None,
                                 era5_paired_city_nearest_percent=100*float(np.mean([by_date[row['date']][1] for row in paired])) if paired else None)
                    comparisons.append(entry)
                    # Prefer the finer sub-hourly NSRDB experiment where extracted;
                    # retain source identity, not a silent source substitution.
                    merged_targets[slug, month] = dict(place=places[slug]['name'], month=month,
                                                       days=len(observed), sunny_share_percent=100*source_share,
                                                       source=source, source_years=entry['source_years'])
                    years = sorted({int(row['date'][:4]) for row in paired})
                    if len(years) < 2:
                        continue
                    train_year, test_year = years[0], years[-1]
                    training = [row for row in paired if int(row['date'][:4]) == train_year]
                    testing = [row for row in paired if int(row['date'][:4]) == test_year]
                    if min(len(training), len(testing)) < 15:
                        continue
                    bias = float(np.mean([row['sunny_share']-by_date[row['date']][0] for row in training]))
                    before = float(np.mean([by_date[row['date']][0] for row in testing]))
                    after = float(np.mean([np.clip(by_date[row['date']][0]+bias, 0, 1) for row in testing]))
                    truth = float(np.mean([row['sunny_share'] for row in testing]))
                    holdout.append(dict(slug=slug, source=source, month=month, train_year=train_year, test_year=test_year,
                                        training_days=len(training), testing_days=len(testing),
                                        before_error_percentage_points=100*abs(before-truth),
                                        after_error_percentage_points=100*abs(after-truth),
                                        training_bias_percentage_points=100*bias))
    output = ROOT/'comparison'
    output.mkdir(exist_ok=True)
    for name, rows in [('matched-monthly.csv', comparisons), ('colocated-monthly.csv', colocations), ('year-holdout.csv', holdout)]:
        if rows:
            with (output/name).open('w') as stream:
                writer = csv.DictWriter(stream, fieldnames=list(rows[0]))
                writer.writeheader()
                writer.writerows(rows)
    (output/'prototype-targets.json').write_text(json.dumps(list(merged_targets.values()), indent=2)+'\n')
    cloud_path = ROOT/'observations-2016-2024-hourly.csv.gz'
    if not cloud_path.exists():
        cloud_path = ROOT/'observations-hourly.csv.gz'
    if cloud_path.exists():
        native_by_place = {}
        for slug in places:
            with np.load(ROOT/f'era5/{slug}.npz') as data:
                native_by_place[slug] = dict(zip(map(str, data['dates']), zip(data['native_dni'], data['native_daylight_hours'])))
        counts = {}
        with gzip.open(cloud_path, 'rt') as stream:
            for row in csv.DictReader(stream):
                label = slug_for_name(row['place'])
                slug = next((slug for slug in places if label == slug or label.startswith(slug+'-')), None)
                if slug is None or row['local_date'] not in native_by_place[slug]:
                    continue
                dni, daylight = native_by_place[slug][row['local_date']]
                hour_index = int(row['local_hour']) - 10
                if not 0 <= hour_index < daylight:
                    continue
                cloud = float(row['cloud_oktas'])
                if cloud < 7:
                    continue
                key = slug, int(row['local_date'][5:7]), row['station_id']
                total, sunny = counts.get(key, (0, 0))
                counts[key] = total+1, sunny+int(dni[hour_index] >= 300)
        cloud_rows = [dict(slug=slug, month=month, station_id=station, matched_7_or_8_okta_reports=total,
                           era5_sunny_despite_near_overcast_percent=100*sunny/total)
                      for (slug, month, station), (total, sunny) in sorted(counts.items())]
        if cloud_rows:
            with (output/'near-overcast-hour-check.csv').open('w') as stream:
                writer = csv.DictWriter(stream, fieldnames=list(cloud_rows[0]))
                writer.writeheader()
                writer.writerows(cloud_rows)
    print(f'{len(comparisons)} paired source-month comparisons; {len(holdout)} held-out year-months')


if __name__ == '__main__':
    main()
