"""Paired local-hour validation, without fitting to evaluation observations.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
Station and model extrema use the same observed whole UTC hours. The 21:00
comparison uses air temperature; published felt21 is retained as provenance only.
"""
import argparse
import calendar
import collections
import csv
import json
import math
import pathlib
import statistics
import time

import numpy as np
import pandas as pd

from compare import ZONES, era5_daily, finite, published_dates
from published import AGADIR_AIRPORT


def distance_km(latitude, longitude, other_latitude, other_longitude):
    latitude, other_latitude = map(math.radians, [latitude, other_latitude])
    delta_longitude = math.radians(other_longitude - longitude)
    haversine = math.sin((other_latitude - latitude) / 2) ** 2 + math.cos(latitude) * math.cos(other_latitude) * math.sin(delta_longitude / 2) ** 2
    return 6371 * 2 * math.asin(min(1, math.sqrt(haversine)))


def hourly_values(path):
    with np.load(path) as data:
        hours = data['times'].astype('datetime64[h]').astype('int64')
        return {int(hour): float(value) for hour, value in zip(hours, data['temperature_c'])
                if hour >= int(np.datetime64('2016-01-01', 'h').astype('int64'))}


def monthly_quality(dates, month):
    counts = collections.Counter(date[:4] for date in dates)
    years = [year for year, count in counts.items()
             if count >= .8 * calendar.monthrange(int(year), month)[1]]
    return len(years), ';'.join(sorted(years))


