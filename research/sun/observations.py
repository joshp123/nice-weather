#!/usr/bin/env python3
"""Independent airport cloud evidence. Written by AI, GPT-6.1 Sol, 2026-10-17.

PSV mode needs only Python's standard library; Parquet mode uses an existing
DuckDB CLI. Cloud cover is NOT DNI sunshine: a clear
part of the sky need not contain the sun, and thin cloud can transmit direct sun.
This script deliberately preserves that distinction in its output field names.
"""
import argparse
import calendar
import concurrent.futures
import csv
import datetime as dt
import gzip
import hashlib
import http.client
import json
import math
import pathlib
import subprocess
import time
import urllib.error
import urllib.request
from collections import Counter, defaultdict
from zoneinfo import ZoneInfo

ROOT = pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/sun')
BASE = 'https://noaa-ghcnh-pds.s3.us-east-1.amazonaws.com/'
STATIONS = [
    ('Lima', 'PEI0000SPIM', 'America/Lima'),
    ('Lima Callao', 'PEU00084628', 'America/Lima'),
    ('Antofagasta', 'CII0000SCFA', 'America/Santiago'),
    ('Iquique', 'CII0000SCDA', 'America/Santiago'),
    ('Valparaiso', 'CIN00320041', 'America/Santiago'),
    ('Valparaiso lighthouse', 'CIN00330002', 'America/Santiago'),
    ('Swakopmund', 'WAI0000FYSM', 'Africa/Windhoek'),
    ('Walvis Bay', 'WAI0000FYWB', 'Africa/Windhoek'),
    ('San Francisco', 'USW00023234', 'America/Los_Angeles'),
    ('Malaga', 'SPU0006155A', 'Europe/Madrid'),
    ('Seville', 'SPMU0098391', 'Europe/Madrid'),
    ('Cape Town', 'SFM00068999', 'Africa/Johannesburg'),
    ('Tenerife south', 'SPN000C429I', 'Atlantic/Canary'),
    ('San Diego', 'USW00023188', 'America/Los_Angeles'),
    ('Rotterdam', 'NLI0000EHRD', 'Europe/Amsterdam'),
    ('Singapore', 'SNI0000WSSS', 'Asia/Singapore'),
]
GOOD_QC = {'', '0', '1', '4', '5', '9'}


def aemet_normals():
    """Download primary station sunshine normals; no API/account is needed."""
    directory = ROOT / 'aemet'
    directory.mkdir(exist_ok=True)
    stations = [
        ('Malaga', 'malaga-aeropuerto', 'and', '6155A', 36.6661111111),
        ('Seville', 'sevilla-aeropuerto', 'and', '5783', 37.4166666667),
        ('Tenerife south', 'tenerife-sur-aeropuerto', 'coo', 'C429I', 28.047),
    ]
    rows = []
    manifest = []
    base = 'https://www.aemet.es/en/serviciosclimaticos/datosclimatologicos/'
    for place, slug, region, station, latitude in stations:
        url = base + f'valoresclimatologicos_{slug}.csv?k={region}&l={station}'
        path = directory / (slug + '.csv')
        if not path.exists():
            path.write_bytes(urllib.request.urlopen(url, timeout=30).read())
        raw = path.read_bytes()
        manifest.append({'place': place, 'url': url, 'sha256': hashlib.sha256(raw).hexdigest()})
        lines = raw.decode('latin-1').splitlines()[2:]
        for month, observation in enumerate(list(csv.DictReader(lines))[:12], 1):
            possible = []
            for day in range(1, calendar.monthrange(2001, month)[1] + 1):
                date = dt.date(2001, month, day)
                gamma = 2 * math.pi / 365 * (date.timetuple().tm_yday - 1)
                declination = (.006918 - .399912 * math.cos(gamma) + .070257 * math.sin(gamma)
                               - .006758 * math.cos(2 * gamma) + .000907 * math.sin(2 * gamma)
                               - .002697 * math.cos(3 * gamma) + .00148 * math.sin(3 * gamma))
                lat = math.radians(latitude)
                argument = math.cos(math.radians(90.833)) / (math.cos(lat) * math.cos(declination)) - math.tan(lat) * math.tan(declination)
                possible.append(24 / math.pi * math.acos(argument))
            hours = float(observation['I'])
            rows.append({'place': place, 'station_id': station, 'first_year': 1981, 'last_year': 2010,
                         'month': month, 'all_day_sunshine_hours': hours,
                         'estimated_all_daylight_hours': round(sum(possible), 2),
                         'all_day_sunshine_fraction': round(hours / sum(possible), 5),
                         'source': 'AEMET', 'url': base + f'valoresclimatologicos?k={region}&l={station}'})
    write_csv(ROOT / 'aemet-monthly.csv', rows, list(rows[0]))
    (directory / 'downloads.json').write_text(json.dumps(manifest, indent=2) + '\n')
    legal_path = directory / 'aemet-legal-2026-10-17.html'
    if not legal_path.exists():
        legal_path.write_bytes(urllib.request.urlopen('https://www.aemet.es/en/nota_legal', timeout=30).read())


