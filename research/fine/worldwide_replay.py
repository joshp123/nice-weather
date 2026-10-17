"""Build worldwide exact-place compact replay data from retained native weather.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
No station values, per-place adjustments, UI changes, or publication.
"""
import argparse
import concurrent.futures
import datetime as dt
import functools
import gzip
import hashlib
import importlib.metadata
import json
import multiprocessing as mp
import os
import pathlib
import struct
import shutil
import time
import zoneinfo

import numpy as np

from native_normals import file_sha256, local_hour_indices
from compare_step_a import climate_pipeline
from tiles import POINT_DTYPE, TILE_SIZE

PACKET_HEADER = struct.Struct('<4sBBHIHH16s')
ZONE_HEADER = struct.Struct('<4sHHHH16sI')
WIDTH, HEIGHT = 43200, 21600


def write_atomic(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name+f'.{os.getpid()}.partial')
    temporary.write_bytes(data)
    temporary.replace(path)


def packet_bytes(fine, zone, generation, selected, records, physical, monthly, clock_delta):
    header = PACKET_HEADER.pack(b'NWFP', 1, 1, len(selected), fine, zone, len(physical), bytes.fromhex(generation))
    return header + np.asarray(selected, dtype='<u2').tobytes() + np.asarray(records, dtype='<f4').tobytes() + np.asarray(physical, dtype='<f4').tobytes() + np.asarray(monthly, dtype='<f4').tobytes() + np.asarray(clock_delta, dtype='<f4').tobytes()


def compact_json(packet, zone, dates):
    days = len(dates)
    fields = {key: np.array(packet[key]).reshape(days, 12) for key in ('airC', 'dewC', 'sunC', 'shadeC')}
    records = np.column_stack([fields['airC'], fields['dewC'], fields['sunC'], fields['shadeC'],
        packet['highC'], packet['lowC'], packet['rainMm'], packet['windKmh'], packet['sunnyHours'], packet['daylightHours']])
    physical = np.column_stack([packet['highC'], packet['lowC'], fields['airC'][:, :11].max(1), fields['airC'][:, 11], fields['dewC'][:, 11], fields['shadeC'][:, 11]])
    month_axis = np.array([int(date[5:7]) for date in dates])
    monthly = np.array([[np.mean(np.asarray(packet['nativeDewMeanC'])[month_axis == month]),
        np.mean(np.asarray(packet['rainMm'])[month_axis == month] >= 1)*100,
        np.mean(np.asarray(packet['sunnyHours'])[month_axis == month]/np.maximum(1, np.asarray(packet['daylightHours'])[month_axis == month]))*100,
        np.mean(np.asarray(packet['windKmh'])[month_axis == month])] for month in range(1, 13)])
    selected = packet['selectedIndices']
    return packet_bytes(packet['fineIndex'], zone, packet['generation'], selected, records[selected], physical, monthly, np.zeros((12, 2)))


