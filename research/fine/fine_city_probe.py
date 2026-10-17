"""Probe real 30 arc-second city/instrument pixels before global tiles finish.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
Station observations are private evaluation only. No fitting or downloads.
"""
import argparse
import csv
import functools
import json
import pathlib
import time

import numpy as np

from anchors import AnchorSources, SOURCE_NAMES, load_era5_mean_normals
from compare_step_a import (DATA_ROOT, PHASE1, ZONES,
    distance_km, instrument_daily_observations, mean_available, point_weather,
    raw_field, read_stations, sha256, station_daily_hourly)
from native_normals import local_hour_indices
from tiles import PERIOD_NAMES, POINT_DTYPE, decode_tile


def fine_pixel(latitude, longitude):
    row = int(np.floor((90-latitude)*120))
    column = int(np.floor((longitude+180)*120)) % 43200
    return dict(row=row, column=column, latitude=90-(row+.5)/120,
                longitude=-180+(column+.5)/120)


def nearest_supported_pixel(latitude,longitude,land):
    original = fine_pixel(latitude,longitude)
    candidates = []
    for row in range(original['row']-7,original['row']+8):
        for column in range(original['column']-7,original['column']+8):
            if not land[row,column%43200]: continue
            lat,lon = 90-(row+.5)/120,-180+(column%43200+.5)/120
            distance = distance_km(latitude,longitude,lat,lon)
            if distance <= 5:
                candidates.append((distance,row,column%43200,lat,lon))
    if not candidates: raise ValueError('No prepared supported pixel within 5km of instrument')
    _,row,column,latitude,longitude = min(candidates)
    return dict(row=row,column=column,latitude=latitude,longitude=longitude)


class NativeReferences:
    def __init__(self, raw_root, reference_root, chunks, output):
        self.raw_root, self.reference_root = raw_root, reference_root
        self.chunks, self.output = chunks, output
        points = np.fromfile(reference_root/'v6/points.bin', dtype=POINT_DTYPE, offset=4)
        order = np.argsort(points['fine'])
        self.fine = points['fine'][order]
        self.zones = np.load(reference_root/'climate-v6-work/timezones.npy', mmap_mode='r')[order]
        self.inputs = set()
        output.mkdir(parents=True, exist_ok=True)

    def zone(self, fine):
        index = np.searchsorted(self.fine, fine)
        if index == len(self.fine) or self.fine[index] != fine:
            raise ValueError(f'Needed native corner {fine} is absent from prepared reference')
        return str(self.zones[index])

    @functools.lru_cache(maxsize=128)
    def point(self, fine):
        zone = self.zone(fine)
        dates, high, low, mean = [], [], [], []
        for chunk in self.chunks:
            temperature, path = raw_field(self.raw_root, fine, chunk, 't2m')
            self.inputs.add(path)
            chunk_dates, indices, fraction = local_hour_indices(chunk, zone, range(24))
            air = temperature[indices]*(1-fraction)+temperature[indices+1]*fraction
            dates.extend(str(date) for date in chunk_dates)
            high.extend(air.max(1)); low.extend(air.min(1)); mean.extend(air.mean(1))
        arrays = dict(dates=np.array(dates), high=np.array(high), low=np.array(low), mean=np.array(mean))
        months = np.array([int(date[5:7]) for date in dates])
        normals = np.array([[arrays[name][months == month].mean() for name in ('high','low','mean')]
                            for month in range(1,13)],dtype=np.float32)
        arrays.update(normals=normals, zone=np.array(zone), fine=np.array(fine), chunks=np.array(self.chunks))
        np.savez_compressed(self.output/f'{fine}.npz', **arrays)
        return arrays

    def anomaly(self, latitude, longitude, historical):
        row, column = (90-latitude)*4, (longitude+180)*4
        r0, c0 = int(np.floor(row)), int(np.floor(column))
        row_fraction, column_fraction = row-r0, column-c0
        result = np.zeros(12)
        for r, rw in ((r0,1-row_fraction),(min(720,r0+1),row_fraction)):
            for c, cw in ((c0%1440,1-column_fraction),((c0+1)%1440,column_fraction)):
                if rw*cw > 0:
                    result += rw*cw*(self.point(r*1440+c)['normals'][:,2]-historical[:,r,c])
        return result