def download(station_year):
    station, year, file_format = station_year
    key = f'hourly/access/by-year/{year}/{file_format}/GHCNh_{station}_{year}.{file_format}'
    path = ROOT / 'ghcnh/raw' / pathlib.Path(key).name
    url = BASE + key
    if not path.exists():
        for attempt in range(3):
            try:
                with urllib.request.urlopen(url, timeout=90) as response:
                    data = response.read()
                temporary = path.with_suffix('.part')
                temporary.write_bytes(data)
                temporary.rename(path)
                break
            except urllib.error.HTTPError as error:
                if error.code == 404:
                    return {'station_id': station, 'year': year, 'url': url, 'status': '404 missing'}
                raise
            except (http.client.RemoteDisconnected, urllib.error.URLError, TimeoutError):
                if attempt == 2:
                    raise
                time.sleep(2)
    data = path.read_bytes()
    return {'station_id': station, 'year': year, 'url': url, 'status': 'downloaded',
            'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest()}


def coverage_code(value):
    if ':' in value:
        value = value.split(':')[-1]
    try:
        number = int(value)
    except ValueError:
        return None
    if 0 <= number <= 8:
        return number
    # An obscured sky is a separate state, not a measured number of cloud oktas.
    return None


def sky_observation(row):
    value = row.get('sky_condition', '').split(';')[0]
    quality = row.get('sky_condition_Quality_Code', '').split(';')[0]
    if quality in GOOD_QC:
        code = coverage_code(value)
        if code is not None:
            return code, 'total_sky', quality, value
        if value == '09':
            return 8, 'sky_obscured', quality, value
    for prefix, source in [('sky_cover_summation_', 'summation'), ('sky_cover_layer_', 'max_discrete_layer')]:
        values = []
        for index in range(1, 5):
            field = prefix + str(index)
            quality = row.get(field + '_Quality_Code', '')
            if quality not in GOOD_QC:
                continue
            raw = row.get(field, '')
            parts = raw.split(';')
            code = coverage_code(parts[-1]) if len(parts) > 1 and parts[-1] else coverage_code(parts[0])
            if ':09' in raw:
                return 8, 'sky_obscured', quality, raw
            if code is not None:
                values.append((code, quality, raw))
        if values:
            code, quality, raw = max(values)
            return code, source, quality, raw
    return None


def daylight_hours(date, latitude, longitude, zone):
    """Same 10:00-to-sunset slot definition as the product, no ERA5 input."""
    noon = dt.datetime.combine(date, dt.time(12), zone)
    offset = noon.utcoffset().total_seconds() / 3600
    gamma = 2 * math.pi / 365 * (date.timetuple().tm_yday - 1)
    equation = 229.18 * (.000075 + .001868 * math.cos(gamma) - .032077 * math.sin(gamma)
                         - .014615 * math.cos(2 * gamma) - .040849 * math.sin(2 * gamma))
    declination = (.006918 - .399912 * math.cos(gamma) + .070257 * math.sin(gamma)
                   - .006758 * math.cos(2 * gamma) + .000907 * math.sin(2 * gamma)
                   - .002697 * math.cos(3 * gamma) + .00148 * math.sin(3 * gamma))
    latitude = math.radians(latitude)
    argument = ((math.cos(math.radians(90.833)) / (math.cos(latitude) * math.cos(declination)))
                - math.tan(latitude) * math.tan(declination))
    if argument >= 1:
        return 1
    if argument <= -1:
        return 11
    sunset = (720 - 4 * longitude - equation + 4 * math.degrees(math.acos(argument))) / 60 + offset
    return min(11, max(1, math.ceil(sunset) - 10))


def write_csv(path, rows, fields):
    with path.open('w', newline='') as stream:
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        writer.writerows(rows)


def station_rows(path, duckdb_bin):
    if path.suffix == '.psv':
        with path.open() as stream:
            yield from csv.DictReader(stream, delimiter='|')
        return
    columns = ['DATE', 'sky_condition', 'sky_condition_Quality_Code']
    for prefix in ['sky_cover_layer_', 'sky_cover_summation_']:
        for index in range(1, 5):
            columns.extend([prefix + str(index), prefix + str(index) + '_Quality_Code'])
    query = 'SELECT ' + ','.join('"' + name + '"' for name in columns)
    query += " FROM read_parquet('" + str(path).replace("'", "''") + "')"
    with subprocess.Popen([duckdb_bin, '-csv', '-c', query], stdout=subprocess.PIPE, text=True) as process:
        yield from csv.DictReader(process.stdout)
        if process.wait():
            raise RuntimeError(f'Could not read {path}')


def summarize(years, duckdb_bin=None):
    metadata_path = ROOT / 'ghcnh/provenance/stations.csv'
    if not metadata_path.exists():
        metadata_path.write_bytes(urllib.request.urlopen(BASE + 'hourly/doc/ghcnh-station-list.csv').read())
    metadata = {row['GHCN_ID']: row for row in csv.DictReader(metadata_path.open())}
    monthly = []
    period = f'{min(years)}-{max(years)}'
    hourly_path = ROOT / f'observations-{period}-hourly.csv.gz'
    with gzip.open(hourly_path, 'wt', newline='') as stream:
        fields = ['place', 'station_id', 'utc', 'local_date', 'local_hour', 'cloud_oktas', 'cloud_metric_source', 'cloud_quality', 'raw_cloud_code']
        writer = csv.DictWriter(stream, fieldnames=fields)
        writer.writeheader()
        for place, station, zone_name in STATIONS:
            zone = ZoneInfo(zone_name)
            station_metadata = metadata[station]
            latitude, longitude = float(station_metadata['LATITUDE']), float(station_metadata['LONGITUDE'])
            slots = {}
            expected_slots = Counter()
            for year in years:
                date = dt.date(year, 1, 1)
                while date.year == year:
                    expected_slots[date.month] += daylight_hours(date, latitude, longitude, zone)
                    date += dt.timedelta(days=1)
                path = ROOT / 'ghcnh/raw' / f'GHCNh_{station}_{year}.psv'
                parquet = path.with_suffix('.parquet')
                if parquet.exists() and duckdb_bin:
                    path = parquet
                if not path.exists():
                    continue
                for row in station_rows(path, duckdb_bin):
                    utc = dt.datetime.fromisoformat(row['DATE']).replace(tzinfo=dt.timezone.utc)
                    local = utc.astimezone(zone)
                    if local.year not in years or not 10 <= local.hour < 21:
                        continue
                    if local.hour >= 10 + daylight_hours(local.date(), latitude, longitude, zone):
                        continue
                    sky = sky_observation(row)
                    if sky is None:
                        continue
                    # One report per local clock hour prevents frequent SPECI reports
                    # during bad weather from receiving disproportionate weight.
                    key = (local.date(), local.hour)
                    rank = (sky[1] != 'total_sky', abs(local.minute - 30), local.minute)
                    if key not in slots or rank < slots[key][0]:
                        slots[key] = (rank, sky, utc.isoformat())
            by_month = defaultdict(list)
            for (date, hour), (_, sky, utc) in sorted(slots.items()):
                writer.writerow(dict(zip(fields, [place, station, utc, str(date), hour, *sky])))
                by_month[date.month].append((date, hour, sky))
            for month in range(1, 13):
                observations = by_month[month]
                count = len(observations)
                source_counts = Counter(sky[1] for _, _, sky in observations)
                cloud = [sky[0] for _, _, sky in observations]
                monthly.append({
                    'place': place, 'station_id': station, 'station_name': station_metadata['NAME'],
                    'latitude': latitude, 'longitude': longitude, 'zone': zone_name,
                    'first_year': min(years), 'last_year': max(years), 'month': month,
                    'daylight_slots': expected_slots[month], 'valid_cloud_slots': count,
                    'valid_days': len({date for date, _, _ in observations}),
                    'valid_year_count': len({date.year for date, _, _ in observations}),
                    'first_observation_date': min((str(date) for date, _, _ in observations), default=''),
                    'last_observation_date': max((str(date) for date, _, _ in observations), default=''),
                    'coverage_fraction': round(count / expected_slots[month], 5),
                    'observed_unobscured_sky_fraction': round(1 - sum(cloud) / (8 * count), 5) if count else '',
                    'clear_or_few_share': round(sum(value <= 2 for value in cloud) / count, 5) if count else '',
                    'broken_or_overcast_share': round(sum(value >= 5 for value in cloud) / count, 5) if count else '',
                    'overcast_or_obscured_share': round(sum(value == 8 for value in cloud) / count, 5) if count else '',
                    'total_sky_slots': source_counts['total_sky'], 'summation_slots': source_counts['summation'],
                    'max_discrete_layer_slots': source_counts['max_discrete_layer'],
                    'obscured_sky_slots': source_counts['sky_obscured'],
                })
            print(place, 'valid daylight cloud slots', len(slots), flush=True)
    write_csv(ROOT / f'observations-{period}-monthly.csv', monthly, list(monthly[0]))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--years', nargs='+', type=int, default=list(range(2016, 2025)))
    parser.add_argument('--workers', type=int, default=8)
    parser.add_argument('--summarize-only', action='store_true')
    parser.add_argument('--duckdb-bin', help='Existing DuckDB CLI enables much smaller Parquet downloads')
    parser.add_argument('--aemet-only', action='store_true', help='Download the independent 1981–2010 Spanish sunshine normals')
    args = parser.parse_args()
    if args.aemet_only:
        aemet_normals()
        return
    for directory in ['ghcnh/raw', 'ghcnh/provenance']:
        (ROOT / directory).mkdir(parents=True, exist_ok=True)
    if not args.summarize_only:
        file_format = 'parquet' if args.duckdb_bin else 'psv'
        jobs = [(station, year, file_format) for _, station, _ in STATIONS for year in args.years]
        with concurrent.futures.ThreadPoolExecutor(args.workers) as pool:
            provenance = list(pool.map(download, jobs))
        manifest = ROOT / f'ghcnh/provenance/downloads-{min(args.years)}-{max(args.years)}.json'
        manifest.write_text(json.dumps(provenance, indent=2) + '\n')
    summarize(args.years, args.duckdb_bin)


if __name__ == '__main__':
    main()