def prepare(args):
    from timezonefinder import TimezoneFinder
    import rasterio.features
    from rasterio.transform import from_origin
    started = time.monotonic()
    args.work.mkdir(parents=True, exist_ok=True)
    args.output.mkdir(parents=True, exist_ok=True)
    configuration = json.loads((args.tiles/'configuration.json').read_text())
    native_points = np.fromfile(args.reference_root/'v6/points.bin', dtype=POINT_DTYPE, offset=4)
    native_zones = np.load(args.reference_root/'climate-v6-work/timezones.npy')
    finder = TimezoneFinder(in_memory=True)
    names = sorted(set(finder.timezone_names) | set(native_zones.tolist()))
    names_index = {name: index for index, name in enumerate(names)}
    config = dict(generation=configuration['generation'], chunks=configuration['chunks'], timezoneNames=names,
        source='timezonefinder polygon geometry; containing 30 arc-second pixel centre; ODbL-1.0',
        timezonefinder=importlib.metadata.version('timezonefinder'),
        timezonefinder_data=importlib.metadata.version('timezonefinder-data'),
        reference_points_sha256=file_sha256(args.reference_root/'v6/points.bin'),
        reference_timezones_sha256=file_sha256(args.reference_root/'climate-v6-work/timezones.npy'))
    config_path = args.work/'configuration.json'
    if config_path.exists() and json.loads(config_path.read_text()) != config:
        raise ValueError('Replay preparation configuration differs')
    config_path.write_text(json.dumps(config, indent=2)+'\n')
    shapes = []
    for name in finder.timezone_names:
        for polygon in finder.get_geometry(tz_name=name, coords_as_pairs=True):
            geometry = dict(type='Polygon', coordinates=polygon)
            latitudes = [pair[1] for pair in polygon[0]]
            shapes.append((geometry, names_index[name], min(latitudes), max(latitudes)))
    print('Target timezone polygons', len(shapes), flush=True)
    land = np.memmap(args.land_support/'land.u8', mode='r', dtype='u1', shape=(HEIGHT, WIDTH))
    pair_keys = set()
    zones_output = args.output/'zones'
    zone_files = []
    supported = 0
    for first in range(0, HEIGHT, 256):
        last = min(HEIGHT, first+256)
        north, south = 90-first/120, 90-last/120
        candidates = [(geometry, value) for geometry, value, bottom, top in shapes if top >= south and bottom <= north]
        raster = rasterio.features.rasterize(candidates, out_shape=(last-first, WIDTH),
            transform=from_origin(-180, north, 1/120, 1/120), fill=65535, dtype='uint16')
        valid = np.asarray(land[first:last]) != 0
        raster[~valid] = 65535
        rows, columns = np.nonzero(valid)
        if np.any(raster[valid] == 65535):
            # Rare exact polygon cracks must use the same point-in-polygon API,
            # never an adjacent point's timezone.
            for row, column in zip(*np.nonzero(valid & (raster == 65535))):
                lat, lon = 90-(first+row+.5)/120, -180+(column+.5)/120
                name = finder.timezone_at(lat=lat, lng=lon)
                if name is None: raise ValueError(f'Missing target timezone: {lat},{lon}')
                raster[row, column] = names_index[name]
        fine = (np.floor((first+rows+.5)/30+.5).astype(np.uint32)*1440+
                np.floor((columns+.5)/30+.5).astype(np.uint32)%1440)
        keys = (fine.astype(np.uint64) << 16) | raster[rows, columns]
        pair_keys.update(np.unique(keys).tolist())
        supported += len(rows)
        for column in np.unique(columns//256):
            tile = np.full((256, 256), 65535, dtype='<u2')
            width = min(256, WIDTH-int(column)*256)
            tile[:last-first, :width] = raster[:, column*256:column*256+width]
            header = ZONE_HEADER.pack(b'NWZT', 1, first//256, int(column), 256, bytes.fromhex(config['generation']), 65536)
            path = zones_output/f'{first//256:03d}'/f'{column:03d}.nwz.gz'
            encoded = gzip.compress(header+tile.tobytes(), compresslevel=6, mtime=0)
            write_atomic(path, encoded)
            zone_files.append(dict(path=str(path.relative_to(args.output)), bytes=len(encoded), sha256=hashlib.sha256(encoded).hexdigest()))
        print('Target zones row', first, 'pairs', len(pair_keys), 'pixels', supported, 'seconds', round(time.monotonic()-started, 1), flush=True)
    pairs = np.array(sorted(pair_keys), dtype='<u8')
    np.save(args.work/'pairs.npy', pairs)
    np.save(args.work/'native-zone-fine.npy', native_points['fine'])
    np.save(args.work/'native-zone-index.npy', np.array([names_index[str(name)] for name in native_zones], dtype='<u2'))
    (args.work/'zones-files.json').write_text(json.dumps(zone_files, indent=2)+'\n')
    (args.work/'prepared.json').write_text(json.dumps(dict(seconds=time.monotonic()-started, pairs=len(pairs),
        ownNativePoints=len(np.unique(pairs >> 16)), supportedFinePixels=supported,
        zoneFiles=len(zone_files), zoneBytes=sum(value['bytes'] for value in zone_files)), indent=2)+'\n')
    dates = [str(date) for chunk in config['chunks'] for date in climate_pipeline().chunk_dates(chunk)[1]]
    (args.output/'manifest.json').write_text(json.dumps(dict(format='nwfp-v1', status='building',
        generation=config['generation'], chunks=config['chunks'], eveningModel='uniform', dates=dates,
        timezoneNames=names, packetPath='native/{fine}/{zone}.nwfp.gz', zonePath='zones/{row:03d}/{column:03d}.nwz.gz',
        packetCount=len(pairs), ownNativePoints=len(np.unique(pairs >> 16)), supportedFinePixels=supported), indent=2)+'\n')
    print((args.work/'prepared.json').read_text(), flush=True)


@functools.lru_cache(maxsize=5000)
def sampling(chunk, zone):
    dates, indices, fractions = local_hour_indices(chunk, zone, range(24))
    epoch, _ = climate_pipeline().chunk_dates(chunk)
    utc = indices[:, 10:22]+fractions[:, 10:22]
    offsets = np.array([dt.datetime.combine(date, dt.time(12), zoneinfo.ZoneInfo(zone)).utcoffset().total_seconds()/3600 for date in dates])
    return epoch, dates, indices, fractions, utc, offsets


def build_one_tile(job):
    args, name, keys = job
    pipeline = climate_pipeline()
    config = json.loads((args.work/'configuration.json').read_text())
    dates = [str(date) for chunk in config['chunks'] for date in pipeline.chunk_dates(chunk)[1]]
    month_axis = np.array([int(date[5:7]) for date in dates])
    pairs = [(int(key >> 16), int(key & 65535)) for key in keys]
    pending = [(fine, zone) for fine, zone in pairs if not (args.output/f'native/{fine}/{zone}.nwfp.gz').exists()]
    if not pending: return name, len(pairs), 0
    names = config['timezoneNames']
    native_fine = np.load(args.work/'native-zone-fine.npy', mmap_mode='r')
    native_zones = np.load(args.work/'native-zone-index.npy', mmap_mode='r')
    own_zones = {fine: int(native_zones[np.searchsorted(native_fine, fine)]) for fine, _ in pending}
    required = sorted(set(pending) | {(fine, own_zones[fine]) for fine, _ in pending})
    records = {pair: [] for pair in required}
    own_scores = {pair: [] for pair in required}
    native_cells = np.fromfile(args.native_root/'v6/cells.bin', dtype='<u2').reshape(-1, 2)
    geometric_to_cell = {int(row)*360+int(column): index for index, (row, column) in enumerate(native_cells)}
    cell_scores = {}
    for chunk in config['chunks']:
        path = args.reference_root/f'era5/chunk{chunk}'/name
        supplement = args.weather_cache/f'chunk{chunk}'/name
        # Wait for this independent acquisition; never start or stop the home job.
        while True:
            with np.load(path) as original:
                missing = {'d2m', 'u10', 'v10', 'tp', 'fdir'}-set(original.files)
            if not missing or supplement.exists(): break
            time.sleep(2)
        with np.load(path) as original:
            raw = {field: original[field].astype(np.float32) for field in original.files}
        if missing:
            with np.load(supplement) as extra:
                raw.update({field: extra[field].astype(np.float32) for field in missing})
        wind = np.hypot(raw['u10'], raw['v10'])*3.6
        intermediate = args.native_root/f'climate-v6-work/chunk{chunk}'/name
        if intermediate.exists():
            with np.load(intermediate) as saved:
                features = saved['sums']/saved['counts'][:, None, None]
                scores = pipeline.score_features(pipeline.decode_records(pipeline.encode_records(features)))
                for cell, score in zip(saved['cells'], scores): cell_scores.setdefault(int(cell), []).append(score)
        for zone_index in sorted({zone for _, zone in required}):
            members = [fine for fine, zone in required if zone == zone_index]
            latitude = np.array([90-(fine//1440)*.25 for fine in members])
            longitude = np.array([-180+(fine%1440)*.25 for fine in members])
            rows = np.array([(fine//1440)%12 for fine in members])
            columns = np.array([((fine%1440+720)%1440)%12 for fine in members])
            epoch, chunk_dates, indices, fractions, utc, offsets = sampling(chunk, names[zone_index])
            def sample(field, hours, accumulation=False):
                base = indices[:, hours]+int(accumulation)
                fraction = fractions[:, hours]
                return (field[base[:, :, None], rows, columns]*(1-fraction[:, :, None])+
                        field[base[:, :, None]+1, rows, columns]*fraction[:, :, None])
            air24 = sample(raw['t2m'], slice(None))
            air = air24[:, 10:22]
            dew = sample(raw['d2m'], slice(10, 22))
            sampled_wind = sample(wind, slice(10, 22))
            rain = sample(raw['tp'], slice(10, 22), True)
            direct = sample(raw['fdir'], slice(10, 22), True)
            cosine = np.zeros_like(direct)
            for midpoint in (.1127016654, .5, .8872983346):
                weight = 8/18 if midpoint == .5 else 5/18
                utc_dates = np.datetime64(epoch.date())+np.floor((utc+midpoint)/24).astype('timedelta64[D]')
                day_numbers = (utc_dates-utc_dates.astype('datetime64[Y]')).astype(int)+1
                value, _, _ = pipeline.solar(day_numbers[:, :, None], ((utc%24+midpoint)%24)[:, :, None], latitude, longitude)
                cosine += weight*np.maximum(0, value)
            dni = np.divide(direct, cosine, out=np.zeros_like(direct), where=cosine > .001)
            sun, shade, capped = pipeline.taste.felt(air, dew, sampled_wind, dni)
            day_numbers = np.array([date.timetuple().tm_yday for date in chunk_dates])[:, None]
            _, equation, declination = pipeline.solar(day_numbers, 12, latitude, longitude)
            lat_radians = np.deg2rad(latitude)
            argument = np.cos(np.deg2rad(90.833))/(np.cos(lat_radians)*np.cos(declination))-np.tan(lat_radians)*np.tan(declination)
            sunset = (720-4*longitude-equation+4*np.rad2deg(np.arccos(np.clip(argument, -1, 1))))/60
            wrap = 24*np.rint((offsets[:, None]-longitude/15)/24)
            daylight = np.clip(np.ceil(sunset+offsets[:, None]-wrap)-10, 1, 11)
            daylight = np.where(argument >= 1, 1, np.where(argument <= -1, 11, daylight))
            daylight_mask = np.arange(11)[None, :, None] < daylight[:, None, :]
            high, low = air24.max(1), air24.min(1)
            total_rain = rain[:, :11].sum(1)
            mean_wind = sampled_wind.mean(1)
            sunny = ((dni[:, :11] >= 300) & daylight_mask).sum(1)
            for position, fine in enumerate(members):
                record = np.column_stack([air[:, :, position], dew[:, :, position], sun[:, :, position], shade[:, :, position],
                    high[:, position], low[:, position], total_rain[:, position], mean_wind[:, position], sunny[:, position], daylight[:, position]])
                physical = np.column_stack([high[:, position], low[:, position], air[:, :11, position].max(1), air[:, 11, position], dew[:, 11, position], shade[:, 11, position]])
                native_dew = dew[:, :, position].mean(1)
                records[(fine, zone_index)].append((record, physical, native_dew))
                features = np.zeros((len(chunk_dates), 29))
                features[:, :11] = sun[:, :11, position]
                features[:, 11:23] = shade[:, :, position]
                features[:, 23:] = np.column_stack([capped[:, :, position].mean(1), total_rain[:, position], mean_wind[:, position], sunny[:, position], daylight[:, position], air[:, :11, position].max(1)])
                own_scores[(fine, zone_index)].append(pipeline.score_features(pipeline.decode_records(pipeline.encode_records(features))))
    bytes_written = 0
    for fine, zone in pending:
        record = np.concatenate([value[0] for value in records[(fine, zone)]])
        physical = np.concatenate([value[1] for value in records[(fine, zone)]])
        native_dew = np.concatenate([value[2] for value in records[(fine, zone)]])
        geometric = min(fine//1440//4, 179)*360+(fine%1440)//4
        cell = geometric_to_cell.get(geometric)
        if cell in cell_scores and len(cell_scores[cell]) == len(config['chunks']):
            scores = np.concatenate(cell_scores[cell])
        else:
            scores = np.concatenate(own_scores[(fine, zone)])
        selected = []
        for month in range(1, 13):
            indices = np.flatnonzero(month_axis == month)
            order = np.argsort(scores[indices], kind='stable')
            selected.extend(indices[order[np.floor((np.arange(8)+.5)*len(order)/8).astype(int)]])
        monthly = np.array([[native_dew[month_axis == month].mean(), (record[month_axis == month, 50] >= 1).mean()*100,
            (record[month_axis == month, 52]/np.maximum(1, record[month_axis == month, 53])).mean()*100,
            record[month_axis == month, 51].mean()] for month in range(1, 13)])
        clock_delta = np.zeros((12, 2))
        if zone != own_zones[fine]:
            own_physical = np.concatenate([value[1] for value in records[(fine, own_zones[fine])]])
            for month in range(1, 13):
                keep = month_axis == month
                clock_delta[month-1] = own_physical[keep, :2].mean(0)-physical[keep, :2].mean(0)
        payload = packet_bytes(fine, zone, config['generation'], selected, record[selected], physical, monthly, clock_delta)
        encoded = gzip.compress(payload, compresslevel=6, mtime=0)
        write_atomic(args.output/f'native/{fine}/{zone}.nwfp.gz', encoded)
        bytes_written += len(encoded)
    return name, len(pending), bytes_written


def write_attribution(args):
    target = args.output/'timezones-licence'
    evidence = target/'evidence'
    evidence.mkdir(parents=True, exist_ok=True)
    for source in (args.land_support.parents[1]/'fine-sources/timezones').glob('*'):
        if source.is_file(): shutil.copy2(source, evidence/source.name)
    packages = {}
    for package, license_name, output_name in [('timezonefinder', 'LICENSE', 'timezonefinder-MIT.txt'),
                                              ('timezonefinder-data', 'DATA_LICENSE', 'ODbL-1.0.txt')]:
        distribution = importlib.metadata.distribution(package)
        license_path = next(path for path in distribution.files if pathlib.Path(str(path)).name == license_name)
        shutil.copyfile(distribution.locate_file(license_path), target/output_name)
        metadata_path = evidence/(package+'-METADATA.txt')
        metadata_path.write_text(distribution.read_text('METADATA'))
        packages[package] = dict(version=distribution.version, license_sha256=file_sha256(target/output_name),
                                 metadata_sha256=file_sha256(metadata_path))
        if package == 'timezonefinder-data':
            source_files = [path for path in distribution.files if str(path).startswith('timezonefinder_data/data/')]
            packages[package]['installed_data_sha256'] = {str(path): file_sha256(distribution.locate_file(path)) for path in source_files}
    (target/'SOURCE-PROVENANCE.json').write_text(json.dumps(packages, indent=2)+'\n')
    (target/'ATTRIBUTION.txt').write_text(
        'Target-place timezone lookup: Open Database License 1.0.\n'
        'Contains timezone-boundary-builder information processed by timezonefinder and © OpenStreetMap contributors.\n'
        'https://github.com/evansiroky/timezone-boundary-builder\n'
        'https://www.openstreetmap.org/copyright\n'
        'https://opendatacommons.org/licenses/odbl/1-0/\n'
        'Changes: IANA polygon membership rasterized at 30 arc-second pixel centres, restricted to supported land; '
        'published independently as zones/*.nwz.gz plus timezoneNames in the replay manifest. '
        'This independently supplied timezone lookup is available under ODbL 1.0. Weather values keep their stated licence.\n')
    (args.output/'ATTRIBUTION.txt').write_text(
        'Native replay weather: CC BY 4.0.\n'
        'ERA5 hourly, ECMWF / Copernicus Climate Change Service (C3S), DOI:10.24381/cds.adbb2d47, via the anonymous Earthmover archive.\n'
        'https://registry.opendata.aws/earthmover-era5/\n'
        'https://creativecommons.org/licenses/by/4.0/\n'
        'Contains modified Copernicus Climate Change Service information (2026).\n'
        'Changes: own native point weather sampled in target-place local clocks, apparent temperature, daily extrema and weather summaries, '
        'representative-day selection, and monthly native-clock temperature reference differences.\n'
        'Neither the European Commission nor ECMWF is responsible for any use of this Copernicus information. No endorsement is implied.\n'
        'Independent target-place timezone lookup: © OpenStreetMap contributors, ODbL 1.0; see timezones-licence/.\n'
        'Exact-pixel correction tiles are a separate database with their own attribution and CC BY-SA 4.0 weather values.\n')


def build(args):
    started = time.monotonic()
    config = json.loads((args.work/'configuration.json').read_text())
    pairs = np.load(args.work/'pairs.npy')
    groups = {}
    for key in pairs:
        fine = int(key) >> 16
        row, column = divmod(fine, 1440)
        name = f'r{row//12:02d}c{((column+720)%1440)//12:03d}.npz'
        groups.setdefault(name, []).append(int(key))
    priority = set()
    if args.priority_queries:
        for query in json.loads(args.priority_queries.read_text()):
            pixel_row = int(np.floor((90-query['lat'])*120))
            pixel_column = int(np.floor((query['lon']+180)*120))%43200
            row = int(np.floor((pixel_row+.5)/30+.5))
            column = int(np.floor((pixel_column+.5)/30+.5))%1440
            priority.add(f'r{row//12:02d}c{((column+720)%1440)//12:03d}.npz')
    def job_order(item):
        name, _ = item
        with np.load(args.reference_root/f"era5/chunk{config['chunks'][0]}"/name) as raw:
            full = 'd2m' in raw.files
        ready = full or all((args.weather_cache/f'chunk{chunk}'/name).exists() for chunk in config['chunks'])
        return (not ready, name not in priority, name)
    jobs = [(args, name, keys) for name, keys in sorted(groups.items(), key=job_order)]
    only_tiles = args.only_tiles or (json.loads(args.only_tiles_file.read_text()) if args.only_tiles_file else None)
    if only_tiles: jobs = [job for job in jobs if job[1] in only_tiles]
    completed = packets = byte_count = 0
    with mp.get_context('spawn').Pool(args.workers) as pool:
        for name, count, size in pool.imap_unordered(build_one_tile, jobs):
            completed += 1; packets += count; byte_count += size
            if completed%25 == 0 or completed == len(jobs):
                progress = dict(tiles=completed, totalTiles=len(jobs), packets=packets, newBytes=byte_count, seconds=time.monotonic()-started)
                (args.work/args.progress_name).write_text(json.dumps(progress, indent=2)+'\n')
                print(json.dumps(progress), flush=True)
    if only_tiles: return
    write_attribution(args)
    dates = [str(date) for chunk in config['chunks'] for date in climate_pipeline().chunk_dates(chunk)[1]]
    outputs = []
    for key in pairs:
        fine, zone = int(key) >> 16, int(key) & 65535
        path = args.output/f'native/{fine}/{zone}.nwfp.gz'
        outputs.append(dict(path=str(path.relative_to(args.output)), bytes=path.stat().st_size, sha256=file_sha256(path)))
    zone_files = json.loads((args.work/'zones-files.json').read_text())
    manifest = dict(format='nwfp-v1', version=1, generation=config['generation'], chunks=config['chunks'], eveningModel='uniform',
        dates=dates, timezoneNames=config['timezoneNames'], targetClock='30 arc-second containing pixel centre',
        scope='Worldwide supported correction pixels, own native ERA5 point and target local clock',
        packetPath='native/{fine}/{zone}.nwfp.gz', zonePath='zones/{row:03d}/{column:03d}.nwz.gz',
        packetCount=len(outputs), compressedPacketBytes=sum(item['bytes'] for item in outputs),
        zoneCount=len(zone_files), compressedZoneBytes=sum(item['bytes'] for item in zone_files),
        physicalFields=['H24', 'L24', 'dayMaxAir', 'air21', 'dew21', 'nativeShade21'],
        monthlyFields=['nativeDewMean', 'wetPercent', 'meanDailySunnyPercent', 'meanWindKmh'],
        clockBaseOffset='Trailing 12x2 float32 own-native-clock H/L normal minus target-clock H/L normal; zero for same clock',
        selectedRecordFields='air12,dew12,sun12,shade12,H24,L24,rainMm,windKmh,sunnyHours,daylightHours',
        ownNativePoints=len(np.unique(pairs >> 16)),
        supportedFinePixels=json.loads((args.work/'prepared.json').read_text())['supportedFinePixels'],
        inventory='BUILD-INVENTORY.json.gz', checksums='files.sha256', packetIndex='packet-index.bin',
        seconds=time.monotonic()-started,
        written_by='AI, GPT-6.1 Sol, 2026.10.18', builder_sha256=file_sha256(__file__),
        licence='CC-BY-4.0 weather; independent target timezone lookup ODbL-1.0',
        completedUTC=dt.datetime.now(dt.timezone.utc).isoformat())
    write_atomic(args.output/'BUILD-INVENTORY.json.gz', gzip.compress(json.dumps(outputs+zone_files, separators=(',', ':')).encode(), mtime=0))
    (args.output/'files.sha256').write_text(''.join(f"{item['sha256']}  {item['path']}\n" for item in outputs+zone_files))
    write_atomic(args.output/'packet-index.bin', pairs.astype('<u8').tobytes())
    manifest['status'] = 'complete'
    (args.output/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
    print(json.dumps({key: value for key, value in manifest.items() if key not in ('files', 'dates', 'timezoneNames')}), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('prepare', 'build', 'compact-diagnostic', 'attribution'))
    parser.add_argument('--tiles', type=pathlib.Path, required=True)
    parser.add_argument('--reference-root', type=pathlib.Path, required=True)
    parser.add_argument('--native-root', type=pathlib.Path, required=True)
    parser.add_argument('--land-support', type=pathlib.Path, required=True)
    parser.add_argument('--weather-cache', type=pathlib.Path, required=True)
    parser.add_argument('--work', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    parser.add_argument('--workers', type=int, default=4)
    parser.add_argument('--only-tiles', nargs='*')
    parser.add_argument('--only-tiles-file', type=pathlib.Path)
    parser.add_argument('--priority-queries', type=pathlib.Path)
    parser.add_argument('--progress-name', default='progress.json')
    parser.add_argument('--diagnostic', type=pathlib.Path)
    args = parser.parse_args()
    if args.mode == 'prepare': prepare(args)
    elif args.mode == 'build': build(args)
    elif args.mode == 'attribution': write_attribution(args)
    else:
        config = json.loads((args.tiles/'configuration.json').read_text())
        diagnostic = json.loads((args.diagnostic/'manifest.json').read_text())
        names = []
        dates = None
        outputs = []
        for path in sorted((args.diagnostic/'native').glob('*.json.gz')):
            packet = json.loads(gzip.decompress(path.read_bytes()))
            if packet['zone'] not in names: names.append(packet['zone'])
            zone = names.index(packet['zone'])
            dates = packet['dates']
            target = args.output/f"native/{packet['fineIndex']}/{zone}.nwfp.gz"
            encoded = gzip.compress(compact_json(packet, zone, dates), compresslevel=6, mtime=0)
            write_atomic(target, encoded)
            outputs.append(dict(path=str(target.relative_to(args.output)), fine=packet['fineIndex'], zone=zone, bytes=len(encoded)))
        (args.output/'manifest.json').write_text(json.dumps(dict(format='nwfp-v1', generation=config['generation'], eveningModel='uniform', dates=dates, timezoneNames=names, files=outputs), indent=2)+'\n')
        print(json.dumps(outputs), flush=True)


if __name__ == '__main__': main()
