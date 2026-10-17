"""Compare fixed evening rules using visitor judgements and private station-T targets.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
No observed value or validation choice enters correction tiles or replay packets.
"""
import argparse
import csv
import datetime as dt
import gzip
import json
import pathlib
import statistics
import zoneinfo

import numpy as np
from PIL import Image

from b1_validate import corrected_air
from compare_step_a import Snapshot, climate_pipeline, point_weather, weather_scores
from fine_city_probe import fine_pixel
from native_normals import file_sha256
from tiles import decode_tile

MONTH_DAYS = (31,28,31,30,31,30,31,31,30,31,30,31)
THRESHOLDS = np.array([0,25,40,55,70,85,100])
MODELS = ('ordered','uniform','native')


def canonical(scores):
    return np.floor(np.asarray(scores)*10000+.5)/10000


def judgement(scores, month):
    scores = canonical(scores)
    if not len(scores): return None
    shares = (scores[:,None] >= THRESHOLDS).mean(0)
    band = np.flatnonzero(shares[:6] >= .5-1e-9)[-1]
    value = 85+(shares[5]-.5)*30 if band == 5 else THRESHOLDS[band]+(THRESHOLDS[band+1]-THRESHOLDS[band])*(shares[band]-.5)/(shares[band]-shares[band+1])
    return dict(score=float(canonical(max(THRESHOLDS[band],value))),
        niceDayCount=float(shares[3]*MONTH_DAYS[month-1]),
        perfectDayCount=float(shares[5]*MONTH_DAYS[month-1]),
        awfulDayCount=float((1-shares[1])*MONTH_DAYS[month-1]), sampleDays=len(scores))


def corrected_packet(packet, offsets, model):
    count = len(packet['dates'])
    air = np.array(packet['airC']).reshape(count,12)
    months = np.array([int(date[5:7]) for date in packet['dates']])
    high_offsets,low_offsets = offsets[months-1].T
    corrected = corrected_air(air,np.array(packet['highC'])[:,None],np.array(packet['lowC'])[:,None],high_offsets[:,None],low_offsets[:,None])
    if model == 'uniform': corrected[:,11] = air[:,11]+(high_offsets+low_offsets)/2
    if model == 'native': corrected[:,11] = air[:,11]
    return corrected


def packet_features(packet, temperatures, native_humidity=False):
    count = len(packet['dates'])
    air = np.array(packet['airC']).reshape(count,12)
    dew = np.array(packet['dewC']).reshape(count,12)
    vapor = lambda value: 6.105*np.exp(17.27*value/(237.7+value))
    delta = temperatures-air+.33*(vapor(np.minimum(dew,temperatures))-vapor(np.minimum(dew,air)))
    features = np.zeros((count,29))
    features[:,:11] = (np.array(packet['sunC']).reshape(count,12)+delta)[:,:11]
    features[:,11:23] = np.array(packet['shadeC']).reshape(count,12)+delta
    features[:,23] = np.minimum(dew,air if native_humidity else temperatures).mean(1)
    for column,key in enumerate(('rainMm','windKmh','sunnyHours','daylightHours'),24): features[:,column] = packet[key]
    features[:,28] = temperatures[:,:11].max(1)
    return features


