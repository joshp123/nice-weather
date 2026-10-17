"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Sample public temperature normals at evaluation/training coordinates. Downloads
and extracted values stay outside the repository. No production files change.
"""
import argparse
import concurrent.futures
import json
import pathlib
import subprocess
import time
import zipfile

import numpy as np


def download(url, destination):
    if not destination.exists():
        partial = destination.with_suffix(destination.suffix + '.partial')
        subprocess.run(['curl', '-fL', '--retry', '4', '--retry-all-errors',
                        '--max-time', '1800', '-o', str(partial), url], check=True)
        partial.rename(destination)


def download_ranges(url, destination):
    """Eight bounded range requests avoid the archive server's slow single stream."""
    if destination.exists():
        return
    header = subprocess.check_output(['curl', '-fsSI', '--max-time', '30', url]).decode()
    size = int(next(line.split(':',1)[1] for line in header.splitlines() if line.lower().startswith('content-length:')))
    chunks = [(first,min(size,first+2*1024*1024)) for first in range(0,size,2*1024*1024)]
    parts = destination.parent/(destination.name+'.parts')
    parts.mkdir(exist_ok=True)
    def fetch(bounds):
        first, last = bounds
        path = parts/str(first)
        if not path.exists() or path.stat().st_size != last-first:
            subprocess.run(['curl','-fsS','--retry','4','--retry-all-errors','--max-time','180',
                            '-r',f'{first}-{last-1}',url,'-o',str(path)],check=True)
        if path.stat().st_size != last-first:
            raise ValueError((url,first,path.stat().st_size,last-first))
        return path
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        paths = list(pool.map(fetch,chunks))
    partial = destination.with_suffix(destination.suffix+'.partial')
    with partial.open('wb') as stream:
        for path in paths:
            stream.write(path.read_bytes())
    partial.rename(destination)


def sample_worldclim(stations, scratch, resolution):
    """Read each compressed BIL sequentially once, retaining small pixel windows."""
    started = time.monotonic()
    results = {s['station']: {'tmax': [], 'tmin': [], 'tmax_5km': [], 'tmin_5km': []}
               for s in stations if s.get('station')}
    provenance = []
    tiles = {}
    for station in stations:
        if station.get('station'):
            tile = f"{int((90-station['lat'])//30)}{int((180+station['lon'])//30)}"
            tiles.setdefault(tile,[]).append(station)
    jobs = [(variable,tile,targets) for variable in ['tmax','tmin'] for tile,targets in tiles.items()] if resolution=='30s' else [(variable,None,stations) for variable in ['tmax','tmin']]
    for variable, tile, targets in jobs:
        filename = f'{variable}_{tile}.zip' if tile else f'{variable}_{resolution}_bil.zip'
        url = 'https://geodata.ucdavis.edu/climate/worldclim/1_4/'+('tiles/cur/' if tile else 'grid/cur/')+filename
        archive_path = scratch / f'worldclim14_{filename}'
        download_ranges(url, archive_path)
        with zipfile.ZipFile(archive_path) as archive:
            headers = sorted(n for n in archive.namelist() if n.endswith('.hdr'))
            for header_name in headers:
                month = int(pathlib.Path(header_name).stem.removeprefix(variable).split('_')[0])
                header = dict(line.split(None, 1) for line in archive.read(header_name).decode().splitlines()
                              if len(line.split(None, 1)) == 2)
                width, height = int(header['NCOLS']), int(header['NROWS'])
                west, north = float(header['ULXMAP']), float(header['ULYMAP'])
                dx, dy = float(header['XDIM']), float(header['YDIM'])
                nodata = int(header.get('NODATA', '-9999'))
                stations_by_row = {}
                windows = {}
                for station in targets:
                    if not station.get('station'):
                        continue
                    column = int(np.floor((station['lon'] - west) / dx + .5))
                    row = int(np.floor((north - station['lat']) / dy + .5))
                    if not (0 <= row < height and 0 <= column < width):
                        windows[station['station']] = (row, column, [])
                        continue
                    windows[station['station']] = (row, column, [])
                    radius = 2 if resolution == '30s' else 0
                    for window_row in range(max(0, row-radius), min(height, row+radius+1)):
                        stations_by_row.setdefault(window_row, []).append(station['station'])
                required_rows = sorted(stations_by_row)
                exact = {}
                with archive.open(header_name[:-4]+'.bil') as stream:
                    current_row = 0
                    for row in required_rows:
                        discard = (row-current_row)*width*2
                        while discard:
                            block = stream.read(min(discard, 8*1024*1024))
                            if not block:
                                raise EOFError(header_name)
                            discard -= len(block)
                        raw = stream.read(width*2)
                        values = np.frombuffer(raw, '<i2')
                        current_row = row+1
                        for station_id in stations_by_row[row]:
                            target_row, column, samples = windows[station_id]
                            if row == target_row:
                                exact[station_id] = None if values[column] == nodata else float(values[column])/10
                            radius = 2 if resolution == '30s' else 0
                            samples.extend(values[max(0,column-radius):column+radius+1].tolist())
                for station_id, (_, _, samples) in windows.items():
                    valid = [value/10 for value in samples if value != nodata]
                    results[station_id][variable].append((month, exact.get(station_id)))
                    results[station_id][variable+'_5km'].append((month, float(np.mean(valid)) if valid else None))
                provenance.append(dict(url=url, file=header_name, header=header,
                                       compressed_bytes=archive_path.stat().st_size))
                print('WorldClim1.4', resolution, variable, month, round(time.monotonic()-started,1), flush=True)
    for values in results.values():
        for variable, months in values.items():
            values[variable] = [value for _, value in sorted(months)]
    output = dict(source='WorldClim 1.4', licence='CC-BY-SA-4.0', resolution=resolution,
                  seconds=time.monotonic()-started, points=results, provenance=provenance)
    (scratch/f'worldclim14_{resolution}_points.json').write_text(json.dumps(output, indent=2))


