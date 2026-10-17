"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Sample CHELSA temperature normals with cached HTTP ranges. Exact coastal NoData
stays missing; the separate 5x5 mean reports its valid-pixel count explicitly.
Run with sh research/fine/run.sh chelsa.py. Source rasters remain on the provider.
"""

import argparse
import concurrent.futures
import hashlib
import io
import json
import math
import pathlib
import subprocess
import time
import xml.etree.ElementTree as ET

import numpy as np
import tifffile


class CachedRangeReader(io.RawIOBase):
    def __init__(self, source_url, cache_directory):
        self.name = source_url
        self.source_url = source_url
        self.position = 0
        self.downloaded_bytes = 0
        self.cache_directory = cache_directory
        cache_directory.mkdir(parents=True, exist_ok=True)
        size_path = cache_directory / 'size.txt'
        if not size_path.exists():
            headers = subprocess.check_output([
                'curl', '--head', '--location', '--fail', '--silent',
                '--show-error', '--retry', '5', '--retry-all-errors',
                '--max-time', '60', source_url,
            ]).decode()
            lengths = [int(line.split(':', 1)[1]) for line in headers.splitlines()
                       if line.lower().startswith('content-length:')]
            if not lengths:
                raise IOError(f'No content length returned for {source_url}')
            size_path.write_text(str(lengths[-1]))
        self.size = int(size_path.read_text())

    def seekable(self):
        return True

    def readable(self):
        return True

    def tell(self):
        return self.position

    def seek(self, offset, whence=0):
        self.position = offset + ({0: 0, 1: self.position, 2: self.size}[whence])
        return self.position

    def read(self, size=-1):
        if size < 0:
            size = self.size - self.position
        requested_start = self.position
        requested_length = min(size, self.size - requested_start)
        if requested_length <= 0:
            return b''
        range_start = requested_start
        range_length = requested_length
        if size < 16384:
            range_start = requested_start // 16384 * 16384
            range_length = min(
                math.ceil((requested_start - range_start + size) / 16384) * 16384,
                self.size - range_start,
            )
        range_path = self.cache_directory / f'{range_start}-{range_length}.bin'
        if not range_path.exists():
            downloaded = subprocess.check_output([
                'curl', '--location', '--fail', '--silent', '--show-error',
                '--retry', '5', '--retry-all-errors', '--max-time', '90',
                '--header', f'Range: bytes={range_start}-{range_start + range_length - 1}',
                self.source_url,
            ])
            if len(downloaded) != range_length:
                raise IOError(f'Range response length {len(downloaded)} != '
                              f'{range_length}: {self.source_url}')
            range_path.write_bytes(downloaded)
            self.downloaded_bytes += len(downloaded)
        cached = range_path.read_bytes()
        if len(cached) != range_length:
            raise IOError(f'Incomplete cached range: {range_path}')
        self.position += requested_length
        offset = requested_start - range_start
        return cached[offset:offset + requested_length]


def source_url(variable, month):
    return ('https://os.unil.cloud.switch.ch/chelsa02/chelsa/global/climatologies/'
            f'{variable}/1981-2010/CHELSA_{variable}_{month:02d}_1981-2010_V.2.1.tif')


def sample_month(variable, month, stations, scratch, station_signature):
    cache_directory = scratch / 'chelsa_ranges' / f'{variable}-{month:02d}'
    result_path = cache_directory / 'samples.json'
    if result_path.exists():
        cached = json.loads(result_path.read_text())
        if cached.get('station_signature') == station_signature:
            cached['downloaded_bytes_this_run'] = 0
            return cached

    reader = CachedRangeReader(source_url(variable, month), cache_directory)
    points = {}
    with tifffile.TiffFile(reader) as raster:
        page = raster.pages[0]
        metadata = {entry.attrib['name']: entry.text
                    for entry in ET.fromstring(page.tags['GDAL_METADATA'].value)}
        scale = float(metadata['SCALE'])
        offset = float(metadata.get('OFFSET', '0'))
        if metadata.get('variable_unit') != 'K':
            raise ValueError(f'Unexpected CHELSA unit: {metadata}')
        missing_value = int(page.tags['GDAL_NODATA'].value)
        pixel_width, pixel_height, _ = page.tags['ModelPixelScaleTag'].value
        _, _, _, west, north, _ = page.tags['ModelTiepointTag'].value
        tiles_across = math.ceil(page.imagewidth / page.tilewidth)
        decoded_tiles = {}

        def pixel_value(row, column):
            if not (0 <= row < page.imagelength and 0 <= column < page.imagewidth):
                return None
            tile_index = row // page.tilelength * tiles_across + column // page.tilewidth
            if tile_index not in decoded_tiles:
                reader.seek(page.dataoffsets[tile_index])
                compressed = reader.read(page.databytecounts[tile_index])
                decoded_tiles[tile_index] = np.asarray(page.decode(compressed, tile_index)[0]).reshape(
                    page.tilelength, page.tilewidth)
            stored = int(decoded_tiles[tile_index][row % page.tilelength, column % page.tilewidth])
            return None if stored == missing_value else stored * scale + offset - 273.15

        for station in stations:
            column = math.floor((station['lon'] - west) / pixel_width)
            row = math.floor((north - station['lat']) / pixel_height)
            neighbors = [pixel_value(window_row, window_column)
                         for window_row in range(row - 2, row + 3)
                         for window_column in range(column - 2, column + 3)]
            valid_neighbors = [value for value in neighbors if value is not None]
            points[station['station']] = {
                'exact': pixel_value(row, column),
                'mean_5x5': float(np.mean(valid_neighbors)) if valid_neighbors else None,
                'valid_pixels_5x5': len(valid_neighbors),
                'pixel_center': [west + (column + .5) * pixel_width,
                                 north - (row + .5) * pixel_height],
                'row': row,
                'column': column,
            }
        result = {
            'variable': variable, 'month': month, 'url': reader.source_url,
            'metadata': metadata, 'scale': scale, 'offset': offset,
            'missing_value': missing_value,
            'dimensions': [page.imagelength, page.imagewidth],
            'pixel_degrees': [pixel_width, pixel_height],
            'source_bytes': reader.size, 'tiles_read': len(decoded_tiles),
            'downloaded_bytes_this_run': reader.downloaded_bytes,
            'station_signature': station_signature, 'points': points,
        }
    result_path.write_text(json.dumps(result, indent=2))
    print(f'CHELSA {variable} {month:02d}: {len(points)} stations, '
          f'{result["tiles_read"]} tiles, {reader.downloaded_bytes:,} downloaded bytes', flush=True)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stations', type=pathlib.Path,
                        default=pathlib.Path('/tmp/nwf-agents/round4/fine/stations'))
    parser.add_argument('--evaluation-stations', type=pathlib.Path,
                        help='Explicit evaluation queries, including additional hourly-only sites')
    parser.add_argument('--scratch', type=pathlib.Path, default=pathlib.Path(
        '/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-phase1'))
    parser.add_argument('--workers', type=int, default=4)
    arguments = parser.parse_args()
    started = time.monotonic()
    stations_by_identifier = {}
    station_files = [arguments.evaluation_stations or arguments.stations/'evaluation_stations.json',
                     arguments.stations/'training_stations.json']
    for station_file in station_files:
        for station in json.loads(station_file.read_text()):
            if not station.get('station'):
                continue
            identifier = station['station']
            previous = stations_by_identifier.get(identifier)
            if previous and (previous['lat'], previous['lon']) != (station['lat'], station['lon']):
                raise ValueError(f'Conflicting coordinates for {identifier}')
            stations_by_identifier[identifier] = station
    stations = list(stations_by_identifier.values())
    station_signature = hashlib.sha256(json.dumps(
        sorted((station['station'], station['lat'], station['lon']) for station in stations)
    ).encode()).hexdigest()
    arguments.scratch.mkdir(parents=True, exist_ok=True)
    jobs = [(variable, month) for variable in ['tasmax', 'tasmin'] for month in range(1, 13)]
    with concurrent.futures.ThreadPoolExecutor(max_workers=arguments.workers) as pool:
        monthly_results = list(pool.map(lambda job: sample_month(
            *job, stations, arguments.scratch, station_signature), jobs))
    points = {}
    for station in stations:
        identifier = station['station']
        values = {key: station.get(key) for key in
                  ['lat', 'lon', 'elevation', 'role', 'slug', 'name']}
        values['valid_pixels_5km'] = {}
        for variable, output_name in [('tasmax', 'tmax'), ('tasmin', 'tmin')]:
            months = sorted((result for result in monthly_results if result['variable'] == variable),
                            key=lambda result: result['month'])
            samples = [result['points'][identifier] for result in months]
            values[output_name] = [sample['exact'] for sample in samples]
            values[output_name + '_5km'] = [sample['mean_5x5'] for sample in samples]
            values['valid_pixels_5km'][output_name] = [sample['valid_pixels_5x5'] for sample in samples]
            values['pixel_center'] = samples[0]['pixel_center']
            values['pixel_row_column'] = [samples[0]['row'], samples[0]['column']]
        points[identifier] = values
    output = {
        'source': 'CHELSA v2.1', 'licence': 'CC0-1.0', 'period': '1981-2010',
        'source_page': 'https://www.chelsa-climate.org/datasets/chelsa_climatologies',
        'units': 'degrees Celsius', 'resolution': '30 arc-seconds',
        'aggregation': 'Exact containing pixel; separate unweighted mean of valid pixels '
                       'in centered 5x5 window (2.5 arc-minutes, not constant 5 km). '
                       'No nearest-land substitution.',
        'station_files': [str(path) for path in station_files],
        'station_signature': station_signature,
        'seconds': time.monotonic() - started,
        'downloaded_bytes_this_run': sum(result['downloaded_bytes_this_run'] for result in monthly_results),
        'points': points,
        'provenance': [{key: value for key, value in result.items() if key != 'points'}
                       for result in monthly_results],
    }
    destination = arguments.scratch / 'chelsa_points.json'
    destination.write_text(json.dumps(output, indent=2))
    print(f'Saved {destination}: {len(points)} stations in {output["seconds"]:.1f} seconds', flush=True)


if __name__ == '__main__':
    main()
