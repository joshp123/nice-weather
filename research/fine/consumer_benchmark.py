"""Measure compact native-carrier replay against real hourly data at 13 cities.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
Outputs are private benchmark inputs, not publishable fine-grid source tiles.
"""
import argparse
import concurrent.futures
import datetime as dt
import json
import pathlib
import sys
import time
import zoneinfo

import numpy as np

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1] / 'climate-v6'))
import pipeline
from anchors import AnchorSources, load_era5_mean_normals
from native_normals import local_hour_indices, sample_local

CITIES = [('san_francisco',37.7749,-122.4194),('los_angeles',34.0522,-118.2437),
    ('santa_barbara',34.4208,-119.6982),('san_diego',32.7157,-117.1611),
    ('lima',-12.0464,-77.0428),('valparaiso',-33.0472,-71.6127),
    ('malaga',36.7213,-4.4214),('tenerife_south',28.05,-16.72),
    ('funchal',32.65,-16.91),('cape_town',-33.9258,18.4232),
    ('seville',37.3891,-5.9845),('rotterdam',51.9244,4.4777),('bangkok',13.7563,100.5018)]
CLOCK_HOURS = np.array([10,12,14,15,16,18,20,21])
SOURCE_PERIODS = {1:'prism',2:'worldclim14',3:'worldclim14',4:'chelsa'}
CITY_ZONES = ['America/Los_Angeles']*4+['America/Lima','America/Santiago','Europe/Madrid',
    'Atlantic/Canary','Atlantic/Madeira','Africa/Johannesburg','Europe/Madrid','Europe/Amsterdam','Asia/Bangkok']


def vapor(dew):
    return 6.105*np.exp(17.27*dew/(237.7+dew))


def correct(features, air, dew, high, low, high_offset, low_offset):
    result = features.copy()
    width = high-low
    repair = (width==0)|(width+high_offset-low_offset <= 0)
    weight = np.divide(air-low[:,None],width[:,None],out=np.full_like(air,.5),where=width[:,None]!=0)
    delta = low_offset[:,None]+weight*(high_offset-low_offset)[:,None]
    delta[repair] = ((high_offset+low_offset)/2)[repair,None]
    corrected_air = air+delta
    felt_delta = delta+.33*(vapor(np.minimum(dew,corrected_air))-vapor(np.minimum(dew,air)))
    result[:,:11] += felt_delta[:,:11]
    result[:,11:23] += felt_delta
    result[:,28] = corrected_air[:,:11].max(1)
    result[:,23] = np.minimum(dew,corrected_air).mean(1)
    return result, corrected_air, repair


def compact(features, air, dew, high, low, precision):
    base = pipeline.encode_records(features)
    values = np.concatenate((air[:,CLOCK_HOURS-10],dew[:,CLOCK_HOURS-10],high[:,None],low[:,None]),axis=1)
    if precision == 33:
        encoded = np.clip(np.rint((values+30)*3),0,255).astype(np.uint8)
        decoded = encoded.astype(float)/3-30
        clipping = int(np.sum((values < -30)|(values > 55)))
    else:
        encoded = np.ascontiguousarray(np.rint(values*10),dtype='<i2').view(np.uint8)
        decoded = encoded.copy().view('<i2').astype(float)/10
        clipping = 0
    decoded_features = pipeline.decode_records(base)
    if precision == 60:
        felt = np.rint(features[:,[0,2,4,6,8,10,13,16,19,22]]*10).astype('<i2')
        felt_decoded = felt.astype(float)/10
        for hour in range(11):
            left=hour//2;right=min(left+1,5);fraction=(hour%2)/2
            decoded_features[:,hour]=felt_decoded[:,left]*(1-fraction)+felt_decoded[:,right]*fraction
        for hour in range(12):
            position=np.clip((hour-2)/3,0,3);left=int(position);right=min(left+1,3)
            decoded_features[:,11+hour]=felt_decoded[:,6+left]*(1-(position-left))+felt_decoded[:,6+right]*(position-left)
        base=np.concatenate((np.ascontiguousarray(felt).view(np.uint8),base[:,11:15]),axis=1)
    output = np.concatenate((base,encoded),axis=1)
    interpolate = lambda block: np.stack([np.interp(np.arange(10,22),CLOCK_HOURS,row) for row in block])
    return output,decoded_features,interpolate(decoded[:,:8]),interpolate(decoded[:,8:16]),decoded[:,16],decoded[:,17],clipping