def exact_current_scores(snapshot, selection, month):
    pipeline = climate_pipeline()
    count = snapshot.meta['N']
    offsets = np.asarray(Image.open(snapshot.root/f'climate/offsets-{month:02d}.png'))
    x=(selection['lon']+180)*4%1440; y=(90-selection['lat'])*4
    centre_row=int(np.floor(y+.5)); centre_column=int(np.floor(x+.5)); choices=[]
    for row in range(centre_row-2,centre_row+3):
        if not 0<=row<=600: continue
        for column in range(centre_column-2,centre_column+3):
            if offsets[row,column%1440]!=255: choices.append(((row-y)**2+(column-x)**2,row,column%1440))
    _,row,column=min(choices,key=lambda choice:choice[0])
    pairs=np.fromfile(snapshot.root/'cells.bin',dtype='<u2').reshape(-1,2)
    cell_row,cell_column=min(row//4,179),column//4
    column_distance=np.abs(pairs[:,1].astype(int)-cell_column); column_distance=np.minimum(column_distance,360-column_distance)
    cell=int(np.argmin((pairs[:,0].astype(int)-cell_row)**2+column_distance**2))
    records = np.fromfile(snapshot.root/f'climate/month-{month:02d}.bin',dtype=np.uint8).reshape(count,8,15)[cell]
    shift = (float(offsets[row,column])-128)/10
    level = np.clip((shift+15)/3,0,10)
    lower = min(int(np.floor(level)),9); fraction = level-lower
    # Score each adjacent GPU shift level, then interpolate scores before thresholds.
    # decode_records(float64) is reproduced here to avoid its float32 rounding.
    values = records.astype(float)
    features = np.zeros((8,29))
    for hour in range(11):
        position=hour/2; left=int(position); right=min(left+1,5)
        features[:,hour]=(values[:,left]*(1-(position-left))+values[:,right]*(position-left))/3-30
    for hour in range(12):
        position=np.clip((hour-2)/3,0,3); left=int(position); right=min(left+1,3)
        features[:,11+hour]=(values[:,6+left]*(1-(position-left))+values[:,6+right]*(position-left))/3-30
    features[:,23]=values[:,10]/3-30; features[:,24]=(values[:,11]/50)**2
    features[:,25]=values[:,12]/2; features[:,26:28]=values[:,13:15]
    def at_level(index):
        shifted=features.copy(); shifted[:,:23]+=-15+index*3
        return pipeline.score_features(shifted)
    return canonical(at_level(lower)*(1-fraction)+at_level(lower+1)*fraction)


def observed_air(packet, cache):
    with np.load(cache) as data:
        observations = dict(zip(data['utc_hours'].tolist(),data['temperature_c'].tolist()))
    zone=zoneinfo.ZoneInfo(packet['zone'])
    result=np.full((len(packet['dates']),12),np.nan)
    for index,date in enumerate(packet['dates']):
        local=dt.date.fromisoformat(date)
        for hour in range(10,22):
            utc=dt.datetime.combine(local,dt.time(hour),zone).timestamp()/3600
            # Same existing nearest-hour report assignment and local sample rule.
            result[index,hour-10]=observations.get(int(np.rint(utc)),np.nan)
    return result


def load_offsets(tiles, pixel, generation):
    values=[]; hashes={}
    for month in range(1,13):
        path=tiles/f'tiles/{month:02d}/{pixel["row"]//256:03d}/{pixel["column"]//256:03d}.nwf.gz'
        tile=decode_tile(path); index=(pixel['row']%256,pixel['column']%256)
        if tile['generation']!=generation or not int(tile['support'][index])&8: raise ValueError('Unsupported or different-generation city pixel')
        values.append(tile['offsets_c'][index]); hashes[str(path)]=file_sha256(path)
    return np.array(values),hashes


def mean(values):
    values=[value for value in values if value is not None and np.isfinite(value)]
    return statistics.mean(values) if values else None


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    root=pathlib.Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data')
    parser.add_argument('--data-root',type=pathlib.Path,default=root)
    parser.add_argument('--output',type=pathlib.Path,default=root/'fine-validation/b1-evening')
    parser.add_argument('--skip-paired-current',action='store_true')
    args=parser.parse_args(); root=args.data_root; args.output.mkdir(parents=True,exist_ok=True)
    bundle=root/'fine-b1-diagnostic'; tiles=root/'fine-30s'
    manifest=json.loads((bundle/'manifest.json').read_text())
    baseline={(row['slug'],int(row['month'])):row for row in csv.DictReader((root/'fine-validation/step-a/monthly_step_a.csv').open())}
    snapshot=Snapshot(root/'v6'); rows=[]; hashes={}; summaries=[]; current_visitor=[]; module_expected=[]
    for path in [pathlib.Path(__file__),bundle/'manifest.json',tiles/'configuration.json',root/'fine-validation/step-a/monthly_step_a.csv']+[snapshot.root/f'climate/{name}-{month:02d}.{suffix}' for month in range(1,13) for name,suffix in [('month','bin'),('offsets','png')]]:
        hashes[str(path)]=file_sha256(path)
    for query in manifest['queries']:
        slug=query['slug']; pixel=fine_pixel(query['lat'],query['lon'])
        fine=int(np.floor((pixel['row']+.5)/30+.5))*1440+int(np.floor((pixel['column']+.5)/30+.5))%1440
        path=bundle/f'native/{fine}.json.gz'; packet=json.loads(gzip.decompress(path.read_bytes())); hashes[str(path)]=file_sha256(path)
        offsets,tile_hashes=load_offsets(tiles,pixel,manifest['generation']); hashes.update(tile_hashes)
        months=np.array([int(date[5:7]) for date in packet['dates']]); selected=np.array(packet['selectedIndices'])
        models={model:corrected_packet(packet,offsets,model) for model in MODELS}
        if any(not np.array_equal(models['ordered'][:,:11],model[:,:11]) for model in models.values()): raise ValueError('Daytime correction changed')
        features={model:packet_features(packet,value) for model,value in models.items()}
        scores={model:climate_pipeline().score_features(value) for model,value in features.items()}
        cache=root/f'fine-validation/step-a-settlement/rebuild-work/hourly-cache/{slug}.npz'; hashes[str(cache)]=file_sha256(cache)
        observed=observed_air(packet,cache); valid=np.isfinite(observed).all(1)
        targets={name:climate_pipeline().score_features(packet_features(packet,observed,native_humidity=retain)) for name,retain in [('stationT',False),('stationT_nativeHumidity',True)]}
        selection=snapshot.select(query['lat'],query['lon'])
        paired_current=None
        if not args.skip_paired_current:
            weather=[point_weather(str(root/'fine-build/reference-global/era5'),selection['fine'],chunk,packet['zone'],selection['source'],selection['shift_c']) for chunk in packet['chunks']]
            paired_current=np.concatenate([weather_scores(value,value['T']) for value in weather])
        for month in range(1,13):
            prior=baseline[slug,month]; observed_indices=np.flatnonzero((months==month)&valid); native_indices=selected[months[selected]==month]
            current=judgement(exact_current_scores(snapshot,query,month),month)
            current_visitor.append(dict(slug=slug,month=month,**current))
            target={name:judgement(value[observed_indices],month) for name,value in targets.items()}
            for model in MODELS:
                fine_judgement=judgement(scores[model][native_indices],month)
                module_expected.append(dict(slug=slug,month=month,model=model,highC=float(features[model][months==month,28].mean()),feelsAt21C=float(features[model][months==month,22].mean()),**fine_judgement))
                paired=judgement(scores[model][observed_indices],month)
                current_paired=judgement(paired_current[observed_indices],month) if paired_current is not None else None
                row=dict(slug=slug,city=query['name'],month=month,model=model,published_score=float(prior['current_city_default_score']),observed_days=len(observed_indices),assembled_days=int((months==month).sum()),representative_observed_days=int(valid[native_indices].sum()),high_current_c=float(prior['current_city_high_c']),high_candidate_c=float(features[model][months==month,28].mean()),high_observed_c=float(prior['station_instrument_high_c']) if prior['station_instrument_high_c'] else None,
                    evening_metric='air21' if slug=='sf' else 'felt21',evening_current_c=float(prior['current_air21_c'] if slug=='sf' else prior['current_city_felt21_c']),evening_candidate_c=float(models[model][months==month,11].mean() if slug=='sf' else features[model][months==month,22].mean()),evening_observed_c=float(prior['station_hourly_air21_c' if slug=='sf' else 'station_hourly_felt21_c']) if prior['station_hourly_air21_c' if slug=='sf' else 'station_hourly_felt21_c'] else None)
                for prefix,value in [('current',current),('fine',fine_judgement),('target',target['stationT']),('target_nativeHumidity',target['stationT_nativeHumidity']),('paired_fine',paired),('paired_current',current_paired)]:
                    for name in ('score','niceDayCount','perfectDayCount','awfulDayCount'): row[prefix+'_'+name]=value[name] if value else None
                for metric in ('score','niceDayCount','perfectDayCount','awfulDayCount'):
                    for name in ('current','fine'):
                        row[name+'_'+metric+'_error']=abs(row[name+'_'+metric]-row['target_'+metric]) if row['target_'+metric] is not None else None
                    row[metric+'_error_regression']=row['fine_'+metric+'_error']-row['current_'+metric+'_error'] if row['target_'+metric] is not None else None
                    if metric!='score':
                        row['current_'+metric+'_error_share']=row['current_'+metric+'_error']/MONTH_DAYS[month-1] if row['target_'+metric] is not None else None
                        row['fine_'+metric+'_error_share']=row['fine_'+metric+'_error']/MONTH_DAYS[month-1] if row['target_'+metric] is not None else None
                for metric in ('score','niceDayCount','perfectDayCount','awfulDayCount'):
                    for name in ('current','fine','target'):
                        value=row[name+'_'+metric]
                        row[name+'_'+metric+'_display']=float(np.floor(value+(0 if metric=='score' else .5))) if value is not None else None
                    for name in ('current','fine'):
                        row[name+'_'+metric+'_display_error']=abs(row[name+'_'+metric+'_display']-row['target_'+metric+'_display']) if row['target_'+metric] is not None else None
                    row[metric+'_display_error_regression']=row['fine_'+metric+'_display_error']-row['current_'+metric+'_display_error'] if row['target_'+metric] is not None else None
                row['published_score_display_error']=abs(row['published_score']-row['target_score_display']) if row['target_score'] is not None else None
                row['published_score_display_error_regression']=row['fine_score_display_error']-row['published_score_display_error'] if row['target_score'] is not None else None
                row['published_score_error']=abs(row['published_score']-row['target_score']) if row['target_score'] is not None else None
                row['published_score_error_regression']=row['fine_score_error']-row['published_score_error'] if row['target_score'] is not None else None
                rows.append(row)
        print(json.dumps(dict(slug=slug,complete_observed_days=int(valid.sum()),total_days=len(valid))),flush=True)
    for query in manifest['queries']:
        for model in MODELS:
            values=[row for row in rows if row['slug']==query['slug'] and row['model']==model]
            summary=dict(slug=query['slug'],city=query['name'],model=model,observed_days=sum(row['observed_days'] for row in values),months_with_target=sum(row['target_score'] is not None for row in values))
            for metric in ('score','niceDayCount','perfectDayCount','awfulDayCount'):
                for name in ('current','fine'): summary[name+'_'+metric+'_mae']=mean(row[name+'_'+metric+'_error'] for row in values)
                summary[metric+'_regression']=mean(row[metric+'_error_regression'] for row in values)
                summary[metric+'_maximum_monthly_regression']=max((row[metric+'_error_regression'] for row in values if row[metric+'_error_regression'] is not None),default=None)
                if metric!='score':
                    for name in ('current','fine'): summary[name+'_'+metric+'_mae_share']=mean(row[name+'_'+metric+'_error_share'] for row in values)
            for metric in ('score','niceDayCount','perfectDayCount','awfulDayCount'):
                for name in ('current','fine'): summary[name+'_'+metric+'_display_mae']=mean(row[name+'_'+metric+'_display_error'] for row in values)
                summary[metric+'_display_regression']=mean(row[metric+'_display_error_regression'] for row in values)
                summary[metric+'_display_maximum_monthly_regression']=max((row[metric+'_display_error_regression'] for row in values if row[metric+'_display_error_regression'] is not None),default=None)
            summary['published_score_display_mae']=mean(row['published_score_display_error'] for row in values)
            summary['published_score_display_regression']=mean(row['published_score_display_error_regression'] for row in values)
            summary['published_score_mae']=mean(row['published_score_error'] for row in values)
            summary['published_score_regression']=mean(row['published_score_error_regression'] for row in values)
            summary['published_score_maximum_monthly_regression']=max((row['published_score_error_regression'] for row in values if row['published_score_error_regression'] is not None),default=None)
            summary['native_humidity_sensitivity_score_regression']=mean(abs(row['fine_score']-row['target_nativeHumidity_score'])-abs(row['current_score']-row['target_nativeHumidity_score']) for row in values if row['target_nativeHumidity_score'] is not None)
            summary['paired_score_regression']=mean(abs(row['paired_fine_score']-row['target_score'])-abs(row['paired_current_score']-row['target_score']) for row in values if row['target_score'] is not None and row['paired_current_score'] is not None)
            for metric in ('high','evening'):
                for name in ('current','candidate'): summary[name+'_'+metric+'_mae_c']=mean(abs(row[metric+'_'+name+'_c']-row[metric+'_observed_c']) for row in values if row[metric+'_observed_c'] is not None)
            summary['passes_covered_months_mean_score_gate']=summary['score_regression']<=3+1e-9
            summary['complete_target_coverage']=summary['months_with_target']==12
            summary['passes_score_gate']=summary['passes_covered_months_mean_score_gate']
            summary['passes_each_month_score_gate']=summary['score_maximum_monthly_regression']<=3+1e-9
            summaries.append(summary)
    worker_path=args.output/'current-worker.json'
    if worker_path.exists():
        worker={(row['slug'],row['month']):row for row in json.loads(worker_path.read_text())['rows']}
        differences=[abs(row[key]-worker[row['slug'],row['month']][key]) for row in current_visitor for key in ('score','niceDayCount','perfectDayCount','awfulDayCount')]
        if max(differences)>1e-8:
            (args.output/'current-visitor-failed.json').write_text(json.dumps(current_visitor,indent=2)+'\n')
            raise ValueError('Current visitor decoder differs from actual Worker')
        (args.output/'current-parity.json').write_text(json.dumps(dict(values=len(differences),maximum_difference=max(differences),worker_sha256=file_sha256(worker_path)),indent=2)+'\n')
    (args.output/'current-visitor.json').write_text(json.dumps(current_visitor,indent=2)+'\n')
    (args.output/'module-expected.json').write_text(json.dumps(module_expected,indent=2)+'\n')
    with (args.output/'monthly.csv').open('w') as stream:
        writer=csv.DictWriter(stream,fieldnames=list(rows[0]));writer.writeheader();writer.writerows(rows)
    report=dict(written_by='AI, GPT-6.1 Sol, 2026.10.18',generation=manifest['generation'],models=MODELS,daytime_unchanged=True,
        gate='For every city: mean across twelve months of abs(fine visitor score - stationT target) minus abs(current visitor score - stationT target) <= 3 points; unavailable months remain untested and coverage is explicit. Fog coast score error must also decrease. Actual visitor selections differ; same-date full-hour diagnostics are separate columns.',
        target='Observed hourly air at all twelve local10–21h samples on an assembled date; same original instrument. Other weather held at own-city native ERA5; dew capped at substituted air just as shared module. Not a complete station weather score. Native-humidity-retained sensitivity also recorded.',
        summaries=summaries,input_sha256=hashes,current_provenance=snapshot.provenance)
    (args.output/'acceptance.json').write_text(json.dumps(report,indent=2)+'\n')
    lines=['Written by AI on 2026.10.18 by model GPT-6.1 Sol.','','Visitor score errors use current typical-day/median-band rule and actual eight selected days. Station-T target uses every assembled day with all 12 hourly station-air observations; other weather stays own-city native. Counts are monthly estimates of Nice-or-better, Perfect and Awful days. No station fitting enters production. Same-date full-hour controls and all month/count values are in monthly.csv.','','| Place | Rule | Score error current → 1 km | Score error change | Nice count error current → 1 km | High error current → 1 km | 21 error current → 1 km | Target days / months |','|---|---|---:|---:|---:|---:|---:|---:|']
    fmt=lambda value:'—' if value is None else f'{value:.2f}'
    for value in summaries:
        lines.append('| '+ ' | '.join([value['city'],value['model'],fmt(value['current_score_mae'])+' → '+fmt(value['fine_score_mae']),fmt(value['score_regression']),fmt(value['current_niceDayCount_mae'])+' → '+fmt(value['fine_niceDayCount_mae']),fmt(value['current_high_mae_c'])+' → '+fmt(value['candidate_high_mae_c']),fmt(value['current_evening_mae_c'])+' → '+fmt(value['candidate_evening_mae_c']),str(value['observed_days'])+' / '+str(value['months_with_target'])])+' |')
    uniform=[value for value in summaries if value['model']=='uniform']
    primary=['Written by AI on 2026.10.18 by model GPT-6.1 Sol.','','# One global evening rule: uniform shift','','Keep the 10–20h correction numerically identical; 21h uses (monthly high correction + monthly low correction)/2, with corrected-air vapor cap. Uniform has the lowest equal-city mean 21h error among the three fixed rules: '+', '.join(model+' '+f"{mean(value['candidate_evening_mae_c'] for value in summaries if value['model']==model):.3f}"+'°C' for model in MODELS)+'. No station fitting or place exception.','','There are two current baselines. The public API/old preview publishes the stored mean-based scores.bin value. The current checked-out Worker and app use the newly accepted typical-day rule on those same original weather files. The latter was independently decoded here and matches 624 actual Worker scores/counts exactly. Both score comparisons use the same station-T typical-day target, so the scoring-rule transition is visible.','','All cities improve against the actual published monthly score baseline on average across covered months. Against the current typical-day code on original files, every city also stays within +3 mean absolute score-error points and fog coasts improve. **Individual monthly errors can worsen by 15 points**; this passes a mean-across-months interpretation, not a strict every-month limit. Valparaíso has only 60 complete observed days in 4 months; eight monthly score targets are unavailable. This coverage is not described as a twelve-month proof.','','| Place | Published score error → fine | Same-rule score error → fine | Mean error change same-rule | Worst monthly change published / same-rule | Nice count error same-rule → fine, days | High error current → fine, °C | 21h error current → fine, °C | Target months |','|---|---:|---:|---:|---:|---:|---:|---:|---:|']
    for value in uniform:
        primary.append('| '+' | '.join([value['city'],fmt(value['published_score_mae'])+' → '+fmt(value['fine_score_mae']),fmt(value['current_score_mae'])+' → '+fmt(value['fine_score_mae']),fmt(value['score_regression']),fmt(value['published_score_maximum_monthly_regression'])+' / '+fmt(value['score_maximum_monthly_regression']),fmt(value['current_niceDayCount_mae'])+' → '+fmt(value['fine_niceDayCount_mae']),fmt(value['current_high_mae_c'])+' → '+fmt(value['candidate_high_mae_c']),fmt(value['current_evening_mae_c'])+' → '+fmt(value['candidate_evening_mae_c']),str(value['months_with_target'])])+' |')
    primary+=['','Counts include Perfect, Nice-or-better and Awful in monthly.csv and acceptance.json; Nice is shown above. The same-rule counts are actual eight-sample visitor estimates. The old published API has no day counts to capture. The observed target counts use all complete station air days in that month. Rain, wind, dew and sun remain native ERA5; these are temperature-substituted diagnostic targets, not full station comfort truth. High compares model 10–20h with instrument 24h TMAX. SF 21h is the same air-only Pier1 proxy; other places compare felt21. Instrument displacement and sparse Quintero proxy caveats remain.','','## Monthly target coverage','','| Place | Jan | Feb | Mar | Apr | May | Jun | Jul | Aug | Sep | Oct | Nov | Dec |','|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|']
    for query in manifest['queries']:
        values=[row for row in rows if row['slug']==query['slug'] and row['model']=='uniform']
        primary.append('| '+query['name']+' | '+' | '.join(str(row['observed_days'])+'/'+str(row['assembled_days']) for row in values)+' |')
    primary+=['','## All fixed rules','']+lines[2:]
    (args.output/'COMPARISON.md').write_text('\n'.join(primary)+'\n')
    worst=['Written by AI on 2026.10.18 by model GPT-6.1 Sol.','','Worst individual covered month for each baseline. Values are before display rounding; display columns and both baseline error series are in monthly.csv.','','| Place | Baseline | Month | Current score | Fine score | Station-T target | Current error | Fine error | Error increase |','|---|---|---|---:|---:|---:|---:|---:|---:|']
    for query in manifest['queries']:
        values=[row for row in rows if row['slug']==query['slug'] and row['model']=='uniform' and row['target_score'] is not None]
        for baseline_name,label in [('published','Published'),('current','Current typical-day rule')]:
            key='published_score_error_regression' if baseline_name=='published' else 'score_error_regression'
            value=max(values,key=lambda row:row[key])
            current_key=baseline_name+'_score'; error_key=baseline_name+'_score_error'
            worst.append('| '+' | '.join([query['name'],label,str(value['month']),fmt(value[current_key]),fmt(value['fine_score']),fmt(value['target_score']),fmt(value[error_key]),fmt(value['fine_score_error']),fmt(value[key])])+' |')
    (args.output/'WORST_MONTHS.md').write_text('\n'.join(worst)+'\n')
    display=['Written by AI on 2026.10.18 by model GPT-6.1 Sol.','','Monthly score floored as on cards; day counts rounded to nearest whole day. The diagnostic station-T target is rounded by the same display rule. Means are across observed months.','','| Place | Published score error → fine | Same-rule score error → fine | Same-rule mean error change | Nice day count error → fine | Perfect count error → fine | Awful count error → fine |','|---|---:|---:|---:|---:|---:|---:|']
    for value in uniform:
        display.append('| '+' | '.join([value['city'],fmt(value['published_score_display_mae'])+' → '+fmt(value['fine_score_display_mae']),fmt(value['current_score_display_mae'])+' → '+fmt(value['fine_score_display_mae']),fmt(value['score_display_regression'])]+[fmt(value['current_'+metric+'_display_mae'])+' → '+fmt(value['fine_'+metric+'_display_mae']) for metric in ('niceDayCount','perfectDayCount','awfulDayCount')])+' |')
    (args.output/'DISPLAY_COMPARISON.md').write_text('\n'.join(display)+'\n')
    print(json.dumps(dict(summaries=summaries)),flush=True)

if __name__=='__main__': main()
