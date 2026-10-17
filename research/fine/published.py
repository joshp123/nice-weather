"""Capture the published baseline and reproduce its exact Worker pixel choice.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
The default output is a fresh capture directory, separate from phase1's original
live_points.json. Full public binary inputs and hashes make selection auditable.
"""
import argparse
import concurrent.futures
import datetime as dt
import hashlib
import json
import math
import pathlib
import struct
import subprocess
import time
import urllib.parse

AGADIR_AIRPORT = dict(station='ISD60252099999', hourly_station='60252099999', slug='agadir',
    place='Agadir Al Massira airport', country='Morocco', country_code='MO', role='evaluation',
    lat=30.324997, lon=-9.413067, elevation=76.2, target_lat=30.42, target_lon=-9.6,
    target_distance_km=20.8, note='Airport proxy about21km southeast of Agadir city; this is not city-centre weather.',
    metadata_source='https://noaa-global-hourly-pds.s3.amazonaws.com/2022/60252099999.csv')
POINT_RECORD = struct.Struct('<IIhH')
WIDTH, HEIGHT, FACT_BYTES = 1440, 721, 72


def download(url, target):
    target.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(['curl', '--retry', '2', '--connect-timeout', '20', '--max-time', '180',
                    '-fsS', '-D', str(target.with_suffix(target.suffix + '.headers')), url, '-o', str(target)], check=True)
    return target.read_bytes()


def nearest_published_land(latitude, longitude, facts):
    row = min(HEIGHT-1, max(0, math.floor((90-latitude)*4+.5)))
    column = math.floor((longitude+180)*4+.5) % WIDTH
    best, best_distance = None, math.inf
    # Worker clips horizontal search at the date line; it does not wrap neighbors.
    for candidate_row in range(max(0, row-2), min(HEIGHT, row+3)):
        for candidate_column in range(max(0, column-2), min(WIDTH, column+3)):
            fine = candidate_row*WIDTH+candidate_column
            if not any(facts[fine*FACT_BYTES:(fine+1)*FACT_BYTES]):
                continue
            distance = (candidate_row-row)**2 + (candidate_column-column)**2
            if distance < best_distance:
                best, best_distance = fine, distance
    return best