def hourly_cache(output, slug, zone):
    with np.load(output/'hourly-cache'/f'{slug}.npz') as data:
        records = {}
        for hour, temperature, dew, wind in zip(data['utc_hours'],data['temperature_c'],data['dew_point_c'],data['wind_ms']):
            values = dict(temperature=(float(temperature),))
            if np.isfinite(dew): values['dew_point_temperature'] = (float(dew),)
            if np.isfinite(wind): values['wind_speed'] = (float(wind),)
            records[int(hour)] = values
    return station_daily_hourly(records, zone)


def predict_point(point, samples, index, native, historical, raw_root, chunks):
    latitude, longitude = point['latitude'], point['longitude']
    native_row = int(np.floor((90-latitude)*4+.5))
    native_column = int(np.floor((longitude+180)*4+.5)) % 1440
    fine = native_row*1440+native_column
    reference = native.point(fine)
    high_offset, low_offset, anomaly = np.zeros(12), np.zeros(12), np.zeros(12)
    source = samples.source[:,index]
    for code, name in PERIOD_NAMES.items():
        chosen = source == code
        if chosen.any():
            adjustment = native.anomaly(latitude,longitude,historical[name])
            anomaly[chosen] = adjustment[chosen]
            high_offset[chosen] = samples.tmax_c[:,index][chosen]+adjustment[chosen]-reference['normals'][chosen,0]
            low_offset[chosen] = samples.tmin_c[:,index][chosen]+adjustment[chosen]-reference['normals'][chosen,1]
    offsets = np.rint(np.stack((high_offset,low_offset),axis=1)*10)/10
    daily, raw_files = {}, set()
    cursor = 0
    for chunk in chunks:
        weather = point_weather(str(raw_root),fine,chunk,native.zone(fine))
        raw_files.update(weather['raw_files'])
        length = len(weather['dates'])
        high = reference['high'][cursor:cursor+length]
        low = reference['low'][cursor:cursor+length]
        months = np.array([int(date[5:7])-1 for date in weather['dates']])
        hi, lo = offsets[months,0], offsets[months,1]
        uniform = weather['T']+(hi+lo)[:,None]/2
        width = high-low
        weight = np.divide(weather['T']-low[:,None],width[:,None],
                           out=np.full_like(weather['T'],.5),where=width[:,None] != 0)
        corrected = weather['T']+lo[:,None]+weight*(hi-lo)[:,None]
        repaired = high+hi <= low+lo
        corrected[repaired] = uniform[repaired]
        def felt(temperature):
            capped = np.minimum(weather['Td'],temperature)
            vapor = 6.105*np.exp(17.27*capped/(237.7+capped))
            return temperature+.33*vapor-.70*weather['W']/3.6-4
        uniform_felt, dual_felt = felt(uniform), felt(corrected)
        for day,date in enumerate(weather['dates']):
            daily[date] = dict(fine_uniform_high_c=float(uniform[day,:11].max()),
                fine_uniform_air21_c=float(uniform[day,11]),fine_uniform_felt21_c=float(uniform_felt[day,11]),
                fine_dual_high_c=float(corrected[day,:11].max()),fine_dual_air21_c=float(corrected[day,11]),
                fine_dual_felt21_c=float(dual_felt[day,11]),
                native_dew21_c=float(weather['Td'][day,11]),native_wind21_ms=float(weather['W'][day,11]/3.6),
                repaired=bool(repaired[day]))
        cursor += length
    return dict(fine=fine,latitude=90-native_row*.25,longitude=-180+native_column*.25,
        zone=native.zone(fine),anomaly=anomaly,offsets=offsets,daily=daily,raw_files=raw_files)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source-cache',type=pathlib.Path,default=DATA_ROOT/'fine-sources')
    parser.add_argument('--raw-root',type=pathlib.Path,default=DATA_ROOT/'era5')
    parser.add_argument('--reference-root',type=pathlib.Path,default=DATA_ROOT/'fine-build/reference')
    parser.add_argument('--land-support',type=pathlib.Path,default=DATA_ROOT/'fine-build/land-support',help='Completed prepared mask; provisional until actual B tiles are checked')
    parser.add_argument('--step-a-root',type=pathlib.Path,default=DATA_ROOT/'v6-fine-step-a')
    parser.add_argument('--step-a-validation',type=pathlib.Path,default=DATA_ROOT/'fine-validation/step-a')
    parser.add_argument('--observations',type=pathlib.Path,default=PHASE1/'stations')
    parser.add_argument('--hourly-cache-root',type=pathlib.Path,default=PHASE1/'step-a-comparison')
    parser.add_argument('--output',type=pathlib.Path,default=DATA_ROOT/'fine-validation/step-b')
    parser.add_argument('--only',nargs='*',help='Restrict city slugs; default all thirteen')
    parser.add_argument('--tiles',type=pathlib.Path,help='Optional completed B tiles: verify every sampled offset and source')
    args = parser.parse_args(); args.output.mkdir(parents=True,exist_ok=True)
    started = time.monotonic()
    metadata = json.loads((args.step_a_validation/'comparison_step_a_metadata.json').read_text())
    stations = read_stations(args.observations)
    queries = [q for q in metadata['queries'] if not args.only or q['slug'] in args.only]
    chunks = metadata['step_a']['meta']['chunks']
    step_a = {(row['slug'],int(row['month'])):row for row in csv.DictReader((args.step_a_validation/'monthly_step_a.csv').open())}
    native = NativeReferences(args.raw_root,args.reference_root,chunks,args.output/'native-point-cache')
    sampler, historical = AnchorSources(args.source_cache), load_era5_mean_normals(args.source_cache)
    land = np.memmap(args.land_support/'land.u8',mode='r',dtype=np.uint8,shape=(21600,43200))
    points = []
    for query in queries:
        slug = query['slug']; station = stations[slug]
        for kind,latitude,longitude in [('city',query['lat'],query['lon']),('instrument',station['lat'],station['lon'])]:
            pixel = fine_pixel(latitude,longitude)
            points.append(dict(slug=slug,kind=kind,query_latitude=latitude,query_longitude=longitude,**pixel))
            if kind == 'instrument' and not land[pixel['row'],pixel['column']]:
                proxy = nearest_supported_pixel(latitude,longitude,land)
                points.append(dict(slug=slug,kind='instrument_proxy',query_latitude=latitude,query_longitude=longitude,**proxy))
        for kind in ('city','instrument'):
            selection = metadata['selections'][slug]['step_a_'+kind]
            points.append(dict(slug=slug,kind=kind+'_quarter',latitude=selection['lat'],longitude=selection['lon']))
    samples = sampler.sample(np.array([p['latitude'] for p in points]),np.array([p['longitude'] for p in points]))
    source_rows, rows, raw_files = [], [], set()
    for index,point in enumerate(points):
        slug,kind = point['slug'],point['kind']
        position = fine_pixel(point['latitude'],point['longitude'])
        prepared_support = int(land[position['row'],position['column']])
        for month in range(1,13):
            source_rows.append(dict(slug=slug,kind=kind,month=month,latitude=point['latitude'],longitude=point['longitude'],
                prepared_mask_support=prepared_support,
                source=SOURCE_NAMES[int(samples.source[month-1,index])],high_anchor_c=float(samples.tmax_c[month-1,index]),
                low_anchor_c=float(samples.tmin_c[month-1,index])))
        if kind.endswith('_quarter'): continue
        prediction = predict_point(point,samples,index,native,historical,args.raw_root,chunks)
        raw_files.update(prediction['raw_files'])
        station = stations[slug]
        observed_high = instrument_daily_observations(args.observations/'daily'/f"{station['station']}.dly")
        observed_hourly = hourly_cache(args.hourly_cache_root,slug,ZONES[slug])
        for month in range(1,13):
            dates = [date for date in prediction['daily'] if int(date[5:7]) == month]
            base = step_a[slug,month]
            baseline_kind = kind.split('_')[0]
            row = dict(slug=slug,city=base['city'],kind=kind,month=month,query_latitude=point['query_latitude'],
                query_longitude=point['query_longitude'],pixel_latitude=point['latitude'],pixel_longitude=point['longitude'],
                pixel_row=point['row'],pixel_column=point['column'],native_fine=prediction['fine'],
                prepared_mask_support=prepared_support,
                pixel_from_query_km=distance_km(point['query_latitude'],point['query_longitude'],point['latitude'],point['longitude']),
                native_latitude=prediction['latitude'],native_longitude=prediction['longitude'],native_timezone=prediction['zone'],
                quarter_displacement_km=distance_km(point['query_latitude'],point['query_longitude'],prediction['latitude'],prediction['longitude']),
                source=SOURCE_NAMES[int(samples.source[month-1,index])],high_anchor_c=float(samples.tmax_c[month-1,index]),
                low_anchor_c=float(samples.tmin_c[month-1,index]),mean_anomaly_c=float(prediction['anomaly'][month-1]),
                high_offset_c=float(prediction['offsets'][month-1,0]),low_offset_c=float(prediction['offsets'][month-1,1]),
                repaired_days=sum(prediction['daily'][date]['repaired'] for date in dates),period_days=len(dates),
                current_high_c=float(base['current_'+baseline_kind+'_high_c']),step_a_high_c=float(base['step_a_'+baseline_kind+'_high_c']),
                current_felt21_c=float(base['current_'+baseline_kind+'_felt21_c']),step_a_felt21_c=float(base['step_a_'+baseline_kind+'_felt21_c']))
            for variable in ('high','air21','felt21'):
                truth = {date:observed_high[date]['TMAX'] for date in dates if date in observed_high and 'TMAX' in observed_high[date]} if variable == 'high' else {
                    date:observed_hourly[date][variable] for date in dates if date in observed_hourly and observed_hourly[date][variable] is not None}
                row['observed_'+variable+'_c'] = mean_available(truth.values())
                row['observed_'+variable+'_days'] = len(truth)
                for model in ('fine_uniform','fine_dual'):
                    key = model+'_'+variable+'_c'
                    row[key] = mean_available(prediction['daily'][date][key] for date in dates)
                    row[key+'_observed_dates'] = mean_available(prediction['daily'][date][key] for date in truth)
            paired = [date for date in dates if date in observed_hourly and observed_hourly[date]['felt21'] is not None]
            row['paired_evening_days'] = len(paired)
            for name in ('native_dew21_c','native_wind21_ms','fine_dual_air21_c','fine_dual_felt21_c'):
                row['paired_'+name] = mean_available(prediction['daily'][date][name] for date in paired)
            for name,variable in [('observed_air21_c','temperature'),('observed_dew21_c','dew_point_temperature'),('observed_wind21_ms','wind_speed')]:
                row['paired_'+name] = mean_available(observed_hourly[date]['hours'][21][variable][0] for date in paired)
            row['paired_observed_felt21_c'] = mean_available(observed_hourly[date]['felt21'] for date in paired)
            if paired:
                row['paired_temperature_bias_c'] = row['paired_fine_dual_air21_c']-row['paired_observed_air21_c']
                row['paired_wind_felt_bias_c'] = -.70*(row['paired_native_wind21_ms']-row['paired_observed_wind21_ms'])
                row['paired_felt_bias_c'] = row['paired_fine_dual_felt21_c']-row['paired_observed_felt21_c']
                row['paired_vapor_felt_bias_c'] = row['paired_felt_bias_c']-row['paired_temperature_bias_c']-row['paired_wind_felt_bias_c']
            else:
                for name in ('temperature','wind_felt','felt','vapor_felt'): row['paired_'+name+'_bias_c'] = None
            rows.append(row)
        print('Fine city probe',slug,kind,flush=True)
    for filename,values in [('fine_city_probe.csv',rows),('fine_source_displacement.csv',source_rows)]:
        with (args.output/filename).open('w') as stream:
            writer = csv.DictWriter(stream,fieldnames=list(values[0]));writer.writeheader();writer.writerows(values)
    tile_verification = verify_tiles(rows,args.tiles) if args.tiles else None
    weather_paths = sorted({str(path) for path in raw_files|native.inputs})
    observation_paths = [args.observations/'daily'/f"{stations[q['slug']]['station']}.dly" for q in queries]
    observation_paths += [args.hourly_cache_root/'hourly-cache'/f"{q['slug']}.npz" for q in queries]
    provenance = dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',chunks=chunks,seconds=time.monotonic()-started,
        definition='Pre-tile 30arc-second containing-pixel centre; exact source policy; nearest native quarter-degree carrier; bilinear native mean anomaly; offsets rounded to0.1C; ordered dual model uses uniform averaged offsets on nonpositive corrected daily range. Not a delivered tile/card or station fit.',
        high_definition='Product/replay high10–20; observed instrument high24h TMAX; observed dates paired in separate columns.',
        period_dates_sha256=metadata['model_period_dates_sha256'],step_a_validation_sha256=sha256(args.step_a_validation/'comparison_step_a_metadata.json'),
        reference_points_sha256=sha256(args.reference_root/'v6/points.bin'),reference_timezones_sha256=sha256(args.reference_root/'climate-v6-work/timezones.npy'),
        prepared_land_support=dict(path=str(args.land_support),manifest_sha256=sha256(args.land_support/'complete.json'),
            land_sha256=json.loads((args.land_support/'complete.json').read_text())['sha256'],status='Provisional prepared mask; actual delivered B tile support may differ'),
        script_sha256=sha256(pathlib.Path(__file__)),source_metadata_sha256={name:sha256(args.source_cache/name/'dataset.json') for name in ('prism','worldclim14','chelsa','era5_monthly')},
        delivered_tile_offset_verification=tile_verification,
        native_monthly_precision='float32, matching the global native-reference normals; exact same scored dates and prepared timezone per corner',
        observation_inputs_sha256={str(path):sha256(path) for path in observation_paths},
        raw_weather_files_sha256={name:sha256(pathlib.Path(name)) for name in weather_paths},
        outputs_sha256={name:sha256(args.output/name) for name in ('fine_city_probe.csv','fine_source_displacement.csv')})
    (args.output/'fine_city_probe_metadata.json').write_text(json.dumps(provenance,indent=2)+'\n')
    write_report(rows,source_rows,args.output)
    print(json.dumps(dict(rows=len(rows),points=len(points),seconds=provenance['seconds'])),flush=True)


