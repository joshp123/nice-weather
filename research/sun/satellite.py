"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Anonymous NSRDB point extraction. Run inside the project's devenv with uv:
UV_CACHE_DIR=/tmp/nwf-uv-cache devenv shell -- uv run --with h5py --with fsspec --with aiohttp python research/sun/satellite.py --domain goes --year 2022 --place Lima
Only the metadata and requested HDF5 chunks are downloaded, never whole files.
"""
import argparse
import datetime as dt
import json
from pathlib import Path
import time
import concurrent.futures
import urllib.request
import urllib.error
import http.client
from zoneinfo import ZoneInfo

import fsspec
import h5py
import numpy as np

ROOT = Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/sun/satellite')
PLACES = {
    'Lima': (-12.0464, -77.0428, 'America/Lima', 'goes'),
    'Antofagasta': (-23.6509, -70.3975, 'America/Santiago', 'goes'),
    'Valparaiso': (-33.0472, -71.6127, 'America/Santiago', 'goes'),
    'San Francisco': (37.7749, -122.4194, 'America/Los_Angeles', 'goes'),
    'San Diego': (32.7157, -117.1611, 'America/Los_Angeles', 'goes'),
    'Swakopmund': (-22.6784, 14.5266, 'Africa/Windhoek', 'meteosat'),
    'Cape Town': (-33.9249, 18.4241, 'Africa/Johannesburg', 'meteosat'),
    'Malaga': (36.7213, -4.4214, 'Europe/Madrid', 'meteosat'),
    'Seville': (37.3891, -5.9845, 'Europe/Madrid', 'meteosat'),
    'Rotterdam': (51.9244, 4.4777, 'Europe/Amsterdam', 'meteosat'),
    'Tenerife south': (28.055, -16.731, 'Atlantic/Canary', 'meteosat'),
    'Lima ERA5 point': (-12.0, -77.0, 'America/Lima', 'goes'),
    'San Francisco ERA5 point': (38.0, -122.5, 'America/Los_Angeles', 'goes'),
    'Swakopmund ERA5 point': (-22.5, 14.5, 'Africa/Windhoek', 'meteosat'),
}


class ChunkLocationFile:
    """Let HDF5 locate raw chunks, then download their ranges concurrently."""
    def __init__(self, remote):
        self.remote = remote
        self.locating_size = None
        self.locations = []

    def __getattr__(self, name):
        return getattr(self.remote, name)

    def read(self, size=-1):
        if self.locating_size is not None and size == self.locating_size:
            offset = self.remote.tell()
            self.locations.append((offset, size))
            self.remote.seek(offset+size)
            return bytes(size)
        return self.remote.read(size)

    def readinto(self, target):
        payload = self.read(len(target))
        target[:len(payload)] = payload
        return len(payload)


def extract_dni_chunks(dataset, site_index, url, remote):
    """Use indexed direct reads; H5Dget_chunk_info scans the whole tree."""
    time_chunk, site_chunk = dataset.chunks
    site_start = site_index//site_chunk*site_chunk
    result = np.empty(dataset.shape[0], dtype=dataset.dtype)
    ranges = []
    for time_start in range(0, dataset.shape[0], time_chunk):
        if dataset.compression is not None:
            raise ValueError('This extractor expects the NSRDB uncompressed archive')
        remote.locating_size = time_chunk*site_chunk*dataset.dtype.itemsize
        remote.locations = []
        dataset.id.read_direct_chunk((time_start, site_start))
        remote.locating_size = None
        if len(remote.locations) != 1:
            raise ValueError(f'Expected one raw chunk location, got {remote.locations}')
        ranges.append((time_start, *remote.locations[0]))
    print('located', site_index, len(ranges), 'chunks', flush=True)
    def fetch(item):
        time_start, offset, size = item
        request = urllib.request.Request(url, headers={'Range': f'bytes={offset}-{offset+size-1}'})
        for attempt in range(3):
            try:
                with urllib.request.urlopen(request, timeout=120) as response:
                    payload = response.read()
                break
            except (http.client.IncompleteRead, urllib.error.URLError, TimeoutError):
                if attempt == 2:
                    raise
                time.sleep(2*(attempt+1))
        if len(payload) != size:
            raise ValueError(f'Unexpected HTTP range length {len(payload)} versus {size}')
        values = np.frombuffer(payload, dtype=dataset.dtype).reshape(time_chunk, site_chunk)
        return time_start, values[:, site_index-site_start].copy()
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        for time_start, values in pool.map(fetch, ranges):
            length = min(len(values), len(result)-time_start)
            result[time_start:time_start+length] = values[:length]
            print('chunk', site_index, time_start, flush=True)
    return result


def extract(domain, year, only_places=None):
    if domain == 'goes':
        key = f'GOES/full_disc/v4.0.0/nsrdb_full_disc_irradiance_{year}.h5'
    elif domain == 'msg':
        key = f'msg/v1.0.0/msg_irradiance_{year}.h5'
    else:
        key = f'meteosat/meteosat_irradiance_{year}.h5'
    url = 'https://nrel-pds-nsrdb.s3.amazonaws.com/' + key
    ROOT.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    with fsspec.open(url, block_size=2**16, cache_type='blockcache', cache_options={'maxblocks':1024}).open() as remote:
        recording = ChunkLocationFile(remote)
        with h5py.File(recording) as hdf:
            print(domain, year, {name: (hdf[name].shape, hdf[name].chunks) for name in hdf}, flush=True)
            coordinate_path = ROOT / f'{domain}-coordinates.npy'
            if 'coordinates' not in hdf:
                coordinates = None
            elif not coordinate_path.exists():
                coordinates = hdf['coordinates'][:]
                np.save(coordinate_path, coordinates)
            else:
                coordinates = np.load(coordinate_path)
            timestamps = hdf['time_index'][:]
            metadata = {'source': url, 'licence': 'CC BY 3.0 US', 'licence_source': 'https://registry.opendata.aws/nrel-pds-nsrdb/', 'year': year,
                        'dni_attributes': {k: str(v) for k, v in hdf['dni'].attrs.items()}, 'places': {}}
            for name, (latitude, longitude, zone, region) in PLACES.items():
                if only_places and name not in only_places:
                    continue
                if region != ('meteosat' if domain == 'msg' else domain):
                    continue
                target = ROOT / f'{name.replace(" ", "_")}-{year}.npz'
                index_path = ROOT / f'{domain}-indices.json'
                cached_indices = json.loads(index_path.read_text()) if index_path.exists() else {}
                if name in cached_indices:
                    index, selected = cached_indices[name]
                elif name.endswith(' ERA5 point') and name.removesuffix(' ERA5 point') in cached_indices:
                    nearby_index = cached_indices[name.removesuffix(' ERA5 point')][0]
                    first = max(0, nearby_index-12000)
                    nearby = hdf['meta'][first:min(len(hdf['meta']), nearby_index+12000)]
                    distances = (nearby['latitude']-latitude)**2 + ((nearby['longitude']-longitude)*np.cos(np.deg2rad(latitude)))**2
                    local_index = int(np.argmin(distances))
                    if float(distances[local_index]) > .05**2:
                        raise ValueError(f'Nearby metadata window does not contain {name}')
                    index = first+local_index
                    selected = [float(nearby[local_index]['latitude']), float(nearby[local_index]['longitude'])]
                elif coordinates is None:
                    # GOES metadata is sorted by longitude, then descending latitude.
                    # Find the longitude column without downloading its 1.4 GB table.
                    low, high = 0, len(hdf['meta'])
                    while low < high:
                        middle = (low+high)//2
                        if float(hdf['meta'][middle]['longitude']) < longitude-.011:
                            low = middle+1
                        else:
                            high = middle
                    first = max(0, low-6000)
                    nearby = hdf['meta'][first:min(len(hdf['meta']), low+12000)]
                    distances = (nearby['latitude']-latitude)**2 + ((nearby['longitude']-longitude)*np.cos(np.deg2rad(latitude)))**2
                    local_index = int(np.argmin(distances))
                    index = first+local_index
                    selected = [float(nearby[local_index]['latitude']), float(nearby[local_index]['longitude'])]
                else:
                    distances = (coordinates[:, 0] - latitude)**2 + ((coordinates[:, 1] - longitude) * np.cos(np.deg2rad(latitude)))**2
                    index = int(np.argmin(distances))
                    selected = coordinates[index].astype(float).tolist()
                cached_indices = json.loads(index_path.read_text()) if index_path.exists() else {}
                cached_indices[name] = [index, selected]
                index_path.write_text(json.dumps(cached_indices, indent=2)+'\n')
                metadata['places'][name] = {'requested': [latitude, longitude], 'selected': selected, 'zone': zone, 'index': index}
                if target.exists():
                    print('cached', target.name, flush=True)
                    continue
                print('extract', name, index, selected, 'after', round(time.monotonic()-started, 1), 's', flush=True)
                dni = extract_dni_chunks(hdf['dni'], index, url, recording)
                np.savez_compressed(target, time=timestamps, dni=dni, coordinates=selected, requested=[latitude, longitude], zone=zone, source=url)
                print('saved', target.name, 'after', round(time.monotonic()-started, 1), 's', flush=True)
            provenance_path = ROOT / f'{domain}-{year}-provenance.json'
            existing = json.loads(provenance_path.read_text()) if provenance_path.exists() else {}
            metadata['places'] = {**existing.get('places', {}), **metadata['places']}
            provenance_path.write_text(json.dumps(metadata, indent=2)+'\n')


def solar(day_number, utc_hour, latitude, longitude):
    gamma = 2*np.pi/365*(day_number-1+(utc_hour-12)/24)
    equation = 229.18*(.000075+.001868*np.cos(gamma)-.032077*np.sin(gamma)-.014615*np.cos(2*gamma)-.040849*np.sin(2*gamma))
    declination = .006918-.399912*np.cos(gamma)+.070257*np.sin(gamma)-.006758*np.cos(2*gamma)+.000907*np.sin(2*gamma)-.002697*np.cos(3*gamma)+.00148*np.sin(3*gamma)
    lat = np.deg2rad(latitude)
    return equation, declination, lat


def summarize():
    """Match the product's civil clock, hour-mean DNI and integer sunset mask.

    NSRDB irradiance timestamps are instantaneous values. Average the available
    10/15-minute values in each civil clock hour; this approximates hour means.
    The partial hour containing sunset remains in the denominator, as in v6.
    """
    monthly, daily = [], []
    for path in sorted(ROOT.glob('*.npz')):
        with np.load(path) as data:
            name, year = path.stem.rsplit('-', 1)
            name = name.replace('_', ' ')
            zone = ZoneInfo(str(data['zone']))
            latitude, longitude = data['requested']
            groups = {}
            for stamp, dni in zip(data['time'], data['dni']):
                utc = dt.datetime.fromisoformat(stamp.decode()).replace(tzinfo=dt.timezone.utc)
                local = utc.astimezone(zone)
                if local.year == int(year) and 10 <= local.hour <= 20:
                    groups.setdefault(local.date(), {}).setdefault(local.hour, []).append(float(dni))
            for date, hours in groups.items():
                local_noon = dt.datetime.combine(date, dt.time(12), zone)
                offset = local_noon.utcoffset().total_seconds()/3600
                equation, declination, lat = solar(date.timetuple().tm_yday, 12, latitude, longitude)
                argument = np.cos(np.deg2rad(90.833))/(np.cos(lat)*np.cos(declination))-np.tan(lat)*np.tan(declination)
                sunset_utc = (720-4*longitude-equation+4*np.rad2deg(np.arccos(np.clip(argument,-1,1))))/60
                sunset_local = sunset_utc+offset-24*np.rint((offset-longitude/15)/24)
                daylight = int(np.clip(np.ceil(sunset_local)-10,1,11))
                expected_samples = 6 if 'GOES/' in str(data['source']) else 4
                if any(len(hours.get(hour, [])) != expected_samples for hour in range(10,10+daylight)):
                    continue
                sunny = sum(np.mean(hours[hour]) >= 300 for hour in range(10,10+daylight))
                daily.append({'place': name, 'date': str(date), 'year': int(year), 'month': date.month,
                              'sunny_hours': int(sunny), 'daylight_hours': daylight, 'sunny_share': sunny/daylight,
                              'hourly_dni': [float(np.mean(hours[hour])) if hour in hours else None for hour in range(10,21)]})
    for name in sorted({row['place'] for row in daily}):
        for month in range(1,13):
            rows = [row for row in daily if row['place']==name and row['month']==month]
            if rows:
                monthly.append({'place': name, 'month': month, 'days': len(rows),
                                'sunny_share_percent': round(100*np.mean([row['sunny_share'] for row in rows]), 3),
                                'pooled_sunny_share_percent': round(100*sum(row['sunny_hours'] for row in rows)/sum(row['daylight_hours'] for row in rows),3)})
    (ROOT/'monthly.json').write_text(json.dumps(monthly, indent=2)+'\n')
    by_year = []
    for place in sorted({row['place'] for row in daily}):
        for year in sorted({row['year'] for row in daily if row['place']==place}):
            for month in range(1,13):
                rows = [row for row in daily if row['place']==place and row['year']==year and row['month']==month]
                if rows:
                    by_year.append({'place': place, 'year': year, 'month': month, 'days': len(rows),
                                    'sunny_share_percent': round(100*np.mean([row['sunny_share'] for row in rows]),3)})
    (ROOT/'monthly_by_year.json').write_text(json.dumps(by_year, indent=2)+'\n')
    (ROOT/'daily.json').write_text(json.dumps(daily, separators=(',', ':'))+'\n')
    print(json.dumps(monthly, indent=2))


def audit():
    daily = json.loads((ROOT/'daily.json').read_text())
    rows = []
    for path in sorted(ROOT.glob('*.npz')):
        with np.load(path) as data:
            name, year = path.stem.rsplit('-',1)
            name = name.replace('_',' ')
            dni = data['dni']
            times = [dt.datetime.fromisoformat(value.decode()) for value in data['time']]
            increments = np.diff([value.timestamp() for value in times])
            samples_per_hour = 6 if 'GOES/' in str(data['source']) else 4
            matched = [row for row in daily if row['place']==name and row['year']==int(year)]
            rows.append({'file': path.name, 'place': name, 'year': int(year), 'sample_count': int(len(dni)),
                         'expected_samples': (dt.date(int(year)+1,1,1)-dt.date(int(year),1,1)).days*24*samples_per_hour,
                         'dni_dtype': str(dni.dtype), 'minimum_dni_w_m2': int(dni.min()), 'maximum_dni_w_m2': int(dni.max()),
                         'nonfinite_count': int((~np.isfinite(dni)).sum()), 'negative_count': int((dni<0).sum()),
                         'above_source_physical_max_1350_count': int((dni>1350).sum()),
                         'possible_sentinel_counts': {str(value): int((dni==value).sum()) for value in (9999,32767,65535)},
                         'zero_count': int((dni==0).sum()), 'first_utc': times[0].isoformat(), 'last_utc': times[-1].isoformat(),
                         'minimum_sample_interval_seconds': float(increments.min()), 'maximum_sample_interval_seconds': float(increments.max()),
                         'complete_local_date_count': len(matched), 'complete_local_dates_per_month': {str(month): sum(row['month']==month for row in matched) for month in range(1,13)},
                         'selected_pixel': data['coordinates'].tolist(), 'requested_point': data['requested'].tolist()})
    result = {'written_by':'AI, GPT-6.1 Sol, 2026.10.17',
              'scope':'Raw DNI values and UTC sample coverage. Complete local dates use the summarizer daylight-window completeness check. Zero DNI is valid at night or under opaque cloud. NSRDB fill_flag was not extracted, so this does not claim every satellite cloud retrieval was observed rather than model-filled.',
              'files': rows}
    (ROOT/'audit.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result,indent=2))


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--domain', choices=['goes', 'meteosat', 'msg'])
    parser.add_argument('--year', type=int, default=2019)
    parser.add_argument('--summarize', action='store_true')
    parser.add_argument('--audit', action='store_true')
    parser.add_argument('--place', action='append')
    arguments = parser.parse_args()
    if arguments.audit:
        audit()
    elif arguments.summarize:
        summarize()
    else:
        extract(arguments.domain, arguments.year, arguments.place)
