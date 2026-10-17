#!/usr/bin/env python3
"""Download public GHCNh station/year Parquet and retain local-hour observations.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
Run with Python + pyarrow: hourly_stations.py OUTPUT [--years 2016:2025].
Inputs and products stay in OUTPUT; no published climate files are changed.
Hourly-sampled extrema are not observing-instrument daily extremes. Coverage is
reported explicitly; only >=18-hour days enter the strict monthly summaries.
"""
import argparse
import collections
import concurrent.futures
import csv
import datetime as dt
import json
import math
import pathlib
import subprocess
import time
import zoneinfo

import pyarrow.parquet as parquet

BASE = 'https://www.ncei.noaa.gov/oa/global-historical-climatology-network'
DOCUMENTATION = BASE + '/hourly/doc/ghcnh_DOCUMENTATION.pdf'
# Prefer instrument/site identity over nearest station of a different terrain.
TARGETS = [
    ('lima', ['PEI0000SPIM', 'PEU00084628', 'PEM00084628'], 'America/Lima'),
    ('valparaiso', ['CII0000SCER'], 'America/Santiago'),
    ('chania', ['GRW00033209'], 'Europe/Athens'),
    ('bangkok', ['THI0000VTBD'], 'Asia/Bangkok'),
    ('cape_town', ['SFM00068999'], 'Africa/Johannesburg'),
    ('lisbon', ['POM00008535'], 'Europe/Lisbon'),
    ('funchal', ['POM00008521'], 'Atlantic/Madeira'),
    ('las_palmas', ['SPM00060029'], 'Atlantic/Canary'),
    ('nairobi', ['KEI0000HKJK'], 'Africa/Nairobi'),
    ('agadir', ['MOI0000GMAA'], 'Africa/Casablanca'),
]
LEGACY_STATIONS = {
    'lima': '84628099999', 'cape_town': '68816099999',
    'funchal': '08522099999', 'las_palmas': '60030099999',
    'valparaiso': '85543099999', 'agadir': '60252099999',
}
QUALITY_ACCEPTED = {None, '', '0', '1', '4', '5', '9'}
VARIABLES = ['temperature', 'dew_point_temperature', 'wind_speed']
COLUMNS = ['STATION', 'DATE', 'LATITUDE', 'LONGITUDE', 'ELEVATION'] + [
    field for variable in VARIABLES for field in (variable, variable + '_Quality_Code')]


def download(url, target, missing_ok=False):
    if target.exists() and target.stat().st_size:
        return target
    partial = target.with_name(target.name + '.part')
    result = subprocess.run(['curl', '--retry', '2', '--connect-timeout', '20',
                             '--max-time', '120', '-fsS', url, '-o', str(partial)],
                            capture_output=True, text=True)
    if result.returncode:
        if missing_ok and result.returncode == 22:
            return None
        raise RuntimeError(f'{url}: {result.stderr.strip()}')
    partial.replace(target)
    return target


def average(values):
    return round(sum(values) / len(values), 4) if values else None


