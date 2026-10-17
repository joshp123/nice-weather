"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Measure native coastal sunshine against the exact retained production cell days.
Run within devenv, with numpy and pillow available. All generated data stay on SSD.
"""
import argparse
import csv
import datetime as dt
import hashlib
import importlib.util
import json
import pathlib
import sys
import zoneinfo

import numpy as np
from PIL import Image

PROJECT = pathlib.Path(__file__).resolve().parents[2]
sys.path.insert(0, str(PROJECT / 'research/climate-v6'))
import pipeline
import validate
spec = importlib.util.spec_from_file_location('default_scores', PROJECT / 'src/engine/default_scores.py')
default_scores = importlib.util.module_from_spec(spec)
spec.loader.exec_module(default_scores)

PLACES = {
    'lima': ('Lima', -12.046, -77.043, 'America/Lima'),
    'antofagasta': ('Antofagasta', -23.650, -70.400, 'America/Santiago'),
    'iquique': ('Iquique', -20.230, -70.136, 'America/Santiago'),
    'swakopmund': ('Swakopmund', -22.678, 14.526, 'Africa/Windhoek'),
    'walvis-bay': ('Walvis Bay', -22.957, 14.506, 'Africa/Windhoek'),
    'valparaiso': ('Valparaíso', -33.047, -71.613, 'America/Santiago'),
    'san-francisco': ('San Francisco', 37.775, -122.419, 'America/Los_Angeles'),
    'malaga': ('Málaga', 36.721, -4.421, 'Europe/Madrid'),
    'seville': ('Seville', 37.389, -5.984, 'Europe/Madrid'),
    'cape-town': ('Cape Town', -33.925, 18.424, 'Africa/Johannesburg'),
    'tenerife-south': ('Tenerife south', 28.055, -16.731, 'Atlantic/Canary'),
    'san-diego': ('San Diego', 32.716, -117.162, 'America/Los_Angeles'),
    'rotterdam': ('Rotterdam', 51.923, 4.479, 'Europe/Amsterdam'),
    'singapore': ('Singapore', 1.352, 103.820, 'Asia/Singapore'),
}


def tile_for_point(row, column):
    return f'r{row // 12:02d}c{column // 12:03d}.npz'


def sample_days(raw, row, column, zone_name, chunk):
    """Use the production UTC interval, solar integral and daylight definition."""
    epoch, dates = pipeline.chunk_dates(chunk)
    zone = zoneinfo.ZoneInfo(zone_name)
    latitude, longitude = 90 - row / 4, (column / 4 + 180) % 360 - 180
    utc = np.array([[(dt.datetime.combine(date, dt.time(hour), zone).astimezone(dt.timezone.utc) - epoch).total_seconds() / 3600
                     for hour in range(10, 22)] for date in dates])
    low = np.floor(utc).astype(int)
    fraction = utc - low

    def sample(field, accumulated=False):
        lower = low + int(accumulated)
        return field[lower] * (1 - fraction) + field[lower + 1] * fraction

    direct = sample(raw['fdir'], True)
    cosine = np.zeros_like(direct)
    for position, weight in zip((.1127016654, .5, .8872983346), (5 / 18, 8 / 18, 5 / 18)):
        utc_dates = np.datetime64(epoch.date()) + np.floor((utc + position) / 24).astype('timedelta64[D]')
        day_numbers = (utc_dates - utc_dates.astype('datetime64[Y]')).astype(int) + 1
        cosine += weight * np.maximum(0, pipeline.solar(day_numbers, (utc + position) % 24, latitude, longitude)[0])
    dni = np.divide(direct, cosine, out=np.zeros_like(direct), where=cosine > .001)
    day_numbers = np.array([date.timetuple().tm_yday for date in dates])
    _, equation, declination = pipeline.solar(day_numbers, 12, latitude, longitude)
    latitude_radians = np.deg2rad(latitude)
    argument = (np.cos(np.deg2rad(90.833)) / (np.cos(latitude_radians) * np.cos(declination))
                - np.tan(latitude_radians) * np.tan(declination))
    sunset_utc = (720 - 4 * longitude - equation + 4 * np.rad2deg(np.arccos(np.clip(argument, -1, 1)))) / 60
    offsets = np.array([dt.datetime.combine(date, dt.time(12), zone).utcoffset().total_seconds() / 3600 for date in dates])
    sunset_local = sunset_utc + offsets - 24 * np.rint((offsets - longitude / 15) / 24)
    daylight = np.clip(np.ceil(sunset_local) - 10, 1, 11)
    daylight = np.where(argument >= 1, 1, np.where(argument <= -1, 11, daylight))
    mask = np.arange(11)[None, :] < daylight[:, None]
    sunny = ((dni[:, :11] >= 300) & mask).sum(1)
    result = dict(dni=dni[:, :11], horizontal_direct=direct[:, :11], daylight=daylight.astype(np.uint8), sunny=sunny.astype(np.uint8))
    if 't2m' in raw:
        temperature, dew = sample(raw['t2m']), sample(raw['d2m'])
        wind = sample(np.hypot(raw['u10'], raw['v10']) * 3.6)
        sun, shade, dew = pipeline.taste.felt(temperature, dew, wind, dni)
        features = np.zeros((len(dates), 29), np.float32)
        features[:, :11], features[:, 11:23] = sun[:, :11], shade
        features[:, 23] = dew.mean(1)
        features[:, 24] = sample(raw['tp'], True)[:, :11].sum(1)
        features[:, 25] = wind.mean(1)
        features[:, 26], features[:, 27] = sunny, daylight
        features[:, 28] = temperature[:, :11].max(1)
        result['features'] = features
    return result


def sha256(path):
    return hashlib.file_digest(path.open('rb'), 'sha256').hexdigest()


def independent_cosine(utc_hours_since_1940, latitude, longitude):
    """Julian-century NOAA/Meeus geometry, independent of the pipeline formula."""
    julian = 2429629.5 + utc_hours_since_1940 / 24
    centuries = (julian - 2451545) / 36525
    rad = np.deg2rad
    mean_longitude = (280.46646 + centuries * (36000.76983 + centuries * .0003032)) % 360
    mean_anomaly = 357.52911 + centuries * (35999.05029 - .0001537 * centuries)
    eccentricity = .016708634 - centuries * (.000042037 + .0000001267 * centuries)
    centre = (np.sin(rad(mean_anomaly)) * (1.914602-centuries*(.004817+.000014*centuries))
              + np.sin(rad(2*mean_anomaly))*(.019993-.000101*centuries) + np.sin(rad(3*mean_anomaly))*.000289)
    omega = 125.04-1934.136*centuries
    longitude_apparent = mean_longitude+centre-.00569-.00478*np.sin(rad(omega))
    obliquity = 23+(26+(21.448-centuries*(46.815+centuries*(.00059-.001813*centuries)))/60)/60+.00256*np.cos(rad(omega))
    declination = np.arcsin(np.sin(rad(obliquity))*np.sin(rad(longitude_apparent)))
    tangent = np.tan(rad(obliquity)/2)**2
    equation = 4*np.rad2deg(tangent*np.sin(2*rad(mean_longitude))-2*eccentricity*np.sin(rad(mean_anomaly))
                           +4*eccentricity*tangent*np.sin(rad(mean_anomaly))*np.cos(2*rad(mean_longitude))
                           -.5*tangent*tangent*np.sin(4*rad(mean_longitude))-1.25*eccentricity**2*np.sin(2*rad(mean_anomaly)))
    hour_angle = rad((((utc_hours_since_1940 % 24)*60+equation+4*longitude)%1440)/4-180)
    return np.maximum(0,np.sin(rad(latitude))*np.sin(declination)+np.cos(rad(latitude))*np.cos(declination)*np.cos(hour_angle))


def audit_archive(root, output):
    """Verify the saved Lima radiation independently against its anonymous source."""
    import icechunk
    import pcodec
    import zarr
    storage = icechunk.s3_storage(bucket='earthmover-icechunk-era5',prefix='icechunkV2',region='us-east-1',anonymous=True)
    store = icechunk.Repository.open(storage).readonly_session(snapshot_id='TZ2KX6A24CJ5DG17T7MG').store
    archive = zarr.open_group(store,mode='r',path='single/temporal')
    chunk,row,column = 82,408,1132
    native = archive['fdir'][chunk*8736:(chunk+1)*8736,row:row+1,column:column+1].reshape(-1)/3600
    with np.load(root/f'era5/chunk{chunk}'/tile_for_point(row,column)) as data:
        cached = data['fdir'][:,row%12,column%12]
    report = dict(fdir_attributes=dict(archive['fdir'].attrs),latitude=90-row/4,
                  longitude=(column/4+180)%360-180,chunk=chunk,
                  maximum_float16_difference_w_m2=float(np.max(np.abs(native-cached))),
                  encoded_equal=bool(np.array_equal(native.astype(np.float16),cached)))
    (output/'archive-cache-audit.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report,indent=2))


def audit_browser_selection(output):
    """Compare API pixel selection with browser selection without opening raw ERA5."""
    metadata = json.loads((output/'provenance.json').read_text())
    points = np.fromfile(output/'points-baseline.bin',dtype=validate.POINT_DTYPE,offset=4)
    cells = np.fromfile(output/'cells-baseline.bin',dtype='<u2').reshape(-1,2)
    ordinals = default_scores.nearest_land_ordinals(cells)
    fine_scores = np.memmap(output/'scores-baseline.bin',dtype=np.uint8,mode='r',shape=(721*1440,12))
    rows, columns = points['fine']//1440,points['fine']%1440
    results = []
    for slug,place in metadata['places'].items():
        x,y = (place['requested_longitude']+180)*4,(90-place['requested_latitude'])*4
        candidates = np.where((abs(rows-round(y))<=2)&(abs(columns-round(x))<=2))[0]
        selected = candidates[np.argmin((rows[candidates]-y)**2+(columns[candidates]-x)**2)]
        browser_fine = int(points['fine'][selected])
        r,c = divmod(browser_fine,1440)
        x_cell,y_cell = c/4-.5,r/4-.5
        base_x,base_y = int(np.floor(x_cell)),int(np.floor(y_cell))
        fraction_x,fraction_y = x_cell-base_x,y_cell-base_y
        contributor_cells,weights = [],[]
        for vertical in range(2):
            for horizontal in range(2):
                contributor_cells.append(ordinals[np.clip(base_y+vertical,0,179),(base_x+horizontal)%360])
                weights.append((fraction_x if horizontal else 1-fraction_x)*(fraction_y if vertical else 1-fraction_y))
        with np.load(output/f'{slug}.npz') as measured:
            for month in range(1,13):
                with Image.open(output/f'baseline-climate/offsets-{month:02}.png') as image:
                    thermal_offset = (float(np.asarray(image).flat[browser_fine])-128)/10
                records = np.memmap(output/f'baseline-climate/month-{month:02}.bin',dtype=np.uint8,mode='r',shape=(len(cells),8,15))[contributor_cells]
                levels = default_scores.cell_level_scores(records)
                level = float(np.clip((thermal_offset+15)/3,0,10))
                lower = min(int(np.floor(level)),9)
                browser_score = sum(weight*(values[lower]*(1-(level-lower))+values[lower+1]*(level-lower))
                                    for weight,values in zip(weights,levels))
                results.append(dict(slug=slug,place=place['name'],month=month,
                                    worker_latitude=place['selected_latitude'],worker_longitude=place['selected_longitude'],
                                    browser_latitude=90-r/4,browser_longitude=c/4-180,
                                    worker_score=float(measured['published_score_bilinear'][month-1]),browser_score=float(browser_score),
                                    browser_minus_worker_score=float(browser_score-measured['published_score_bilinear'][month-1]),
                                    worker_score_pixel=int(measured['published_score_pixel'][month-1]),browser_score_pixel=int(fine_scores[browser_fine,month-1])))
    with (output/'browser-selection-audit.csv').open('w') as handle:
        writer = csv.DictWriter(handle,fieldnames=results[0].keys())
        writer.writeheader()
        writer.writerows(results)
    print(f'{len(results)} browser/API selected-pixel comparisons; no raw ERA5 opened.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=pathlib.Path, default=pipeline.ROOT)
    parser.add_argument('--output', type=pathlib.Path)
    parser.add_argument('--chunks', type=int, nargs='+', default=[76, 78, 80, 82, 84])
    parser.add_argument('--archive-only',action='store_true',help='Requires icechunk, pcodec and zarr; audits one raw point without extraction.')
    parser.add_argument('--browser-only',action='store_true',help='Audit selected-pixel score differences from retained baseline files only.')
    args = parser.parse_args()
    output = args.output or args.root / 'sun/era5'
    output.mkdir(parents=True, exist_ok=True)
    if args.archive_only:
        audit_archive(args.root,output)
        return
    if args.browser_only:
        audit_browser_selection(output)
        return
    for source, target in [('v6/points.bin', 'points-baseline.bin'), ('v6/cells.bin', 'cells-baseline.bin'),
                           ('v6/climate/facts.bin', 'facts-baseline.bin'), ('v6/climate/meta.json', 'meta-baseline.json'),
                           ('v6/climate/scores.bin', 'scores-baseline.bin')]:
        if not (output / target).exists():
            (output / target).write_bytes((args.root / source).read_bytes())
    for month in range(1, 13):
        for filename in [f'month-{month:02}.bin', f'offsets-{month:02}.png']:
            target = output / 'baseline-climate' / filename
            if not target.exists():
                target.parent.mkdir(exist_ok=True)
                target.write_bytes((args.root / 'v6/climate' / filename).read_bytes())
    points = np.fromfile(output / 'points-baseline.bin', dtype=validate.POINT_DTYPE, offset=4)
    cells = np.fromfile(output / 'cells-baseline.bin', dtype='<u2').reshape(-1, 2)
    ordinals = default_scores.nearest_land_ordinals(cells)
    facts = np.memmap(output / 'facts-baseline.bin', dtype=np.uint8, mode='r', shape=(721 * 1440, 12, 6))
    zones = np.load(args.root / 'climate-v6-work/timezones.npy')
    latitudes, longitudes = 90 - points['fine'] // 1440 / 4, points['fine'] % 1440 / 4 - 180
    lsm = np.load(args.root / 'era5/static_lsm.npy')
    default_score_bytes = np.memmap(output / 'scores-baseline.bin', dtype=np.uint8, mode='r', shape=(721*1440, 12))
    fine_to_point_id = {int(fine):index for index,fine in enumerate(points['fine'])}
    rows, neighbour_rows, radiation_rows, provenance = [], [], [], {}
    for slug, (name, latitude, longitude, city_zone) in PLACES.items():
        distances = (latitudes - latitude) ** 2 + ((longitudes - longitude) * np.cos(np.deg2rad(latitude))) ** 2
        rounded_row, rounded_column = int(np.floor((90-latitude)*4+.5)), int(np.floor((longitude+180)*4+.5)) % 1440
        candidate_fine = [r*1440+c for r in range(max(0,rounded_row-2), min(721,rounded_row+3))
                          for c in range(max(0,rounded_column-2),min(1440,rounded_column+3)) if r*1440+c in fine_to_point_id]
        fine = min(candidate_fine,key=lambda fine:(fine//1440-rounded_row)**2+(fine%1440-rounded_column)**2)
        point_id = fine_to_point_id[fine]
        point = points[point_id]
        cell_id = int(point['cell'])
        fine = int(point['fine'])
        row, shifted_column = divmod(fine, 1440)
        column = (shifted_column + 720) % 1440
        nearest_row, nearest_column = round((90 - latitude) * 4), round((longitude % 360) * 4) % 1440
        neighbours = sorted(set([(r, c % 1440) for r in range(nearest_row - 2, nearest_row + 3)
                                 for c in range(nearest_column - 2, nearest_column + 3)] + [(row, column)]))
        local_neighbours = [(r, c) for r, c in neighbours if (args.root / f'era5/chunk{args.chunks[0]}' / tile_for_point(r, c)).exists()]
        neighbour_index = local_neighbours.index((nearest_row, nearest_column))
        point_neighbour_index = local_neighbours.index((row, column))
        cell_x, cell_y = shifted_column/4-.5, row/4-.5
        base_x, base_y = int(np.floor(cell_x)),int(np.floor(cell_y))
        fraction_x, fraction_y = cell_x-base_x,cell_y-base_y
        bilinear_cells, bilinear_weights = [], []
        for vertical in range(2):
            for horizontal in range(2):
                bilinear_cells.append(int(ordinals[np.clip(base_y+vertical,0,179),(base_x+horizontal)%360]))
                bilinear_weights.append((fraction_x if horizontal else 1-fraction_x)*(fraction_y if vertical else 1-fraction_y))
        collected = {key: [] for key in ['dates', 'chunk', 'production_records', 'cell_features', 'point_features', 'native_dni',
                                       'native_horizontal_direct', 'native_sunny_hours', 'native_daylight_hours', 'nearby_sunny_hours', 'nearby_daylight_hours']}
        for chunk in args.chunks:
            print(name, chunk, flush=True)
            intermediate_path = args.root / f'climate-v6-work/chunk{chunk}' / tile_for_point(row, column)
            with np.load(intermediate_path) as data:
                cell_index = list(data['cells']).index(cell_id)
                cell_features = data['sums'][cell_index] / data['counts'][cell_index]
                count = int(data['counts'][cell_index])
            expected = int((points['cell'] == cell_id).sum())
            if count != expected:
                raise ValueError(f'{name}: cell crosses retained tile boundary: {count} versus {expected}')
            nearby_sun, nearby_daylight = [], []
            radiation_tiles = {}
            for r, c in local_neighbours:
                path = args.root / f'era5/chunk{chunk}' / tile_for_point(r, c)
                # Only radiation is needed away from the selected production point.
                fields = ['fdir', 't2m', 'd2m', 'u10', 'v10', 'tp'] if (r, c) == (row, column) else ['fdir']
                if str(path) not in radiation_tiles:
                    with np.load(path) as data:
                        radiation_tiles[str(path)] = data['fdir']
                raw = {'fdir':radiation_tiles[str(path)][:,r%12,c%12].astype(np.float32)}
                if len(fields)>1:
                    with np.load(path) as data:
                        raw.update({field: data[field][:, r % 12, c % 12].astype(np.float32) for field in fields[1:]})
                sampled = sample_days(raw, r, c, str(zones[point_id]), chunk)
                nearby_sun.append(sampled['sunny'])
                nearby_daylight.append(sampled['daylight'])
                if (r, c) == (row, column):
                    selected_sample = sampled
            dates = np.array(pipeline.chunk_dates(chunk)[1], dtype='datetime64[D]')
            for key, values in dict(dates=dates, chunk=np.full(len(dates), chunk, np.uint8),
                                    production_records=pipeline.encode_records(cell_features), cell_features=cell_features,
                                    point_features=selected_sample['features'], native_dni=selected_sample['dni'],
                                    native_horizontal_direct=selected_sample['horizontal_direct'],
                                    native_sunny_hours=selected_sample['sunny'], native_daylight_hours=selected_sample['daylight'],
                                    nearby_sunny_hours=np.array(nearby_sun).T, nearby_daylight_hours=np.array(nearby_daylight).T).items():
                collected[key].append(values)
        arrays = {key: np.concatenate(values) for key, values in collected.items()}
        months = (arrays['dates'].astype('datetime64[M]').astype(int) % 12) + 1
        selected_zone = zoneinfo.ZoneInfo(str(zones[point_id]))
        origin = dt.datetime(1940,1,1,tzinfo=dt.timezone.utc)
        utc_starts = np.array([
            [(dt.datetime.combine(dt.date.fromisoformat(str(date)),dt.time(hour),selected_zone).astimezone(dt.timezone.utc)-origin).total_seconds()/3600
             for hour in range(10,21)] for date in arrays['dates']])
        independent_hour_cosine = independent_cosine(utc_starts[:,:,None]+(np.arange(60)+.5)/60,90-row/4,shifted_column/4-180).mean(2)
        independent_dni = np.divide(arrays['native_horizontal_direct'],independent_hour_cosine,
                                    out=np.zeros_like(arrays['native_dni']),where=independent_hour_cosine>.001)
        independent_sunny = ((independent_dni>=300)&(np.arange(11)<arrays['native_daylight_hours'][:,None])).sum(1)
        fine_offset, published_records, published_score, published_share = [], [], [], []
        bilinear_records, bilinear_score = [], []
        for month in range(1, 13):
            mask = months == month
            with Image.open(output / f'baseline-climate/offsets-{month:02}.png') as image:
                offset = (float(np.array(image).flat[fine]) - 128) / 10
            all_selected = np.memmap(output / f'baseline-climate/month-{month:02}.bin', dtype=np.uint8, mode='r', shape=(len(cells), 8, 15))
            selected = all_selected[cell_id].copy()
            neighbours_selected = all_selected[bilinear_cells].copy()
            bilinear_records.append(neighbours_selected)
            levels = default_scores.cell_level_scores(neighbours_selected)
            level = float(np.clip((offset+15)/3,0,10))
            lower = min(int(np.floor(level)),9)
            weighted = sum(weight * (scores[lower]*(1-(level-lower))+scores[lower+1]*(level-lower))
                           for scores,weight in zip(levels,bilinear_weights))
            bilinear_score.append(float(weighted))
            fine_offset.append(offset)
            published_records.append(selected)
            published_score.append(float(validate.score_records(selected, offset).mean()))
            published_share.append(float((selected[:, 13] / selected[:, 14]).mean()))
            records = arrays['production_records'][mask]
            raw_counts, raw_daylight = arrays['native_sunny_hours'][mask], arrays['native_daylight_hours'][mask]
            for hour in [12,14,16]:
                radiation_rows.append(dict(slug=slug,place=name,month=month,local_hour=hour,days=int(mask.sum()),
                                           horizontal_direct_w_m2=float(arrays['native_horizontal_direct'][mask,hour-10].mean()),
                                           pipeline_dni_w_m2=float(arrays['native_dni'][mask,hour-10].mean()),
                                           independent_dni_w_m2=float(independent_dni[mask,hour-10].mean()),
                                           pipeline_sunny_percent=100*float((raw_counts/raw_daylight).mean()),
                                           independent_sunny_percent=100*float((independent_sunny[mask]/raw_daylight).mean())))
            rows.append(dict(slug=slug, place=name, month=month, days=int(mask.sum()), cell_points=count,
                             cell_sunny_percent=100 * float((records[:, 13] / records[:, 14]).mean()),
                             cell_unrounded_sunny_percent=100 * float((arrays['cell_features'][mask, 26] / arrays['cell_features'][mask, 27]).mean()),
                             selected_native_sunny_percent=100 * float((raw_counts / raw_daylight).mean()),
                             nearest_raw_sunny_percent=100 * float((arrays['nearby_sunny_hours'][mask, neighbour_index] / arrays['nearby_daylight_hours'][mask, neighbour_index]).mean()),
                             native_pooled_daylight_sunny_percent=100 * float(raw_counts.sum() / raw_daylight.sum()),
                             published_sample_sunny_percent=100 * published_share[-1],
                             published_fact_sunny_percent=int(facts[fine, month - 1, 4]),
                             published_score=published_score[-1], offset_c=offset,
                             published_score_pixel=int(default_score_bytes[fine,month-1]), published_score_bilinear=float(weighted),
                             full_cell_score=float(validate.score_records(records, offset).mean())))
            for index, (r, c) in enumerate(local_neighbours):
                neighbour_rows.append(dict(slug=slug, month=month, latitude=90-r/4, longitude=(c/4+180)%360-180,
                                           land_fraction=float(lsm[r, c]), selected=int(index == point_neighbour_index),
                                           nearest_raw=int(index == neighbour_index),
                                           sunny_percent=100 * float((arrays['nearby_sunny_hours'][mask, index] / arrays['nearby_daylight_hours'][mask, index]).mean())))
        arrays.update(fine_offset_c=np.array(fine_offset), published_records=np.array(published_records),
                      published_score=np.array(published_score), published_sunny_share=np.array(published_share),
                      bilinear_records=np.array(bilinear_records), bilinear_cells=np.array(bilinear_cells), bilinear_weights=np.array(bilinear_weights),
                      published_score_bilinear=np.array(bilinear_score), published_score_pixel=np.array(default_score_bytes[fine]),
                      nearby_coordinates=np.array([(90-r/4, (c/4+180)%360-180) for r, c in local_neighbours]),
                      nearby_land_fraction=np.array([lsm[r,c] for r,c in local_neighbours]),
                      nearby_selected_index=point_neighbour_index, nearby_nearest_index=neighbour_index,
                      native_selected_latitude=90-row/4, native_selected_longitude=shifted_column/4-180,
                      fine=fine, cell=cell_id)
        np.savez_compressed(output / f'{slug}.npz', **arrays)
        provenance[slug] = dict(name=name, requested_latitude=latitude, requested_longitude=longitude, city_timezone=city_zone,
                                production_timezone=str(zones[point_id]), selected_latitude=90-row/4, selected_longitude=shifted_column/4-180,
                                selected_distance_km=111.2*float(np.sqrt(distances[point_id])), fine=fine, cell=cell_id,
                                cell_grid_row_column=cells[cell_id].tolist(), cell_points=count,
                                source=int(point['source']), source_shift_c=float(point['shift'])/100,
                                nearest_raw_latitude=90-nearest_row/4, nearest_raw_longitude=(nearest_column/4+180)%360-180)
    for filename, values in [('monthly.csv', rows), ('neighbours-monthly.csv', neighbour_rows),('radiation-solar-audit.csv',radiation_rows)]:
        with (output / filename).open('w') as handle:
            writer = csv.DictWriter(handle, fieldnames=values[0].keys())
            writer.writeheader()
            writer.writerows(values)
    provenance = dict(written_by='AI, GPT-6.1 Sol, 2026.10.17', chunks=args.chunks,
                      baseline_sha256={name:sha256(output/name) for name in ['points-baseline.bin','cells-baseline.bin','facts-baseline.bin','meta-baseline.json']},
                      radiation_source='fdir at the native fine point rr/cc; temperature source sr/sc and shift do not affect fdir',
                      averaging='Each daily percentage averaged within month unless explicitly pooled. Published facts pool sunny/daylight counts.',
                      neighbourhood_timezone='Production selected point IANA timezone applied throughout the +/-0.5 degree neighbourhood.',
                      places=provenance)
    (output / 'provenance.json').write_text(json.dumps(provenance, indent=2, ensure_ascii=False)+'\n')


if __name__ == '__main__':
    main()
