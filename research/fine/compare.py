"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Independent monthly-extrema evaluation. All requested stations are excluded from
our fitting: residual candidates fit only the separate training stations.
Source grids may have used the same stations in their original interpolation.
"""
import argparse
import calendar
import csv
import datetime as dt
import hashlib
import json
import math
import pathlib
import statistics
import time

import numpy as np
import pandas as pd

ZONES = dict(malaga='Europe/Madrid', tenerife_south='Atlantic/Canary', las_palmas='Atlantic/Canary',
    funchal='Atlantic/Madeira', lisbon='Europe/Lisbon', cape_town='Africa/Johannesburg',
    sydney='Australia/Sydney', perth='Australia/Perth', honolulu='Pacific/Honolulu',
    nairobi='Africa/Nairobi', lima='America/Lima', rotterdam='Europe/Amsterdam',
    london='Europe/London', marrakech='Africa/Casablanca', sf='America/Los_Angeles',
    san_diego='America/Los_Angeles', los_angeles='America/Los_Angeles', santa_barbara='America/Los_Angeles',
    valparaiso='America/Santiago', agadir='Africa/Casablanca', seville='Europe/Madrid',
    chania='Europe/Athens', las_vegas='America/Los_Angeles', bangkok='Asia/Bangkok')


def station_observations(path):
    result = {}
    for line in path.read_text().splitlines():
        year, month, element = int(line[11:15]), int(line[15:17]), line[17:21]
        if element not in ('TMIN','TMAX') or not 2016 <= year <= 2025:
            continue
        for day in range(1,calendar.monthrange(year,month)[1]+1):
            field = line[21+(day-1)*8:29+(day-1)*8]
            value = int(field[:5])
            if value != -9999 and field[6] == ' ':
                result.setdefault(dt.date(year,month,day).isoformat(),{})[element] = value/10
    return result


def era5_daily(path, zone, calculate_normals=True):
    data = np.load(path)
    index = pd.to_datetime(data['times']).tz_localize('UTC').tz_convert(zone)
    series = pd.Series(data['temperature_c'].astype(float), index=index)
    grouped = series.groupby(index.date)
    daily = {}
    for date, values in grouped:
        first = pd.Timestamp(date).tz_localize(zone,nonexistent='shift_forward',ambiguous=True)
        last = (pd.Timestamp(date)+pd.Timedelta(days=1)).tz_localize(zone,nonexistent='shift_forward',ambiguous=True)
        expected = int((last-first).total_seconds()/3600)
        if len(values) != expected:
            continue
        daily[date.isoformat()] = dict(TMAX=float(values.max()), TMIN=float(values.min()),
            TMEAN=float(values.mean()),
            high10_20=float(values[(values.index.hour>=10)&(values.index.hour<=20)].max()),
            air21=float(values[values.index.hour==21].mean()))
    if not calculate_normals:
        return daily,{}
    normals = {}
    for name,start,end in [('worldclim14',1960,1990),('chelsa',1981,2010),('prism',1991,2020),('daymet',1991,2020),('worldclim21_benchmark',1970,2000),('modern',2016,2025)]:
        normals[name] = {element: [float(np.mean([values[element] for date,values in daily.items()
            if start<=int(date[:4])<=end and int(date[5:7])==month]))
            for month in range(1,13)] for element in ('TMIN','TMAX','TMEAN')}
    return daily,normals


def finite(value):
    return value is not None and math.isfinite(float(value))


def published_dates(chunks):
    dates = set()
    for chunk in chunks:
        epoch = dt.datetime(1940,1,1,tzinfo=dt.timezone.utc)+dt.timedelta(hours=int(chunk)*8736)
        dates.update((epoch.date()+dt.timedelta(days=day)).isoformat() for day in range(2,362))
    return dates


def method_references(row, method):
    if method.startswith('published_period_'):
        return row.get('published_period_observed_c'),row.get('published_period_raw_era5_c')
    return row['observed_c'],row['raw_era5_c']


def method_errors(rows, method):
    matched = []
    for row in rows:
        observed,raw = method_references(row,method)
        if finite(row.get(method)) and finite(observed):
            matched.append((row,float(row[method])-observed,raw,observed))
    if not matched:
        return None
    return dict(months=len(matched),stations=len({row['station'] for row,error,raw,observed in matched}),
                mae_c=statistics.mean(abs(error) for row,error,raw,observed in matched),
                bias_c=statistics.mean(error for row,error,raw,observed in matched),
                raw_mae_same_months_c=statistics.mean(abs(raw-observed) for row,error,raw,observed in matched if finite(raw)),
                published_mae_same_months_c=statistics.mean(abs(row['published_high_c']-observed)
                    for row,error,raw,observed in matched if finite(row.get('published_high_c')))
                    if matched[0][0]['element']=='TMAX' and any(finite(row.get('published_high_c')) for row,error,raw,observed in matched) else None)


def range_inversion_audit(stations, scratch, grids, published_days, output):
    rows = []
    for station in stations:
        station_id = station.get('station')
        path = scratch/f'era5_{station_id}_native.npz'
        if not station_id or not path.exists():
            continue
        data = np.load(path)
        index = pd.to_datetime(data['times']).tz_localize('UTC').tz_convert(ZONES[station['slug']])
        series = pd.Series(data['temperature_c'].astype(float),index=index)
        daily = series.groupby(index.date).agg(['min','max','count']).iloc[1:-1]
        daily = daily[daily['count']>=23]
        dates = np.array([date.isoformat() for date in daily.index])
        daily.index = pd.to_datetime(daily.index)
        available = np.isin(dates,sorted(published_days))
        modern = (daily.index.year>=2016)&(daily.index.year<=2025)
        for month in range(1,13):
            monthly = daily.index.month==month
            available_month = daily[available&monthly]
            if available_month.empty:
                continue
            available_range = float(available_month['max'].mean()-available_month['min'].mean())
            for name,grid in grids.items():
                values = grid.get(station_id,{})
                for suffix in ('','_5km'):
                    high = values.get('tmax'+suffix,[None]*12)[month-1]
                    low = values.get('tmin'+suffix,[None]*12)[month-1]
                    if not finite(high) or not finite(low):
                        continue
                    correction_range = high-low-available_range
                    ratio = (high-low)/available_range
                    for period,selection in [('2016-2025',modern),('published-available',available)]:
                        selected = daily[selection&monthly]
                        raw_range = selected['max']-selected['min']
                        inverted = raw_range+correction_range<=0
                        ordered_range = (raw_range+correction_range).where(~inverted,raw_range)
                        rows.append(dict(station=station_id,slug=station['slug'],month=month,period=period,
                            method=name+suffix,days=len(selected),available_mean_range_c=available_range,
                            fine_mean_range_c=high-low,additive_range_change_c=correction_range,
                            additive_inverted_days=int((raw_range+correction_range<=0).sum()),
                            additive_minimum_range_c=float((raw_range+correction_range).min()),
                            ordered_repaired_days=int(inverted.sum()),
                            ordered_inverted_days=int((ordered_range<=0).sum()),
                            ordered_minimum_range_c=float(ordered_range.min()),
                            affine_ratio=ratio,affine_inverted_days=int((raw_range*ratio<=0).sum()),
                            affine_minimum_range_c=float((raw_range*ratio).min())))
    if rows:
        with (output/'range_inversion_audit.csv').open('w') as stream:
            writer=csv.DictWriter(stream,fieldnames=list(rows[0]));writer.writeheader();writer.writerows(rows)
    return rows


def apply_source_policy(row):
    for source in ('prism','worldclim14','worldclim14_5km','chelsa'):
        method = source+'_available_ordered_c'
        if finite(row.get(method)):
            row['source_policy_available_ordered_c'] = row[method]
            row['source_policy_source'] = source
            row['source_policy_repaired_days'] = row.get(source+'_available_ordered_repaired_days',0)
            return
    row['source_policy_available_ordered_c'] = row['raw_era5_c']
    row['source_policy_source'] = 'native_era5_coarse'
    row['source_policy_repaired_days'] = 0


def write_source_policy_summary(rows, output):
    result = dict(priority=['prism','worldclim14','worldclim14_5km','chelsa','native_era5_coarse'],
                  selection='Availability only, identical order for every place; no station-specific winner selection',metrics={})
    for element in ('TMAX','TMIN'):
        for cohort,quality in [('strict','accepted'),('five_year','accepted_5_years'),('diagnostic',None)]:
            selected = [row for row in rows if row['element']==element and (quality is None or row[quality]=='True')]
            result['metrics'][element+'/'+cohort] = method_errors(selected,'source_policy_available_ordered_c')
            counts = {}
            for row in selected:
                counts[row['station']] = counts.get(row['station'],0)+1
            complete = [row for row in selected if counts[row['station']]==12]
            result['metrics'][element+'/'+cohort+'_complete'] = method_errors(complete,'source_policy_available_ordered_c')
    (output/'source_policy_summary.json').write_text(json.dumps(result,indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--observations', type=pathlib.Path, default=pathlib.Path('/tmp/nwf-agents/round4/fine/stations'))
    parser.add_argument('--scratch', type=pathlib.Path, default=pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/fine-phase1'))
    parser.add_argument('--output', type=pathlib.Path, default=pathlib.Path('/tmp/nwf-agents/round4/fine'))
    parser.add_argument('--range-audit-only',action='store_true')
    parser.add_argument('--source-policy-only',action='store_true')
    args = parser.parse_args()
    started = time.monotonic()
    if args.source_policy_only:
        rows = list(csv.DictReader((args.output/'monthly_comparison.csv').open()))
        for row in rows:
            for key,value in list(row.items()):
                if key.endswith('_c'):
                    row[key] = float(value) if value else None
            apply_source_policy(row)
        keys = list(dict.fromkeys(key for row in rows for key in row))
        with (args.output/'monthly_comparison.csv').open('w') as stream:
            writer=csv.DictWriter(stream,fieldnames=keys);writer.writeheader();writer.writerows(rows)
        write_source_policy_summary(rows,args.output)
        print('Applied availability-only source policy to',len(rows),'extrema-months',flush=True)
        return
    stations = json.loads((args.observations/'evaluation_stations.json').read_text())
    query_path = args.output/'evaluation_queries.json'
    audit_stations = json.loads(query_path.read_text()) if query_path.exists() else stations
    observed_summary = {(r['station'],int(r['month']),r['element']):r for r in csv.DictReader((args.observations/'station_monthly.csv').open()) if r['period']=='2016-2025' and r['role']=='evaluation'}
    live_document = json.loads((args.output/'live_points.json').read_text())
    live = live_document['stations']
    published_days = published_dates(live_document['live_meta']['chunks'])
    residuals = {}
    residual_path = args.scratch/'private_residuals.csv'
    if residual_path.exists():
        for row in csv.DictReader(residual_path.open()):
            if row['prediction_c']:
                residuals[(row['station'],int(row['month']),row['element'],row['scope'])] = float(row['prediction_c'])
    grids = {}
    for name,filename in [('worldclim14','worldclim14_30s_points.json'),('chelsa','chelsa_points.json'),('prism','prism_points.json')]:
        path = args.scratch/filename
        if path.exists():
            document = json.loads(path.read_text())
            grids[name] = document.get('points',document)
    previous = pathlib.Path('/tmp/nwf-agents/round3/finedata')
    if (previous/'daymet_points.json').exists():
        by_slug = json.loads((previous/'daymet_points.json').read_text())
        grids['daymet'] = {s['station']:by_slug[s['slug']] for s in stations if s['slug'] in by_slug}
    if (previous/'worldclim_points.json').exists():
        by_slug = json.loads((previous/'worldclim_points.json').read_text())
        grids['worldclim21_benchmark'] = {s['station']:{v:by_slug[s['slug']][v]['values'] for v in ('tmax','tmin')}
            for s in stations if s['slug'] in by_slug}
    if args.range_audit_only:
        audit=range_inversion_audit(audit_stations,args.scratch,grids,published_days,args.output)
        print('Audited',len(audit),'station-month/method/period ranges',flush=True)
        return
    rows = []
    unavailable_native = []
    available_normals_coverage = {}
    methods = {'raw_era5_c','published_high_c','product_algorithm_c','product_algorithm_daytime_high_c',
               'published_period_raw_era5_c','published_period_product_daytime_high_c','published_period_live_high_c'}
    for station in stations:
        station_id = station.get('station')
        native_path = args.scratch/f'era5_{station_id}_native.npz'
        if not station_id or not native_path.exists():
            print('Unavailable native ERA5',station['slug'],flush=True)
            unavailable_native.append(station['slug'])
            continue
        daily,normals = era5_daily(native_path,ZONES[station['slug']])
        available_dates = sorted(published_days.intersection(daily))
        available_normals = {element:[statistics.mean(daily[date][element] for date in available_dates
            if int(date[5:7])==month) for month in range(1,13)] for element in ('TMAX','TMIN','TMEAN')}
        available_normals_coverage[station_id] = dict(days=len(available_dates),
            monthly_days=[sum(int(date[5:7])==month for date in available_dates) for month in range(1,13)],
            dates_sha256=hashlib.sha256('\n'.join(available_dates).encode()).hexdigest())
        observations = station_observations(args.observations/'daily'/f'{station_id}.dly')
        selected = live[station_id]
        product_path = args.scratch/f'era5_{station_id}_product.npz'
        product_daily = era5_daily(product_path,ZONES[station['slug']],calculate_normals=False)[0] if product_path.exists() else {}
        for month in range(1,13):
            for element,variable in [('TMAX','tmax'),('TMIN','tmin')]:
                summary = observed_summary[(station_id,month,element)]
                accepted_years = set(summary['years'].split(';'))
                dates = [date for date,values in observations.items() if int(date[5:7])==month
                    and element in values and date in daily and date[:4] in accepted_years]
                # Sparse diagnostics use all observed dates, while retaining the failed coverage flag.
                if not dates:
                    dates = [date for date,values in observations.items() if int(date[5:7])==month
                        and element in values and date in daily]
                if not dates:
                    continue
                observed = statistics.mean(observations[date][element] for date in dates)
                raw = statistics.mean(daily[date][element] for date in dates)
                product_dates = [date for date in dates if date in product_daily]
                live_dates = [date for date,values in observations.items() if date in published_days
                              and int(date[5:7])==month and element in values and date in daily]
                live_product_dates = [date for date in live_dates if date in product_daily]
                month_published_days = [date for date in published_days if int(date[5:7])==month]
                month_product_available = [date for date in month_published_days if date in product_daily]
                row = dict(slug=station['slug'],place=station['place'],station=station_id,month=month,
                    element=element,days=len(dates),coverage=float(summary['coverage']),
                    complete_years=summary['complete_years'],accepted=summary['accepted'],
                    accepted_5_years=summary['accepted_5_years'],observed_c=observed,raw_era5_c=raw,
                    published_high_c=selected['monthly_airhigh_c'][month-1] if element=='TMAX' else None,
                    published_felt21_c=selected['monthly_felt21_c'][month-1],
                    product_algorithm_c=statistics.mean(product_daily[date][element]+selected['shift'] for date in product_dates) if product_dates else None,
                    product_algorithm_daytime_high_c=statistics.mean(product_daily[date]['high10_20']+selected['shift'] for date in product_dates) if product_dates and element=='TMAX' else None,
                    product_algorithm_days=len(product_dates),
                    published_period_days=len(month_published_days),published_period_observed_days=len(live_dates),
                    published_period_coverage=len(live_dates)/len(month_published_days) if month_published_days else None,
                    published_period_product_available_days=len(month_product_available),
                    published_period_product_available_coverage=len(month_product_available)/len(month_published_days) if month_published_days else None,
                    published_period_observed_c=statistics.mean(observations[date][element] for date in live_dates) if live_dates else None,
                    published_period_raw_era5_c=statistics.mean(daily[date][element] for date in live_dates) if live_dates else None,
                    published_period_product_daytime_high_c=statistics.mean(product_daily[date]['high10_20']+selected['shift'] for date in live_product_dates) if live_product_dates and element=='TMAX' else None,
                    published_period_live_high_c=selected['monthly_airhigh_c'][month-1] if element=='TMAX' and live_dates else None)
                for scope,method in [('global-private','private_residual_global_c'),('us-government-origin','us_government_residual_c')]:
                    anchor = residuals.get((station_id,month,element,scope))
                    if finite(anchor):
                        row[method] = raw-normals['modern'][element][month-1]+anchor
                        methods.add(method)
                for name,grid in grids.items():
                    values = grid.get(station_id,{})
                    for suffix in ('','_5km'):
                        key = variable+suffix
                        if key not in values:
                            continue
                        anchor = values[key][month-1]
                        if finite(anchor):
                            row[name+suffix+'_c'] = raw+anchor-normals[name][element][month-1]
                            row[name+suffix+'_mean_anomaly_c'] = (raw-normals['modern'][element][month-1]+anchor
                                +normals['modern']['TMEAN'][month-1]-normals[name]['TMEAN'][month-1])
                            row[name+suffix+'_available_mean_anomaly_c'] = (raw-available_normals[element][month-1]+anchor
                                +available_normals['TMEAN'][month-1]-normals[name]['TMEAN'][month-1])
                            methods.update((name+suffix+'_c',name+suffix+'_mean_anomaly_c',
                                            name+suffix+'_available_mean_anomaly_c'))
                        high = values.get('tmax'+suffix,[None]*12)[month-1]
                        low = values.get('tmin'+suffix,[None]*12)[month-1]
                        if finite(high) and finite(low):
                            available_low = available_normals['TMIN'][month-1]
                            available_high = available_normals['TMAX'][month-1]
                            warming = available_normals['TMEAN'][month-1]-normals[name]['TMEAN'][month-1]
                            range_ratio = (high-low)/(available_high-available_low)
                            row[name+suffix+'_available_affine_c'] = low+warming+range_ratio*(raw-available_low)
                            methods.add(name+suffix+'_available_affine_c')
                            high_offset = high+warming-available_high
                            low_offset = low+warming-available_low
                            uniform_offset = (high_offset+low_offset)/2
                            repaired_dates = {date for date in dates if
                                daily[date]['TMAX']+high_offset<=daily[date]['TMIN']+low_offset}
                            element_offset = high_offset if element=='TMAX' else low_offset
                            row[name+suffix+'_available_ordered_c'] = statistics.mean(
                                daily[date][element]+(uniform_offset if date in repaired_dates else element_offset)
                                for date in dates)
                            row[name+suffix+'_available_ordered_repaired_days'] = len(repaired_dates)
                            methods.add(name+suffix+'_available_ordered_c')
                    high = values.get('tmax',[None]*12)[month-1]
                    low = values.get('tmin',[None]*12)[month-1]
                    if finite(high) and finite(low):
                        uniform = (high-normals[name]['TMAX'][month-1]+low-normals[name]['TMIN'][month-1])/2
                        row[name+'_uniform_c'] = raw+uniform
                        methods.add(name+'_uniform_c')
                apply_source_policy(row)
                methods.add('source_policy_available_ordered_c')
                rows.append(row)
        print('Compared',station['slug'],flush=True)
    keys = list(dict.fromkeys(key for row in rows for key in row))
    with (args.output/'monthly_comparison.csv').open('w') as stream:
        writer = csv.DictWriter(stream,fieldnames=keys);writer.writeheader();writer.writerows(rows)
    summary = {}
    methods = sorted(methods)
    station_summary = []
    for element in ('TMAX','TMIN'):
        for cohort,quality in [('strict','accepted'),('five_year','accepted_5_years'),('diagnostic',None)]:
            candidates = [row for row in rows if row['element']==element and (quality is None or row[quality]=='True')]
            eligible = [method for method in methods if any(finite(row.get(method)) for row in candidates)]
            for method in eligible:
                metrics = method_errors(candidates,method)
                if metrics:
                    summary[f'{element}/{cohort}/{method}'] = metrics
                for station in stations:
                    station_rows = [row for row in candidates if row['station']==station.get('station')]
                    metrics = method_errors(station_rows,method)
                    if metrics:
                        station_summary.append(dict(station=station['station'],slug=station['slug'],place=station['place'],
                                                    element=element,cohort=cohort,method=method,**metrics))
    (args.output/'comparison_summary.json').write_text(json.dumps(summary,indent=2))
    if station_summary:
        with (args.output/'station_method_summary.csv').open('w') as stream:
            writer = csv.DictWriter(stream,fieldnames=list(station_summary[0]));writer.writeheader();writer.writerows(station_summary)
    compact = {}
    for entry in station_summary:
        if entry['cohort']!='strict':
            continue
        compact.setdefault(entry['slug'],{}).setdefault(entry['element'],{})[entry['method']] = {
            key:entry[key] for key in ('months','mae_c','bias_c')}
    (args.output/'station_method_summary.json').write_text(json.dumps(compact,indent=2))
    metadata = dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',seconds=time.monotonic()-started,
        compared_stations=len({row['station'] for row in rows}),unavailable_native=unavailable_native,
        source_grids_present=sorted(grids),
        residuals={
            'private_residual_global_c':'PRIVATE: foreign GHCN-derived fitting is not cleared for publication; fitted anchor minus2016–2025 native ERA5 extrema normal plus paired ERA5 extrema',
            'us_government_residual_c':'Separate US-government-origin source-filtered fit, not blanket clearance of NOAA data; same modern-anchor formula'},
        published_chunks=live_document['live_meta']['chunks'],published_dates=[min(published_days),max(published_days)],
        live_build_time=live_document['live_meta'].get('buildTime'),
        live_points_sha256=hashlib.sha256((args.output/'live_points.json').read_bytes()).hexdigest(),
        available_normals=dict(
            selection='Complete local24h native ERA5 dates in captured published chunks, without the station2016 lower bound; no additional historical hourly period',
            published_dates_sha256=hashlib.sha256('\n'.join(sorted(published_days)).encode()).hexdigest(),
            per_station=available_normals_coverage,
            formula='paired ERA5 extrema minus available-period ERA5 extrema normal plus fine-grid anchor plus available-period ERA5 mean minus source-period ERA5 mean',
            affine_formula='target low plus (fine high minus fine low)/(available high minus available low) times (paired ERA5 extrema minus available low); target low is fine low plus available mean minus source-period mean',
            affine_caveat='Additive and affine available-period corrections have identical full-period monthly high/low targets, but differ on paired observation subsets because affine scales the daily anomalies.',
            ordered_formula='Use available-mean high/low offsets on each day; if corrected daily high <= corrected daily low, use the averaged high/low offset uniformly for all hours of that day. Same rule for every source/location, without a fitted threshold.',
            ordered_repair_counts='Per-method paired-date counts in monthly_comparison.csv; all complete native days in range_inversion_audit.csv, separately for2016–2025 and published dates.',
            historical_mean='This phase1 point experiment uses native hourly means for source-period TMEAN. A production implementation can substitute historical monthly mean ERA5 without acquiring historical global hourly extrema.'),
        published_ranges=[dict(chunk=chunk,first=min(published_dates([chunk])),last=max(published_dates([chunk])),days=360)
                          for chunk in live_document['live_meta']['chunks']],
        published_days=len(published_days),published_date_selection='For each chunk: UTC epoch1940 + chunk×8736h; local calendar date offsets2..361 inclusive, matching pipeline.chunk_dates',
        reference_periods={
            'raw_era5_c':'2016–2025, same accepted station observation dates, complete local24h ERA5 groups',
            'product_algorithm_c':'2016–2025 reconstructed current source+height-shift algorithm, 24h extrema on paired station dates',
            'product_algorithm_daytime_high_c':'2016–2025 reconstructed current algorithm local10:00–20:00 high versus observed24h TMAX; different measurement windows',
            'published_high_c':'Actual captured live card high compared with2016–2025 station mean: periods differ; diagnostic context only',
            'published_period_product_daytime_high_c':'Partial live-period current algorithm high10–20 versus same-date observed24h TMAX; paired subset matches dates but measurement windows differ',
            'published_period_live_high_c':'Actual live card monthly high versus observed24h TMAX on available dates in published period; card includes all period days, observations can be sparse',
            'published_period_raw_era5_c':'Native24h extrema versus same-date observations in actual live period'},
        published_period_caveat='This is not a full published-period reconstruction. Station observations are restricted to2016–2025; product-point histories begin2015-12-30. Any earlier published dates are excluded from the paired subset. Per-month published_period_days, observed_days, product_available_days and coverage expose missing dates. Live card values still describe the complete captured published period.',
        low_caveat='TMIN is a dawn/daily-extreme diagnostic, not a validation of21:00 felt temperature',
        station_summary='CSV includes strict, five_year and diagnostic cohorts; compact JSON includes strict only; per-method months report actual matched coverage')
    (args.output/'comparison_metadata.json').write_text(json.dumps(metadata,indent=2))
    write_source_policy_summary(rows,args.output)
    range_inversion_audit(audit_stations,args.scratch,grids,published_days,args.output)
    print('Compared',len({row['station'] for row in rows}),'stations;',len(rows),'extrema-months;',round(metadata['seconds'],2),'seconds',flush=True)


if __name__=='__main__':
    main()