def process_station_year(job):
    output, slug, aliases, timezone, year = job
    selected = None
    attempted = []
    for station in aliases:
        url = f'{BASE}/hourly/access/by-year/{year}/parquet/GHCNh_{station}_{year}.parquet'
        target = output / 'raw' / f'GHCNh_{station}_{year}.parquet'
        attempted.append(url)
        if download(url, target, missing_ok=True):
            selected = (station, target, url)
            break
    legacy_rows = None
    if slug in LEGACY_STATIONS:
        # Retain NOAA's denser legacy reports where modern hourly aliases are sparse.
        station = LEGACY_STATIONS[slug]
        url = f'https://noaa-global-hourly-pds.s3.amazonaws.com/{year}/{station}.csv'
        target = output / 'raw' / f'ISD_{station}_{year}.csv'
        attempted.append(url)
        if download(url, target, missing_ok=True):
            selected = (station, target, url)
            legacy_rows = []
            with target.open() as stream:
                for record in csv.DictReader(stream):
                    normalized = {key: record[key] for key in ['DATE', 'LATITUDE', 'LONGITUDE', 'ELEVATION']}
                    for variable, field, position in [('temperature', 'TMP', 0), ('dew_point_temperature', 'DEW', 0), ('wind_speed', 'WND', 3)]:
                        parts = record[field].split(',')
                        raw_value, quality = parts[position:position + 2]
                        normalized[variable] = None if abs(int(raw_value)) == 9999 else int(raw_value) / 10
                        normalized[variable + '_Quality_Code'] = quality
                    legacy_rows.append(normalized)
    if selected is None:
        return {'slug': slug, 'year': year, 'status': 'no_file', 'attempted': attempted}, []
    station, target, url = selected
    input_rows = legacy_rows if legacy_rows is not None else parquet.read_table(target, columns=COLUMNS).to_pylist()
    observations = collections.defaultdict(dict)
    quality_counts = collections.Counter()
    location = None
    for row in input_rows:
        stamp = dt.datetime.fromisoformat(row['DATE']).replace(tzinfo=dt.timezone.utc)
        if not 2016 <= stamp.year <= 2025:
            continue
        location = {key: float(row[key]) for key in ['LATITUDE', 'LONGITUDE', 'ELEVATION']}
        # Multiple reports in the same UTC hour: use the closest to the hour.
        rounded_hour = (stamp + dt.timedelta(minutes=30)).replace(minute=0, second=0)
        distance = abs((stamp - rounded_hour).total_seconds())
        for variable in VARIABLES:
            value, quality = row[variable], row[variable + '_Quality_Code']
            quality_counts[f'{variable}:{quality}'] += 1
            if value is None or quality not in QUALITY_ACCEPTED:
                continue
            previous = observations[rounded_hour].get(variable)
            if previous is None or distance < previous[1]:
                observations[rounded_hour][variable] = (value, distance, stamp.isoformat())
    zone = zoneinfo.ZoneInfo(timezone)
    daily = collections.defaultdict(list)
    for stamp, fields in sorted(observations.items()):
        if 'temperature' not in fields:
            continue
        local = stamp.astimezone(zone)
        daily[local.date()].append((local.hour, fields))
    rows = []
    for date, samples in sorted(daily.items()):
        all_temperatures = [fields['temperature'][0] for _, fields in samples]
        daytime = [fields['temperature'][0] for hour, fields in samples if 10 <= hour <= 20]
        at21 = next((fields for hour, fields in samples if hour == 21), None)
        temperature21 = at21['temperature'][0] if at21 else None
        dew21 = at21.get('dew_point_temperature', (None,))[0] if at21 else None
        wind21 = at21.get('wind_speed', (None,))[0] if at21 else None
        felt21 = None
        if temperature21 is not None and dew21 is not None and wind21 is not None:
            dew = min(dew21, temperature21)
            vapour = 6.105 * math.exp(17.27 * dew / (237.7 + dew))
            felt21 = temperature21 + .33 * vapour - .70 * wind21 - 4
        expected_hours = round((dt.datetime.combine(date + dt.timedelta(days=1), dt.time(), zone).astimezone(dt.timezone.utc)
                                - dt.datetime.combine(date, dt.time(), zone).astimezone(dt.timezone.utc)).total_seconds() / 3600)
        rows.append({'slug': slug, 'hourly_station': station, 'date': date.isoformat(),
                     'sampled_high_c': max(all_temperatures), 'sampled_low_c': min(all_temperatures),
                     'sampled_daytime_high_c': max(daytime) if daytime else None,
                     'temperature21_c': temperature21, 'dew21_c': dew21, 'wind21_ms': wind21,
                     'felt21_c': round(felt21, 4) if felt21 is not None else None,
                     'temperature_hours': len(samples), 'expected_hours': expected_hours,
                     'daytime_hours': len(daytime),
                     'temperature_utc_hours': ';'.join((dt.datetime.fromisoformat(fields['temperature'][2])
                         + dt.timedelta(minutes=30)).replace(minute=0, second=0).isoformat()
                         for _, fields in samples)})
    provenance = {'slug': slug, 'year': year, 'status': 'ok', 'hourly_station': station,
                  'url': url, 'bytes': target.stat().st_size, 'raw_rows': len(input_rows),
                  'location': location, 'days': len(rows), 'strict_18hour_days': sum(r['temperature_hours'] >= 18 for r in rows),
                  'days21': sum(r['temperature21_c'] is not None for r in rows),
                  'quality_counts': dict(quality_counts)}
    return provenance, rows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('output', type=pathlib.Path)
    parser.add_argument('--years', default='2016:2025')
    parser.add_argument('--workers', type=int, default=4)
    args = parser.parse_args()
    first, last = map(int, args.years.split(':'))
    output = args.output
    (output / 'raw').mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    jobs = [(output, slug, aliases, zone, year) for slug, aliases, zone in TARGETS for year in range(first, last + 1)]
    rows, provenance = [], []
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.workers) as executor:
        for info, days in executor.map(process_station_year, jobs):
            provenance.append(info)
            rows.extend(days)
            print(info['slug'], info['year'], info['status'], info.get('strict_18hour_days', 0), info.get('days21', 0), flush=True)
    distinct_days = {}
    for row in rows:
        if not first <= int(row['date'][:4]) <= last:
            continue
        key = row['slug'], row['date']
        if key not in distinct_days or row['temperature_hours'] > distinct_days[key]['temperature_hours']:
            distinct_days[key] = row
    rows = sorted(distinct_days.values(), key=lambda row: (row['slug'], row['date']))
    if rows:
        with (output / 'daily_sampled_extrema.csv').open('w') as stream:
            writer = csv.DictWriter(stream, fieldnames=list(rows[0]))
            writer.writeheader()
            writer.writerows(rows)
    monthly = []
    for slug, _, _ in TARGETS:
        for month in range(1, 13):
            selected = [r for r in rows if r['slug'] == slug and int(r['date'][5:7]) == month]
            strict = [r for r in selected if r['temperature_hours'] >= 18]
            at21 = [r for r in selected if r['temperature21_c'] is not None]
            monthly.append({'slug': slug, 'month': month, 'days_any': len(selected), 'days_18hour': len(strict),
                            'sampled_high_c': average([r['sampled_high_c'] for r in strict]),
                            'sampled_low_c': average([r['sampled_low_c'] for r in strict]),
                            'days21': len(at21), 'temperature21_c': average([r['temperature21_c'] for r in at21]),
                            'felt21_c': average([r['felt21_c'] for r in at21 if r['felt21_c'] is not None])})
    report = {'written_by': 'AI, GPT-6.1 Sol, 2026.10.17', 'documentation': DOCUMENTATION,
              'accepted_quality_codes': list(sorted(str(x) for x in QUALITY_ACCEPTED)),
              'method': 'UTC timestamps; reports nearest whole UTC hour within30min; local IANA calendar. Adjacent input-year local-date overlaps retain the fuller day once. Sampled extrema need18 distinct temperaturehours for strict monthly means. Exact local21 hour (nearest report within30min), dew+wind when available. Hourly maxima/minima understate instrument extrema. Alternative stations never silently combined with GHCNd sites.',
              'years': [first, last], 'seconds': time.monotonic() - started, 'files': provenance, 'monthly': monthly}
    (output / 'summary.json').write_text(json.dumps(report, indent=2) + '\n')
    print('seconds', round(report['seconds'], 2), 'bytes', sum(p.get('bytes', 0) for p in provenance), flush=True)


if __name__ == '__main__':
    main()
