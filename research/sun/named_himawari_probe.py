"""Bounded anonymous Himawari typical-year probe; never a global acquisition.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
Run in devenv with numpy, scipy, h5py, fsspec and aiohttp. Physical chunk offsets,
including the chunked compound metadata and selected years, come from HDF5.
"""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import http.client
import json
from pathlib import Path
import threading
import time
import urllib.request

import h5py
import numpy as np

from instant_hours import integrate_civil_hours
from named_range_file import AnonymousRangeFile
from named_satellite import atomic_json, solar_sunset
from named_timezone_lookup import TargetTimezones
from satellite import ChunkLocationFile

ROOT = Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/sun/named/himawari')
URL = 'https://nrel-pds-nsrdb.s3.amazonaws.com/himawari/himawari_{}-2020.h5'
CITIES = ('Bangkok', 'Singapore', 'Perth', 'Sydney', 'Tokyo')


class BoundedRangeFile(AnonymousRangeFile):
    def __init__(self, url, max_bytes):
        super().__init__(url)
        self.max_bytes = max_bytes
        self.direct_bytes = 0
        self.lock = threading.Lock()
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def reserve(self, size):
        with self.lock:
            if self.transferred + self.direct_bytes + size > self.max_bytes:
                raise ValueError('Explicit network byte budget exhausted')
            self.direct_bytes += size

    def block(self, index):
        if index not in self.blocks:
            with self.lock:
                if self.transferred + self.direct_bytes + self.block_size > self.max_bytes:
                    raise ValueError('Explicit network byte budget exhausted')
        return super().block(index)

    def exact(self, offset, size):
        for attempt in range(4):
            self.reserve(size)
            request = urllib.request.Request(self.url, headers={'Range': f'bytes={offset}-{offset+size-1}'})
            try:
                with self.opener.open(request, timeout=90) as response:
                    if response.status != 206:
                        raise ValueError('Archive did not honour a bounded range request')
                    data = response.read(size+1)
                if len(data) != size:
                    raise OSError(f'Truncated source range: {len(data)} != {size}')
                return data
            except (OSError, http.client.IncompleteRead):
                if attempt == 3:
                    raise
                time.sleep(2**attempt)