def write_report(rows,source_rows,output):
    def fmt(value): return '—' if value is None else f'{float(value):.1f}'
    lines=['Written by AI on 2026.10.17 by model GPT-6.1 Sol.','','# Fine city probe','',
        'This is a model replay at actual 30 arc-second source pixels, not a delivered product card. No station fitting. Separate city and instrument rows preserve airport/proxy displacement. Full monthly results are in fine_city_probe.csv; exact-source comparisons are in fine_source_displacement.csv.','',
        'July high is the mean daily 10–20h maximum; observed high is instrument 24h TMAX. Felt21 uses native dew/wind with the corrected-air vapor cap. Dual offsets are rounded to 0.1°C and use a uniform averaged shift whenever the corrected daily 24h range is nonpositive. Observed means use available dates; paired-date predictions are separate CSV columns. Source anchors retain their own declared historical periods; model replay adds the common ERA5 mean anomaly.','',
        '| Place | Query | July high current / A / fine uniform / fine dual / instrument | July felt21 current / A / fine uniform / fine dual / instrument | Repaired days |',
        '|---|---|---:|---:|---:|']
    for row in rows:
        if row['month'] != 7: continue
        label = row['kind']+(' (unsupported in prepared mask)' if not row['prepared_mask_support'] else '')
        lines.append('| '+' | '.join([row['city'],label,
            ' / '.join(fmt(row[k]) for k in ('current_high_c','step_a_high_c','fine_uniform_high_c','fine_dual_high_c','observed_high_c')),
            ' / '.join(fmt(row[k]) for k in ('current_felt21_c','step_a_felt21_c','fine_uniform_felt21_c','fine_dual_felt21_c','observed_felt21_c')),
            str(row['repaired_days'])])+' |')
    lines+=['','## Source displacement at the three problem places','',
        '| Place | Query | Latitude, longitude | July source | High / low anchor °C |',
        '|---|---|---|---|---:|']
    for row in source_rows:
        if row['month']==7 and row['slug'] in ('sf','funchal','tenerife_south'):
            lines.append('| '+' | '.join([row['slug'],row['kind'],f"{row['latitude']:.5f}, {row['longitude']:.5f}",
                row['source'],fmt(row['high_anchor_c'])+' / '+fmt(row['low_anchor_c'])])+' |')
    funchal = next((r for r in rows if r['slug']=='funchal' and r['kind']=='instrument' and r['month']==7),None)
    if funchal:
        lines+=['','## Funchal evening components','',
            f"On the same {funchal['paired_evening_days']} July dates with measured air, dew and wind: fine dual air21 {fmt(funchal['paired_fine_dual_air21_c'])}°C versus observed {fmt(funchal['paired_observed_air21_c'])}°C; native dew21 {fmt(funchal['paired_native_dew21_c'])}°C versus observed {fmt(funchal['paired_observed_dew21_c'])}°C; native wind21 {fmt(funchal['paired_native_wind21_ms'])}m/s versus observed {fmt(funchal['paired_observed_wind21_ms'])}m/s.",'',
            f"The felt21 bias is {fmt(funchal['paired_felt_bias_c'])}°C: air-temperature contribution {fmt(funchal['paired_temperature_bias_c'])}°C, vapor-pressure contribution {fmt(funchal['paired_vapor_felt_bias_c'])}°C, and wind contribution {fmt(funchal['paired_wind_felt_bias_c'])}°C. Contributions use the actual felt equation on the same dates; this diagnoses the retained weather fields, without fitting them."]
    proxies = [r for r in rows if r['kind']=='instrument_proxy' and r['month']==7]
    for row in proxies:
        lines+=['',f"{row['city']}: the declared instrument coordinate is unsupported by the completed initial land mask. The instrument_proxy row is the nearest supported centre in that mask, {row['pixel_from_query_km']:.2f}km away at {row['pixel_latitude']:.5f},{row['pixel_longitude']:.5f}. This is a separate diagnostic selection, not an invented production lookup rule. The upcoming OSM-supported mask may change membership; only the optional actual-tile check establishes final B support."]
    (output/'FINE_CITY_PROBE.md').write_text('\n'.join(lines)+'\n')