def sample_prism(stations, scratch):
    from urllib.parse import urlencode
    output = {}
    endpoint = 'https://prism.oregonstate.edu/explorer/dataexplorer/rpc.php'
    for station in stations:
        if station.get('country_code') != 'US' or not (-125 < station['lon'] < -66):
            continue
        parameters = dict(proc='gridserv', call='pp/monthly_normals_timeseries',
                          lon=station['lon'], lat=station['lat'], spares='800m',
                          stats='tmax tmin', units='si', interp=0, range='monthly_normals')
        path = scratch / f"prism_{station['station']}.json"
        if not path.exists():
            response = subprocess.check_output(['curl','-fsS','--retry','4','--retry-all-errors',
                '--max-time','90','--data',urlencode(parameters),endpoint])
            path.write_bytes(response)
        data = json.loads(path.read_text())
        if data.get('errors') or 'result' not in data:
            raise ValueError(data)
        output[station['station']] = dict(**data['result']['data'], parameters=parameters, endpoint=endpoint)
        print('PRISM', station['slug'], flush=True)
    (scratch/'prism_points.json').write_text(json.dumps(output, indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('source', choices=['worldclim', 'prism'])
    parser.add_argument('--stations', type=pathlib.Path, default=pathlib.Path('/tmp/nwf-agents/round4/fine/stations/evaluation_stations.json'))
    parser.add_argument('--scratch', type=pathlib.Path, default=pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-phase1'))
    parser.add_argument('--resolution', default='30s', choices=['30s','2-5m'])
    args = parser.parse_args()
    args.scratch.mkdir(exist_ok=True, parents=True)
    stations = json.loads(args.stations.read_text())
    if args.source == 'worldclim':
        sample_worldclim(stations, args.scratch, args.resolution)
    else:
        sample_prism(stations, args.scratch)


if __name__ == '__main__':
    main()