def decode_months(fine, facts, scores):
    def temperature(value):
        return math.floor((value/3-30)*10+.5)/10
    rows = []
    for month in range(12):
        high, evening, dew, wet, sunny, wind = facts[fine*72+month*6:fine*72+(month+1)*6]
        score = scores[fine*12+month]
        rows.append(dict(month=month+1, highC=temperature(high), feelsAt21C=temperature(evening),
            dewPointC=temperature(dew), wetDaysPercent=wet, sunnyPercent=sunny, windKmh=wind/2,
            score=None if score == 255 else score))
    return rows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--stations', type=pathlib.Path, default=pathlib.Path('/tmp/nwf-agents/round4/fine/stations/evaluation_stations.json'))
    parser.add_argument('--output', type=pathlib.Path, default=pathlib.Path('/tmp/nwf-agents/round4/fine/published-capture/live_points.json'))
    parser.add_argument('--base-url', default='https://nice.jjpcodes.com')
    parser.add_argument('--static-directory', type=pathlib.Path, default=pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/era5'))
    args = parser.parse_args()
    started = time.monotonic()
    raw = args.output.parent / 'published-inputs'
    raw.mkdir(parents=True, exist_ok=True)
    base = args.base_url.rstrip('/')
    meta_url = base + '/data/v6/climate/meta.json'
    meta = json.loads(download(meta_url, raw/'meta-before.json'))
    resources = [('points', '/data/v6/points.bin'), ('facts', '/data/v6/climate/facts.bin'), ('scores', '/data/v6/climate/scores.bin')]
    def acquire(resource):
        name, route = resource
        value = download(base+route, raw/(name+'.bin'))
        return name, value
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        binaries = dict(pool.map(acquire, resources))
    if len(binaries['facts']) != WIDTH*HEIGHT*FACT_BYTES or len(binaries['scores']) != WIDTH*HEIGHT*12:
        raise ValueError('Published facts/scores binary dimensions disagree with v6 contract')
    point_count = struct.unpack_from('<I', binaries['points'])[0]
    if len(binaries['points']) != 4+point_count*POINT_RECORD.size:
        raise ValueError('Published points binary dimensions disagree with v6 contract')
    points = {record[0]: record for record in POINT_RECORD.iter_unpack(binaries['points'][4:])}
    height = land = None
    if (args.static_directory/'static_z.npy').exists() and (args.static_directory/'static_lsm.npy').exists():
        import numpy as np
        height = np.load(args.static_directory/'static_z.npy')/9.80665
        land = np.load(args.static_directory/'static_lsm.npy')
    stations = json.loads(args.stations.read_text())
    if not any(station.get('station') == AGADIR_AIRPORT['station'] for station in stations):
        stations.append(AGADIR_AIRPORT)
    queries = list(stations)
    for station in stations:
        if station.get('original_noaa_location'):
            queries.append({**station, **station['original_noaa_location'], 'slug': station['slug']+'_original', 'original_query_for': station['station']})
    (args.output.parent/'evaluation_queries.json').write_text(json.dumps(stations, indent=2)+'\n')
    def capture(station):
        lat, lon = station['lat'], station['lon']
        url = base+'/api/place?'+urllib.parse.urlencode(dict(lat=lat, lon=lon))
        key = station.get('station') or 'null'
        api_path = raw/f"api-{station['slug']}-{key}.json"
        api = json.loads(download(url, api_path))
        fine = nearest_published_land(lat, lon, binaries['facts'])
        reconstructed = decode_months(fine, binaries['facts'], binaries['scores']) if fine is not None else None
        if reconstructed != api['months']:
            raise ValueError(f"Worker/API facts disagree for {station['slug']}: publication may have changed during capture")
        entry = dict(slug=station['slug'], place=station['place'], station=station.get('station'),
            station_latitude=lat, station_longitude=lon, query_url=url, api_response_file=str(api_path),
            monthly=api['months'], fine=fine, source=None, shift=None,
            geolocation_uncertain=station.get('geolocation_uncertain', False),
            location_note=station.get('coordinate_note', station.get('note')), target_distance_km=station.get('target_distance_km'),
            monthly_airhigh_c=[row['highC'] for row in reconstructed] if reconstructed else None,
            monthly_felt21_c=[row['feelsAt21C'] for row in reconstructed] if reconstructed else None)
        if fine is not None:
            _, source, shift, cell = points[fine]
            fr, fc = divmod(fine, WIDTH); sr, sc = divmod(source, WIDTH)
            entry.update(source=source, shift=shift/100, cell=cell,
                fine_latitude=90-fr*.25, fine_longitude=-180+fc*.25,
                source_latitude=90-sr*.25, source_longitude=-180+sc*.25)
            if height is not None:
                entry.update(fine_height_m=float(height[fr,(fc+720)%WIDTH]), source_height_m=float(height[sr,(sc+720)%WIDTH]),
                    fine_land_fraction=float(land[fr,(fc+720)%WIDTH]), source_land_fraction=float(land[sr,(sc+720)%WIDTH]))
        return station, entry
    rows, by_slug = {}, {}
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        captures = list(pool.map(capture, queries))
    for station, entry in captures:
        if station.get('original_query_for'):
            by_slug[entry['slug']] = entry
            continue
        rows[station.get('station') or 'null'] = entry
        by_slug[station['slug'] if station.get('station') else station['slug']+'_city'] = entry
    for station, entry in captures:
        if station.get('original_query_for'):
            rows[station['original_query_for']]['original_query'] = entry
    after = json.loads(download(meta_url, raw/'meta-after.json'))
    if meta != after:
        raise ValueError('Published climate metadata changed during capture; rerun into a new folder')
    result = dict(written_by='AI, GPT-6.1 Sol, 2026.10.17', captured_at_utc=dt.datetime.now(dt.timezone.utc).isoformat(),
        base_url=base, live_meta=meta, selection='Worker exact: nonzero72byte facts, nearest integer distance to rounded centre within clipped5x5, first row/column ties; decoded facts/scores equal API response.',
        source_files={name:dict(url=base+route, path=str(raw/(name+'.bin')), bytes=len(binaries[name]), sha256=hashlib.sha256(binaries[name]).hexdigest()) for name,route in resources},
        static_metadata_directory=str(args.static_directory) if height is not None else None,
        evaluation_queries=str(args.output.parent/'evaluation_queries.json'), stations=rows, by_slug=by_slug,
        seconds=time.monotonic()-started)
    args.output.write_text(json.dumps(result, indent=2)+'\n')
    print(json.dumps(dict(output=str(args.output), stations=len(rows), seconds=result['seconds'], points_sha256=result['source_files']['points']['sha256'])))


if __name__ == '__main__':
    main()