def verify_tiles(rows,root):
    @functools.lru_cache(maxsize=384)
    def tile(month,row,column):
        return decode_tile(root/f'tiles/{month:02d}/{row:03d}/{column:03d}.nwf.gz')
    errors=[]; missing=[]
    for row in rows:
        r,c = row['pixel_row'],row['pixel_column']
        actual = tile(row['month'],r//256,c//256)
        flags = int(actual['support'][r%256,c%256])
        if not flags and not row['prepared_mask_support']:
            missing.append(dict(slug=row['slug'],kind=row['kind'],month=row['month']))
            continue
        if not flags&8 or SOURCE_NAMES[flags&7] != row['source']:
            raise ValueError(f"Delivered source/support differs for {row['slug']}/{row['kind']}/{row['month']}")
        expected = np.array([row['high_offset_c'],row['low_offset_c']])
        error = float(np.max(np.abs(actual['offsets_c'][r%256,c%256]-expected)))
        if error > .01001:
            raise ValueError(f"Delivered offset differs for {row['slug']}/{row['kind']}/{row['month']}: {error}")
        errors.append(error)
    return dict(points_months=len(errors),maximum_difference_c=max(errors),unsupported_point_months=missing,
        manifest_sha256=sha256(root/'manifest.json'))


if __name__ == '__main__': main()