class PhysicalChunks:
    def __init__(self, recording, remote):
        self.recording, self.remote = recording, remote
        self.locations = {}

    def locate(self, dataset, origin):
        key = (dataset.name, tuple(origin))
        if key not in self.locations:
            if dataset.compression is not None or dataset.shuffle or dataset.fletcher32:
                raise ValueError('Probe only supports verified unfiltered archive chunks')
            size = int(np.prod(dataset.chunks))*dataset.dtype.itemsize
            self.recording.locating_size = size
            self.recording.locations = []
            dataset.id.read_direct_chunk(tuple(origin))
            self.recording.locating_size = None
            if len(self.recording.locations) != 1:
                raise ValueError('Cannot identify one physical source chunk')
            self.locations[key] = self.recording.locations[0]
        return self.locations[key]

    def scalar(self, dataset, indices):
        origin = tuple(index//chunk*chunk for index, chunk in zip(indices, dataset.chunks))
        offset, _ = self.locate(dataset, origin)
        within = tuple(index-start for index, start in zip(indices, origin))
        address = int(np.ravel_multi_index(within, dataset.chunks))*dataset.dtype.itemsize
        return np.frombuffer(self.remote.exact(offset+address, dataset.dtype.itemsize), dtype=dataset.dtype)[0]

def catalogue_points(path):
    result = []
    for line in path.read_text().splitlines():
        row = line.split('\t')
        if row[0] in CITIES and row[0] not in {point['name'] for point in result}:
            result.append(dict(name=row[0], latitude=float(row[3]), longitude=float(row[4])))
    if len(result) != 5:
        raise ValueError('Expected all five catalogue cities')
    return result


def summarize(values, timestamps, latitude, longitude, zone, selected_years):
    rows = [[] for _ in range(12)]
    for day in integrate_civil_hours(values, timestamps, zone, selected_years):
        actual = day['date'].replace(year=day['actual_year'])
        daylight = solar_sunset(actual, latitude, longitude, day['utc_offset_hours'])
        if day['complete'][:daylight].all():
            hours = day['hourly_dni']
            rows[day['date'].month-1].append((np.count_nonzero(hours[:daylight] >= 300)/daylight, daylight, hours))
    return [dict(sunnyShare=float(np.mean([row[0] for row in month])), daylightHours=float(np.mean([row[1] for row in month])), hourlyDni=np.mean([row[2] for row in month], axis=0).tolist(), sourceDays=len(month)) for month in rows]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=ROOT)
    parser.add_argument('--places', type=Path, default=Path('artifacts/places.tsv'))
    parser.add_argument('--max-bytes', type=int, default=100_000_000)
    parser.add_argument('--metadata-only', action='store_true')
    parser.add_argument('--source', choices=('tmy', 'tdy'), default='tmy')
    parser.add_argument('--reference-metadata', type=Path, help='Reuse only after 25 actual source coordinate checks')
    parser.add_argument('--full-metadata-plan', type=Path, help='Explicitly plan all named coordinates; requires 260 MB extra metadata transfer budget')
    args = parser.parse_args()
    args.root.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    url = URL.format(args.source)
    with BoundedRangeFile(url, args.max_bytes) as remote:
        recording = ChunkLocationFile(remote)
        with h5py.File(recording) as hdf:
            chunks = PhysicalChunks(recording, remote)
            metadata, dni = hdf['meta'], hdf['dni']
            scale_factor = float(dni.attrs['scale_factor'])
            report = dict(writtenBy='AI, GPT-6.1 Sol, 2026-10-18', url=url, archiveBytes=remote.size, sourceVersion=str(hdf.attrs['version']), dniShape=dni.shape, dniChunks=dni.chunks, dniScaleFactor=scale_factor, dniUnits=str(dni.attrs['units']), metadataChunks=metadata.chunks, metadataBytes=metadata.size*metadata.dtype.itemsize, integration='Linear instantaneous DNI at 60 minute midpoints per civil hour, canonical IANA clock and selected source year')
            print(json.dumps(report), flush=True)
            selected_path = args.root/f'{args.source}-selected-points.json'
            if args.full_metadata_plan:
                from scipy.spatial import cKDTree
                metadata_path = args.reference_metadata or args.root/f'{args.source}-metadata.npy'
                if args.reference_metadata:
                    reference = np.load(metadata_path, mmap_mode='r')
                    if reference.shape != metadata.shape:
                        raise ValueError('Reference metadata shape differs from actual source')
                    for index in np.linspace(0, len(metadata)-1, 25).astype(int):
                        actual = chunks.scalar(metadata, (index,))
                        if (actual['latitude'], actual['longitude']) != (reference[index]['latitude'], reference[index]['longitude']):
                            raise ValueError('Reference coordinates differ from actual source')
                    report['referenceCoordinatesVerified'] = 25
                if not metadata_path.exists():
                    requests = [(start, *chunks.locate(metadata, (start,))) for start in range(0, len(metadata), metadata.chunks[0])]
                    print(json.dumps(dict(metadataChunks=len(requests), metadataTransferBytes=sum(size for _, _, size in requests))), flush=True)
                    available = np.empty(metadata.shape, dtype=metadata.dtype)
                    metadata_cache = args.root/f'{args.source}-metadata-chunks'
                    metadata_cache.mkdir(exist_ok=True)
                    def fetch_metadata(item):
                        start, offset, size = item
                        cached = metadata_cache/f'{start}.bin'
                        if not cached.exists():
                            payload = remote.exact(offset, size)
                            temporary = cached.with_suffix('.partial')
                            temporary.write_bytes(payload)
                            temporary.replace(cached)
                        return start, np.frombuffer(cached.read_bytes(), dtype=metadata.dtype)
                    with ThreadPoolExecutor(max_workers=12) as pool:
                        for start, values in pool.map(fetch_metadata, requests):
                            count = min(len(values), len(available)-start)
                            available[start:start+count] = values[:count]
                    np.save(metadata_path, available)
                available = np.load(metadata_path, mmap_mode='r')
                native_coordinates = np.column_stack((available['latitude'], available['longitude']))
                def spherical(points):
                    radians = np.deg2rad(points)
                    return np.column_stack((np.cos(radians[:, 0])*np.cos(radians[:, 1]), np.cos(radians[:, 0])*np.sin(radians[:, 1]), np.sin(radians[:, 0])))
                tree = cKDTree(spherical(native_coordinates))
                named_coordinates = np.array(json.loads(args.full_metadata_plan.read_text()))
                distances, indices = tree.query(spherical(named_coordinates), distance_upper_bound=8/6371)
                supported = distances*6371 < 8
                selected_coordinates = np.full(named_coordinates.shape, np.nan)
                selected_coordinates[supported] = native_coordinates[indices[supported]]
                supported_groups = sorted(set((indices[supported]//dni.chunks[1]).tolist()))
                bytes_per_group = int(np.ceil(dni.shape[0]/dni.chunks[0]))*int(np.prod(dni.chunks))*dni.dtype.itemsize
                global_plan = dict(requested=len(named_coordinates), eligible=int(supported.sum()), nativeGroups=len(supported_groups), groups=supported_groups, radiationBytes=len(supported_groups)*bytes_per_group, bytesPerGroup=bytes_per_group, metadataBytes=report['metadataBytes'], maxDistanceKm=8, metadataSha256=hashlib.sha256(metadata_path.read_bytes()).hexdigest(), coordinatesSha256=hashlib.sha256(args.full_metadata_plan.read_bytes()).hexdigest(), method='Spherical nearest actual Himawari pixel; support within 8 km; no radiation acquisition')
                atomic_json(args.root/'named-global-plan.json', global_plan)
                np.savez_compressed(args.root/'named-global-locations.npz', requested=named_coordinates, sourceIndices=np.where(supported, indices, -1), supported=supported, selected=selected_coordinates, distanceKm=distances*6371)
                print(json.dumps({key: value for key, value in global_plan.items() if key != 'groups'}), flush=True)
                points = []
                for point in catalogue_points(args.places):
                    distance, index = tree.query(spherical(np.array([[point['latitude'], point['longitude']]])))
                    if distance[0]*6371 >= 8:
                        raise ValueError(f'City outside native coverage: {point["name"]}')
                    points.append(dict(**point, sourceIndex=int(index[0]), selectedCoordinates=native_coordinates[index[0]].tolist(), distanceKm=float(distance[0]*6371)))
                atomic_json(selected_path, points)
            elif selected_path.exists():
                points = json.loads(selected_path.read_text())
            else:
                raise ValueError('First run an explicit full metadata plan to select the five native points')
            group_size = dni.chunks[1]
            groups = sorted({point['sourceIndex']//group_size for point in points})
            bytes_per_group = int(np.ceil(dni.shape[0]/dni.chunks[0]))*int(np.prod(dni.chunks))*dni.dtype.itemsize
            report.update(points=points, nativeGroups=len(groups), radiationBytes=len(groups)*bytes_per_group, bytesPerGroup=bytes_per_group, allDomainGroupUpperBound=int(np.ceil(dni.shape[1]/group_size)), allDomainRadiationBytesUpperBound=int(np.ceil(dni.shape[1]/group_size))*bytes_per_group)
            atomic_json(args.root/f'{args.source}-probe-plan.json', report)
            print(json.dumps(report), flush=True)
            if args.metadata_only:
                return
            timestamps = [value.decode() for value in hdf['time_index'][:]]
            years = hdf['tmy_year_short']
            zones = TargetTimezones(ROOT.parent/'target-timezones.json')
            records = []
            for point in points:
                index = point['sourceIndex']
                point['selectedYears'] = [int(chunks.scalar(years, (month, index))) for month in range(12)]
                if any(year < 2011 or year > 2020 for year in point['selectedYears']):
                    raise ValueError('Selected Himawari years fall outside documented 2011–2020 period')
                point['timezone'] = zones.timezone_at(lat=point['latitude'], lng=point['longitude'])
            for group in groups:
                members = [point for point in points if point['sourceIndex']//group_size == group]
                path = args.root/f'{args.source}-group-{group}.npz'
                if not path.exists():
                    requests = [(start, *chunks.locate(dni, (start, group*group_size))) for start in range(0, dni.shape[0], dni.chunks[0])]
                    if remote.transferred+remote.direct_bytes+sum(size for _, _, size in requests) > args.max_bytes:
                        raise ValueError('Planned radiation exceeds remaining byte budget')
                    def fetch(item):
                        start, offset, size = item
                        values = np.frombuffer(remote.exact(offset, size), dtype=dni.dtype).reshape(dni.chunks)
                        return start, values[:, [point['sourceIndex']%group_size for point in members]].copy()
                    values = np.empty((dni.shape[0], len(members)), dtype=dni.dtype)
                    with ThreadPoolExecutor(max_workers=7) as pool:
                        for start, data in pool.map(fetch, requests):
                            count = min(len(data), len(values)-start)
                            values[start:start+count] = data[:count]
                    np.savez_compressed(path, dni=values, time=np.array(timestamps), sourceIndices=[point['sourceIndex'] for point in members])
                cached = np.load(path)
                for column, point in enumerate(members):
                    months = summarize(cached['dni'][:, column]/scale_factor, timestamps, point['latitude'], point['longitude'], point['timezone'], point['selectedYears'])
                    records.append(dict(**point, months=months, source=f'NSRDB Himawari PSM v3 {args.source.upper()} 2011–2020', years=[2011, 2020], url=url, rawSha256=hashlib.sha256(path.read_bytes()).hexdigest()))
                print(json.dumps(dict(groupComplete=group, bytes=remote.transferred+remote.direct_bytes)), flush=True)
            report.update(records=records, rangeBudgetBytesUsed=remote.transferred+remote.direct_bytes, seconds=time.monotonic()-started, scope='Five native points only; no production target or worldwide coverage claim')
            atomic_json(args.root/f'{args.source}-probe-results.json', report)
            print(json.dumps(dict(complete=True, rangeBudgetBytesUsed=report['rangeBudgetBytesUsed'], seconds=report['seconds'])), flush=True)


if __name__ == '__main__':
    main()