def mean(values):
    return statistics.mean(values) if values else None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=pathlib.Path, default=pathlib.Path('/tmp/nwf-agents/round4/fine'))
    parser.add_argument('--scratch', type=pathlib.Path, default=pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-phase1'))
    args = parser.parse_args()
    started = time.monotonic()
    stations = json.loads((args.output / 'stations/evaluation_stations.json').read_text())
    additions = args.output / 'hourly/evaluation_additions.json'
    if additions.exists():
        stations.extend(json.loads(additions.read_text()))
    if not any(station.get('station') and station['slug'] == 'agadir' for station in stations):
        stations.append(AGADIR_AIRPORT)
    stations = {station['slug']: station for station in stations if station.get('station')}
    provenance = json.loads((args.output / 'hourly/summary.json').read_text())
    locations = collections.defaultdict(list)
    for record in provenance['files']:
        if record['status'] == 'ok':
            locations[(record['slug'], record['hourly_station'])].append(record['location'])
    # Adjacent UTC-year inputs overlap at local midnight. Keep the fuller day once.
    observed = {}
    for row in csv.DictReader((args.output / 'hourly/daily_sampled_extrema.csv').open()):
        if not 2016 <= int(row['date'][:4]) <= 2025:
            continue
        key = row['slug'], row['date']
        if key not in observed or int(row['temperature_hours']) > int(observed[key]['temperature_hours']):
            observed[key] = row
    live_document = json.loads((args.output / 'live_points.json').read_text())
    live = live_document['stations']
    published_days = published_dates(live_document['live_meta']['chunks'])
    grids = {}
    for name, filename in [('chelsa', 'chelsa_points.json'), ('worldclim14', 'worldclim14_30s_points.json'), ('prism', 'prism_points.json')]:
        path = args.scratch / filename
        if path.exists():
            document = json.loads(path.read_text())
            grids[name] = document.get('points', document)
    rows, unavailable, site_metadata = [], [], {}
    corrected_ranges = collections.defaultdict(dict)
    ordering_repairs = collections.defaultdict(set)
    affine_parameters = {}
    composite_sources = {}
    for slug in sorted({key[0] for key in observed}):
        station = stations[slug]
        station_id = station['station']
        native_path = args.scratch / f'era5_{station_id}_native.npz'
        if not native_path.exists():
            unavailable.append({'slug': slug, 'reason': 'native ERA5 absent'})
            continue
        daily, normals = era5_daily(native_path, ZONES[slug])
        normals['available'] = {element: [mean([values[element] for date, values in daily.items()
            if date in published_days and int(date[5:7]) == month]) for month in range(1,13)]
            for element in ('TMIN', 'TMAX', 'TMEAN')}
        native_hours = hourly_values(native_path)
        product_path = args.scratch / f'era5_{station_id}_product.npz'
        product_daily = era5_daily(product_path, ZONES[slug], calculate_normals=False)[0] if product_path.exists() else {}
        product_hours = hourly_values(product_path) if product_path.exists() else {}
        selected = live[station_id]
        shift = selected['shift']
        uncertain = station.get('geolocation_uncertain', False)
        station_days = []
        site_metadata[slug] = {'station': station_id, 'latitude': station['lat'], 'longitude': station['lon'],
            'geolocation_uncertain': uncertain, 'proxy_note': station.get('note', station.get('setting', '')),
            'target_distance_km': station.get('target_distance_km'), 'hourly_sources': [], 'rejected_days_location': 0,
            'available_climatology_days_by_month': [sum(date in published_days and int(date[5:7]) == month
                for date in daily) for month in range(1,13)]}
        for (source_slug, source_id), source_locations in locations.items():
            if source_slug == slug:
                distinct = {json.dumps(location, sort_keys=True) for location in source_locations}
                site_metadata[slug]['hourly_sources'].extend({'hourly_station': source_id, **json.loads(location)}
                    for location in sorted(distinct))
        for (observed_slug, date), observation in observed.items():
            if observed_slug != slug or date not in daily:
                continue
            source_locations = locations[(slug, observation['hourly_station'])]
            separation = max(distance_km(station['lat'], station['lon'], location['LATITUDE'], location['LONGITUDE'])
                             for location in source_locations)
            if separation > 5:
                site_metadata[slug]['rejected_days_location'] += 1
                continue
            utc_hours = [int(pd.Timestamp(stamp).timestamp() // 3600)
                         for stamp in observation['temperature_utc_hours'].split(';')]
            if not all(hour in native_hours for hour in utc_hours):
                continue
            samples = [native_hours[hour] for hour in utc_hours]
            local_hours = [pd.Timestamp(hour * 3600, unit='s', tz='UTC').tz_convert(ZONES[slug]).hour for hour in utc_hours]
            daytime_samples = [value for value, hour in zip(samples, local_hours) if 10 <= hour <= 20]
            features = {'sampled_high': (max(samples), observation['sampled_high_c']),
                        'sampled_low': (min(samples), observation['sampled_low_c']),
                        'daytime_high': (max(daytime_samples) if daytime_samples else None, observation['sampled_daytime_high_c']),
                        'air21': (daily[date]['air21'], observation['temperature21_c'])}
            for element, (raw, truth) in features.items():
                if truth == '' or not finite(raw) or not finite(truth):
                    continue
                if element != 'air21' and int(observation['temperature_hours']) < 18:
                    continue
                if element == 'daytime_high' and int(observation['daytime_hours']) < 9:
                    continue
                predictions = {'raw_era5_c': raw}
                if date in product_daily:
                    if element == 'air21':
                        predictions['product_algorithm_c'] = product_daily[date]['air21'] + shift
                    elif all(hour in product_hours for hour in utc_hours):
                        product_samples = [product_hours[hour] for hour in utc_hours]
                        if element == 'daytime_high':
                            product_samples = [value for value, hour in zip(product_samples, local_hours) if 10 <= hour <= 20]
                        predictions['product_algorithm_c'] = (min(product_samples) if element == 'sampled_low' else max(product_samples)) + shift
                month_index = int(date[5:7]) - 1
                native_range = daily[date]['TMAX'] - daily[date]['TMIN']
                for name, grid in grids.items():
                    values = grid.get(station_id, {})
                    if 'lat' in values and distance_km(station['lat'], station['lon'], values['lat'], values['lon']) > 5:
                        raise ValueError(f'{name} grid sampled at different station location: {slug}')
                    for suffix in ('', '_5km'):
                        high = values.get('tmax' + suffix, [None] * 12)[month_index]
                        low = values.get('tmin' + suffix, [None] * 12)[month_index]
                        if not finite(high) or not finite(low):
                            continue
                        for variant, reference in [('', name), ('_mean_anomaly', 'modern'), ('_available_mean_anomaly', 'available')]:
                            mean_anomaly = (normals[reference]['TMEAN'][month_index] - normals[name]['TMEAN'][month_index]) if variant else 0
                            high_offset = high - normals[reference]['TMAX'][month_index] + mean_anomaly
                            low_offset = low - normals[reference]['TMIN'][month_index] + mean_anomaly
                            corrected_ranges[(slug, name + suffix + variant)][date] = (native_range + high_offset - low_offset, native_range)
                            predictions[name + suffix + '_uniform' + variant + '_c'] = raw + (high_offset + low_offset) / 2
                            input_samples = [raw] if element == 'air21' else (daytime_samples if element == 'daytime_high' else samples)
                            corrected_samples = [value + low_offset + (high_offset-low_offset) *
                                ((value-daily[date]['TMIN'])/native_range if native_range else .5) for value in input_samples]
                            predictions[name + suffix + '_high_low' + variant + '_c'] = (min(corrected_samples)
                                if element == 'sampled_low' else max(corrected_samples))
                            if reference == 'available':
                                corrected_range = native_range + high_offset - low_offset
                                method = name + suffix + '_available_ordered'
                                if corrected_range <= 0:
                                    ordering_repairs[(slug, method)].add(date)
                                    predictions[method + '_c'] = raw + (high_offset + low_offset) / 2
                                else:
                                    predictions[method + '_c'] = predictions[name + suffix + '_high_low' + variant + '_c']
                                corrected_ranges[(slug, method)][date] = (
                                    native_range if corrected_range <= 0 else corrected_range, native_range)
                        available_low = normals['available']['TMIN'][month_index]
                        available_high = normals['available']['TMAX'][month_index]
                        warming = normals['available']['TMEAN'][month_index] - normals[name]['TMEAN'][month_index]
                        target_low, target_high = low + warming, high + warming
                        ratio = (target_high-target_low)/(available_high-available_low)
                        input_samples = [raw] if element == 'air21' else (daytime_samples if element == 'daytime_high' else samples)
                        affine_samples = [target_low + ratio*(value-available_low) for value in input_samples]
                        predictions[name + suffix + '_available_affine_c'] = (min(affine_samples)
                            if element == 'sampled_low' else max(affine_samples))
                        corrected_ranges[(slug, name + suffix + '_available_affine')][date] = (native_range*ratio, native_range)
                        affine_bounds = [target_low + ratio*(daily[date][extreme]-available_low) for extreme in ('TMIN','TMAX')]
                        audit = affine_parameters.setdefault(f'{slug}/{name}{suffix}/{month_index+1}',
                            dict(ratio=ratio, target_low_c=target_low, target_high_c=target_high,
                                 available_low_c=available_low, available_high_c=available_high,
                                 minimum_corrected_hourly_c=min(affine_bounds), maximum_corrected_hourly_c=max(affine_bounds)))
                        audit['minimum_corrected_hourly_c'] = min(audit['minimum_corrected_hourly_c'], min(affine_bounds))
                        audit['maximum_corrected_hourly_c'] = max(audit['maximum_corrected_hourly_c'], max(affine_bounds))
                for source in ('prism', 'worldclim14', 'worldclim14_5km', 'chelsa'):
                    if source + '_available_ordered_c' in predictions:
                        composite_sources[f'{slug}/{month_index+1}'] = source
                        for candidate, ending in [('ordered', '_available_ordered_c'),
                                ('high_low', '_high_low_available_mean_anomaly_c'),
                                ('uniform', '_uniform_available_mean_anomaly_c'),
                                ('affine', '_available_affine_c')]:
                            predictions['composite_available_' + candidate + '_c'] = predictions[source + ending]
                        break
                else:
                    composite_sources[f'{slug}/{month_index+1}'] = 'native_era5_coarse'
                    for candidate in ('ordered', 'high_low', 'uniform', 'affine'):
                        predictions['composite_available_' + candidate + '_c'] = raw
                station_days.append({'date': date, 'element': element, 'observed_c': float(truth),
                                     'source_distance_km': separation, **predictions})
        for period, first, last in [('2016-2025', 2016, 2025), ('2019-2022', 2019, 2022)]:
            for month in range(1, 13):
                for element in ('sampled_high', 'sampled_low', 'daytime_high', 'air21'):
                    candidates = [day for day in station_days if first <= int(day['date'][:4]) <= last
                                  and int(day['date'][5:7]) == month and day['element'] == element]
                    if not candidates:
                        continue
                    methods = sorted({key for day in candidates for key in day if key.endswith('_c')} - {'observed_c'})
                    for method in methods:
                        paired = [day for day in candidates if finite(day.get(method))]
                        if not paired:
                            continue
                        complete_years, year_list = monthly_quality([day['date'] for day in paired], month)
                        accepted = set(year_list.split(';'))
                        for cohort, subset in [('diagnostic', paired), ('coverage_years', [day for day in paired if day['date'][:4] in accepted])]:
                            if not subset:
                                continue
                            truth = mean([day['observed_c'] for day in subset])
                            predicted = mean([day[method] for day in subset])
                            rows.append(dict(slug=slug, station=station_id, period=period, month=month, element=element,
                                method=method, cohort=cohort, days=len(subset), all_paired_days=len(paired),
                                complete_years=complete_years, accepted_years=year_list,
                                strict=cohort == 'coverage_years' and complete_years >= 5 and not uncertain,
                                geolocation_uncertain=uncertain,
                                maximum_source_distance_km=max(day['source_distance_km'] for day in subset),
                                target_distance_km=station.get('target_distance_km'), observed_c=truth,
                                predicted_c=predicted, error_c=predicted-truth,
                                raw_era5_same_days_c=mean([day['raw_era5_c'] for day in subset]),
                                daily_mae_c=mean([abs(day[method]-day['observed_c']) for day in subset]),
                                published_airhigh_c=selected['monthly_airhigh_c'][month-1] if element=='daytime_high' else None,
                                published_felt21_provenance_c=selected['monthly_felt21_c'][month-1] if element=='air21' else None))
        print('Compared hourly', slug, len(station_days), flush=True)
    if rows:
        with (args.output / 'hourly_comparison.csv').open('w') as stream:
            writer = csv.DictWriter(stream, fieldnames=list(rows[0])); writer.writeheader(); writer.writerows(rows)
    summary = {}
    for period in ('2016-2025', '2019-2022'):
        for cohort in ('strict', 'diagnostic'):
            subset = [row for row in rows if row['period'] == period and (row['cohort'] == 'diagnostic' if cohort == 'diagnostic' else row['strict'])]
            for element in sorted({row['element'] for row in subset}):
                for method in sorted({row['method'] for row in subset if row['element'] == element}):
                    matched = [row for row in subset if row['element'] == element and row['method'] == method]
                    summary[f'{period}/{cohort}/{element}/{method}'] = dict(months=len(matched),
                        stations=len({row['station'] for row in matched}), mae_c=mean([abs(row['error_c']) for row in matched]),
                        bias_c=mean([row['error_c'] for row in matched]),
                        raw_mae_same_months_c=mean([abs(row['raw_era5_same_days_c']-row['observed_c']) for row in matched]))
    report = {'written_by': 'AI, GPT-6.1 Sol, 2026.10.17', 'seconds': time.monotonic()-started,
        'method': 'No fitting to evaluation observations. Adjacent input-year boundary duplicates keep fullest day. Actual local21 air temperature; extrema use identical station-observed whole UTC hours and >=18 temperaturehours/day (daytime additionally >=9 of10..20). Reports within30min of hour. Uniform averages monthly max/min offsets; high_low interpolates offsets by (raw hourlyT-raw dailyTmin)/(raw dailyTmax-raw dailyTmin). Extrema are recomputed from corrected hourly samples, including inverted ranges without clamping. Available_ordered uses available-mean dual offsets, switching the whole day to their averaged uniform offset when corrected daily high<=low; this global rule preserves the raw daily range on repaired days. Available_affine maps available-period monthly native Tmin/Tmax to fine Tmin/Tmax plus common mean warming, scaling hourly anomalies by the monthly range ratio; no tuning or clamp. Reference-period variant uses historical ERA5 extrema normals. Mean_anomaly uses modern2016-25 extrema normals plus modern-minus-source-period meanT change. Available_mean_anomaly uses exact published chunk dates for modern normals instead, matching the feasible global stream-read of existing chunks. Diagnostic means use all paired dates; coverage_years means exclude every month-year below80% calendar-day coverage. Strict additionally requires >=5 accepted years, coordinates<=5km and certain geolocation; Chania diagnostic. Published high is a different historical sample; published felt21 is never compared with observed airT.',
        'sites': site_metadata, 'unavailable': unavailable, 'summary': summary,
        'published_meta': live_document.get('live_meta'),
        'published_capture_at_utc': live_document.get('captured_at_utc'),
        'affine_parameters': affine_parameters,
        'composite_sources': composite_sources,
        'ordering_repairs': {f'{slug}/{method}': {'repaired_days': len(ordering_repairs[(slug, method)]),
            'audited_days': len(values)} for (slug, method), values in corrected_ranges.items()
            if method.endswith('_available_ordered')},
        'non_positive_corrected_ranges': {f'{slug}/{method}': {'days': len(values),
            'high_low_non_positive_days': sum(value[0] <= 0 for value in values.values()),
            'minimum_corrected_range_c': min(value[0] for value in values.values()),
            'uniform_non_positive_days': sum(value[1] <= 0 for value in values.values())}
            for (slug, method), values in corrected_ranges.items()}}
    (args.output / 'hourly_comparison_summary.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps({'seconds': report['seconds'], 'rows': len(rows), 'unavailable': unavailable}), flush=True)


if __name__ == '__main__':
    main()