def native_point(root,chunk,row,column,zone):
    native_column = (column+720)%1440
    tile = f'r{row//12:02d}c{native_column//12:03d}.npz'
    with np.load(root/f'era5/chunk{chunk}'/tile) as raw:
        fields = {name:raw[name][:,row%12,native_column%12].astype(np.float32) for name in raw.files}
    dates,indices,fraction = local_hour_indices(chunk,zone,range(24))
    sample = lambda field,ending=False: field[indices+(1 if ending else 0)]*(1-fraction)+field[indices+(2 if ending else 1)]*fraction
    air24 = sample(fields['t2m'])
    air,dew = air24[:,10:22],sample(fields['d2m'])[:,10:22]
    wind = sample(np.hypot(fields['u10'],fields['v10'])*3.6)[:,10:22]
    rain,direct = sample(fields['tp'],True)[:,10:22],sample(fields['fdir'],True)[:,10:22]
    epoch,_ = pipeline.chunk_dates(chunk)
    utc = indices[:,10:22]+fraction[:,10:22]
    latitude,longitude = 90-row*.25,-180+column*.25
    cosine = np.zeros_like(direct)
    for position,weight in [(.1127016654,5/18),(.5,8/18),(.8872983346,5/18)]:
        utc_dates = np.datetime64(epoch.date())+np.floor((utc+position)/24).astype('timedelta64[D]')
        day_numbers = (utc_dates-utc_dates.astype('datetime64[Y]')).astype(int)+1
        cos,_,_ = pipeline.solar(day_numbers,(utc+position)%24,latitude,longitude)
        cosine += weight*np.maximum(cos,0)
    dni = np.divide(direct,cosine,out=np.zeros_like(direct),where=cosine>.001)
    sun,shade,capped_dew = pipeline.taste.felt(air,dew,wind,dni)
    day_numbers = np.array([date.timetuple().tm_yday for date in dates])
    _,equation,declination = pipeline.solar(day_numbers,12,latitude,longitude)
    angle = np.deg2rad(latitude)
    argument = np.cos(np.deg2rad(90.833))/(np.cos(angle)*np.cos(declination))-np.tan(angle)*np.tan(declination)
    sunset = (720-4*longitude-equation+4*np.rad2deg(np.arccos(np.clip(argument,-1,1))))/60
    timezone = zoneinfo.ZoneInfo(zone)
    offsets = np.array([dt.datetime.combine(date,dt.time(12),timezone).utcoffset().total_seconds()/3600 for date in dates])
    daylight = np.clip(np.ceil(sunset+offsets-24*np.rint((offsets-longitude/15)/24))-10,1,11)
    daylight = np.where(argument>=1,1,np.where(argument<=-1,11,daylight))
    mask = np.arange(11)<daylight[:,None]
    features = np.zeros((len(dates),29))
    features[:,:11],features[:,11:23] = sun[:,:11],shade
    features[:,23],features[:,24],features[:,25] = capped_dew.mean(1),rain[:,:11].sum(1),wind.mean(1)
    features[:,26],features[:,27],features[:,28] = ((dni[:,:11]>=300)&mask).sum(1),daylight,air[:,:11].max(1)
    cell_id = min(row//4,179)*360+column//4
    cells = np.fromfile(root/'v6/cells.bin',dtype='<u2').reshape(-1,2)
    ordinal = np.flatnonzero(cells[:,0]*360+cells[:,1] == cell_id)
    with np.load(root/f'climate-v6-work/chunk{chunk}'/tile) as saved:
        position = np.flatnonzero(saved['cells'] == ordinal[0]) if ordinal.size else []
        reference = saved['sums'][position[0]]/saved['counts'][position[0]] if len(position) else None
    return dict(dates=dates,air=air,dew=dew,high=air24.max(1),low=air24.min(1),mean=air24.mean(1),
        features=features,reference=reference)


def audit_native_clipping(root,output,workers):
    """Audit tile-local native cell means; these are not assembled whole-cell means."""
    sampled=np.array([0,2,4,6,8,10,13,16,19,22])
    def inspect(path):
        with np.load(path) as saved:
            felt=saved['sums'][...,:23]/saved['counts'][:,None,None]
            cells=saved['cells']
        low=np.unravel_index(np.argmin(felt),felt.shape);high=np.unravel_index(np.argmax(felt),felt.shape)
        chunk=int(path.parent.name[5:]);_,dates=pipeline.chunk_dates(chunk)
        def extreme(index):
            field=index[2]
            return dict(value_c=float(felt[index]),cell_ordinal=int(cells[index[0]]),date=str(dates[index[1]]),
                field=int(field),local_hour=int(field+10 if field<11 else field-11+10),
                exposure='sun' if field<11 else 'shade',tile=path.name,chunk=chunk)
        selected=felt[...,sampled]
        return dict(values=int(felt.size),below=int((felt< -30).sum()),above=int((felt>55).sum()),
            retained_values=int(selected.size),retained_below=int((selected< -30).sum()),retained_above=int((selected>55).sum()),
            low=extreme(low),high=extreme(high))
    totals=dict(values=0,below=0,above=0,retained_values=0,retained_below=0,retained_above=0)
    paths=sorted((root/'climate-v6-work').glob('chunk*/r*.npz'))
    started=time.monotonic();lowest=highest=None
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        for index,result in enumerate(pool.map(inspect,paths)):
            for key in totals:totals[key]+=result[key]
            if lowest is None or result['low']['value_c']<lowest['value_c']:lowest=result['low']
            if highest is None or result['high']['value_c']>highest['value_c']:highest=result['high']
            if index%1000==0:print('clip audit',index,len(paths),flush=True)
    cells=np.fromfile(root/'v6/cells.bin',dtype='<u2').reshape(-1,2)
    for value in [lowest,highest]:value['cell_latitude_longitude']=[float(89.5-cells[value['cell_ordinal'],0]),float(-179.5+cells[value['cell_ordinal'],1])]
    report=dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',scope='Fresh native tile-local cell daily means before whole-cell assembly. Retained means ten encoded felt fields, over all dates, not only eight selected dates.',
        tiles=len(paths),seconds=time.monotonic()-started,**totals,minimum=lowest,maximum=highest)
    output.mkdir(parents=True,exist_ok=True);(output/'native-clipping.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report),flush=True)


def probe_native_clipping(root,output):
    audit=json.loads((output/'native-clipping.json').read_text())
    points=np.fromfile(root/'v6/points.bin',dtype=pipeline.POINT_DTYPE,offset=4)
    zones=np.load(root/'climate-v6-work/timezones.npy')
    results=[]
    for label in ['minimum','maximum']:
        record=audit[label];best=None
        for index in np.flatnonzero(points['cell']==record['cell_ordinal']):
            row,column=divmod(int(points['fine'][index]),1440)
            weather=native_point(root,record['chunk'],row,column,str(zones[index]))
            day=np.flatnonzero(np.array([str(date) for date in weather['dates']])==record['date'])[0]
            felt=weather['features'][day,record['field']]
            if best is None or (felt<best[0] if label=='minimum' else felt>best[0]):best=(felt,index,row,column,weather,day)
        felt,index,row,column,weather,day=best
        result=dict(label=label,latitude=90-row*.25,longitude=-180+column*.25,zone=str(zones[index]),date=record['date'],
            local_hour=record['local_hour'],exposure=record['exposure'],native_felt_c=float(felt),native_air_c=float(weather['air'][day,record['local_hour']-10]),carriers={})
        for size in [33,51,60]:
            packed=compact(weather['features'],weather['air'],weather['dew'],weather['high'],weather['low'],size)
            result['carriers'][size]=dict(decoded_felt_c=float(packed[1][day,record['field']]),felt_error_c=float(packed[1][day,record['field']]-felt),auxiliary_clipped_values=packed[-1])
        results.append(result)
    (output/'clipping-point-probe.json').write_text(json.dumps(results,indent=2)+'\n')
    print(json.dumps(results),flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--native-root',type=pathlib.Path,required=True)
    parser.add_argument('--source-cache',type=pathlib.Path,required=True)
    parser.add_argument('--output',type=pathlib.Path,required=True)
    parser.add_argument('--chunks',type=int,nargs='+',default=[76,78,80,82,84])
    parser.add_argument('--weather-only',action='store_true')
    parser.add_argument('--audit-native-clipping',action='store_true')
    parser.add_argument('--probe-native-clipping',action='store_true')
    parser.add_argument('--workers',type=int,default=4)
    parser.add_argument('--tiles',type=pathlib.Path,help='Read exact completed production correction tiles instead of the convenience anchor sampler')
    parser.add_argument('--test-offsets',type=float,nargs=2,metavar=('HIGH','LOW'),
        help='Explicit synthetic model offsets for source-independent replay/throughput experiments')
    args = parser.parse_args();started=time.monotonic();args.output.mkdir(parents=True,exist_ok=True)
    if args.probe_native_clipping:
        probe_native_clipping(args.native_root,args.output);return
    if args.audit_native_clipping:
        audit_native_clipping(args.native_root,args.output,args.workers);return
    tile_configuration=None
    if args.tiles:
        tile_configuration=json.loads((args.tiles/'configuration.json').read_text())
        if sorted(tile_configuration['chunks'])!=sorted(args.chunks):raise ValueError('Fine tile generation uses different native chunk dates')
    sources=historical=None
    if not args.weather_only and args.test_offsets is None and args.tiles is None:
        sources=AnchorSources(args.source_cache);historical=load_era5_mean_normals(args.source_cache)
    results=[];render_records={size:[] for size in [33,51,60]};render_offsets=[];cpu_card={};cpu_cached_card={}
    for (slug,latitude,longitude),zone in zip(CITIES,CITY_ZONES):
        row,column=round((90-latitude)*4),round((longitude+180)*4)%1440
        cache=args.output/f'weather-{slug}.npz'
        if cache.exists():
            with np.load(cache) as saved:
                if saved['chunks'].tolist()!=args.chunks:raise ValueError('Weather cache uses different chunks')
                data={key:saved[key] for key in saved.files if key not in ['chunks','reference']}
                data['dates']=np.array(data['dates'].astype('datetime64[D]').tolist(),dtype=object)
                reference=saved['reference']
        else:
            chunks=[native_point(args.native_root,chunk,row,column,zone) for chunk in args.chunks]
            data={key:np.concatenate([value[key] for value in chunks]) for key in chunks[0] if key!='reference'}
            reference=np.concatenate([value['reference'] for value in chunks])
            np.savez_compressed(cache,**{**data,'dates':np.array(data['dates'],dtype='datetime64[D]')},reference=reference,chunks=args.chunks)
        if args.weather_only:
            print('weather cached',slug,flush=True)
            continue
        reference_scores=pipeline.score_features(pipeline.decode_records(pipeline.encode_records(reference)))
        months=np.array([date.month for date in data['dates']])
        anchor=sources.sample(latitude,longitude) if sources else None
        high_offset=np.zeros(12);low_offset=np.zeros(12);tile_generations=[]
        case_sources=anchor.source.tolist() if anchor else ['synthetic_offset_test']*12
        for month in range(12):
            select=months==month+1;code=int(anchor.source[month]) if anchor else 0;period=SOURCE_PERIODS.get(code)
            if period:
                warming=data['mean'][select].mean()-historical[period][month,row,column]
                high_offset[month]=float(anchor.tmax_c[month])+warming-data['high'][select].mean()
                low_offset[month]=float(anchor.tmin_c[month])+warming-data['low'][select].mean()
        if args.tiles is not None:
            from tiles import decode_tile
            fine_row,fine_column=int(np.floor((90-latitude)*120)),int(np.floor((longitude+180)*120))%43200
            expected_row=int(np.floor((fine_row+.5)/30+.5));expected_column=int(np.floor((fine_column+.5)/30+.5))%1440
            if (expected_row,expected_column)!=(row,column):raise ValueError('City weather cache reference differs from exact fine-pixel reference')
            case_sources=[]
            for month in range(1,13):
                tile=decode_tile(args.tiles/f'tiles/{month:02d}/{fine_row//256:03d}/{fine_column//256:03d}.nwf.gz')
                if tile['generation']!=tile_configuration['generation']:raise ValueError('Fine tile header and generation configuration differ')
                support=int(tile['support'][fine_row%256,fine_column%256])
                if not support&8:raise ValueError(f'{slug}: fine pixel has no native reference')
                offsets=tile['offsets_c'][fine_row%256,fine_column%256]
                if np.any(offsets < -3276.7):raise ValueError(f'{slug}: fine pixel has missing offsets')
                high_offset[month-1],low_offset[month-1]=offsets
                case_sources.append(support&7);tile_generations.append(tile['generation'])
        if args.test_offsets is not None:
            high_offset[:],low_offset[:]=args.test_offsets
        corrected,corrected_air,repairs=correct(data['features'],data['air'],data['dew'],data['high'],data['low'],high_offset[months-1],low_offset[months-1])
        true_scores=pipeline.score_features(corrected)
        chosen=[]
        for month in range(1,13):
            available=np.flatnonzero(months==month);order=np.argsort(reference_scores[available],kind='stable')
            chosen.extend(available[order[np.floor((np.arange(8)+.5)*len(order)/8).astype(int)]])
        chosen=np.array(chosen);month_means=lambda x: np.array([x[months==month].mean(0) for month in range(1,13)])
        row_result=dict(slug=slug,latitude=latitude,longitude=longitude,native_latitude=90-row*.25,
            native_longitude=-180+column*.25,zone=zone,source=case_sources,tile_generations=tile_generations,
            high_offset_c=high_offset.tolist(),low_offset_c=low_offset.tolist(),
            ordering_repairs_all_days=int(repairs.sum()),selected_dates=[str(date) for date in data['dates'][chosen]],
            sample_score_bias_by_month=(true_scores[chosen].reshape(12,8).mean(1)-month_means(true_scores)).tolist())
        for size in [33,51,60]:
            records,features,air,dew,high,low,clipping=compact(data['features'],data['air'],data['dew'],data['high'],data['low'],size)
            compact_features,compact_air,compact_repairs=correct(features,air,dew,high,low,high_offset[months-1],low_offset[months-1])
            compact_scores=pipeline.score_features(compact_features)
            error=compact_scores[chosen]-true_scores[chosen]
            row_result[str(size)]=dict(auxiliary_clipped_values=clipping,
                baseline_felt_clipped_values=int(np.sum((data['features'][:,[0,2,4,6,8,10,13,16,19,22]]< -30)|(data['features'][:,[0,2,4,6,8,10,13,16,19,22]]>55))),
                selected_score_mae=float(np.abs(error).mean()),selected_score_max_error=float(np.abs(error).max()),
                monthly_score_mae=float(np.abs(error.reshape(12,8).mean(1)).mean()),
                monthly_score_max_error=float(np.abs(error.reshape(12,8).mean(1)).max()),
                selected_air21_mae_c=float(np.abs(compact_air[chosen,-1]-corrected_air[chosen,-1]).mean()),
                selected_felt21_mae_c=float(np.abs(compact_features[chosen,22]-corrected[chosen,22]).mean()),
                changed_ordering_decisions=int(np.sum(compact_repairs!=repairs)))
            for month_count in [1,12]:
                keep=chosen[:month_count*8]
                variation=2*np.sin((len(results)*30+17)*.017)+2*np.cos(11*.023)
                h=np.floor((high_offset[months[keep]-1]+variation)*10+.5)/10
                l=np.floor((low_offset[months[keep]-1]-variation)*10+.5)/10
                fixture=correct(features[keep],air[keep],dew[keep],high[keep],low[keep],h,l)[0]
                fixture[:,:11] -= .75
                cpu_card[f'{size}/{month_count}/{len(results)}']=float(pipeline.score_features(fixture).mean())
                cached_x=min(255,(len(results)%9)*30+17);cached_y=(len(results)//9)*30+11
                variation=2*np.sin(cached_x*.017)+2*np.cos(cached_y*.023)
                h=np.floor((high_offset[months[keep]-1]+variation)*10+.5)/10
                l=np.floor((low_offset[months[keep]-1]-variation)*10+.5)/10
                fixture=correct(features[keep],air[keep],dew[keep],high[keep],low[keep],h,l)[0]
                fixture[:,:11] -= .75
                cpu_cached_card[f'{size}/{month_count}/{len(results)}']=float(pipeline.score_features(fixture).mean())
            render_records[size].append(records[chosen])
        render_offsets.append(np.stack((high_offset,low_offset),axis=-1));results.append(row_result)
        print('carrier',slug,flush=True)
    if args.weather_only:
        print(json.dumps(dict(weather_only=True,seconds=time.monotonic()-started,cases=len(CITIES))),flush=True)
        return
    for size,records in render_records.items():np.stack(records).astype(np.uint8).tofile(args.output/f'carrier{size}.bin')
    np.stack(render_offsets).astype('<f4').tofile(args.output/'offsets.bin')
    report=dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',seconds=time.monotonic()-started,
        score_aggregation='Arithmetic mean of graded daily scores: eight equally weighted days per month; year is the equally weighted mean of twelve months. Reference research/taste/nice_v6.py through climate-v6/pipeline.py; no threshold-count aggregation.',
        chunks=args.chunks,clock_hours=CLOCK_HOURS.tolist(),cases=results,cpu_card_at_reference_pixel=cpu_card,cpu_cached_card_at_reference_pixel=cpu_cached_card,test_offsets=args.test_offsets,
        render_fixture='13 real city carrier sequences, cycled across synthetic native-reference cells; '+('explicit synthetic test offsets' if args.test_offsets else 'exact production tile offsets' if args.tiles else 'actual city anchor offsets')+'. Shader throughput and compact-replay experiment, not a production geographic tile or network benchmark.',
        layout=dict(cases=len(results),months=12,days=8,carrier_bytes=[33,51,60],offsets='little endian Float32 case/month/high-low'))
    (args.output/'carrier-results.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(dict(seconds=report['seconds'],cases=len(results))),flush=True)


if __name__=='__main__':main()
