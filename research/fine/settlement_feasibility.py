"""Private feasibility bounds for a monthly uniform temperature correction.

Written by AI on 2026.10.17 by model GPT-6.1 Sol.
Station-derived shifts are diagnostic oracles, never publication inputs.
"""
import argparse
import csv
import json
import pathlib
import time

import numpy as np

from compare_step_a import (DATA_ROOT, PHASE1, ZONES, instrument_daily_observations,
    point_weather, read_stations, sha256)
from fine_city_probe import hourly_cache


def number(row, key):
    value = row.get(key)
    return float(value) if value not in (None, '') else None


def felt21(air, dew, wind_ms, shift):
    changed = air + np.asarray(shift)[..., None]
    capped = np.minimum(changed, dew)
    return (changed + .33*6.105*np.exp(17.27*capped/(237.7+capped))-.70*wind_ms-4).mean(-1)


def inverse_felt(air, dew, wind, target):
    lower, upper = -60., 60.
    for _ in range(48):
        middle = (lower+upper)/2
        if felt21(air, dew, wind, middle) < target:
            lower = middle
        else:
            upper = middle
    return (lower+upper)/2


def monthly_bounds(row, air, dew, wind, native_high, margin):
    observed_high = number(row, 'station_instrument_high_c')
    observed_evening = number(row, 'station_hourly_felt21_c')
    current_high = number(row, 'current_instrument_high_c')
    current_evening = number(row, 'current_instrument_felt21_c')
    high_target = observed_high-native_high if observed_high is not None else None
    high_budget = abs(current_high-observed_high)+margin if observed_high is not None else None
    evening_budget = abs(current_evening-observed_evening)+margin if observed_evening is not None else None
    evening_target = inverse_felt(air, dew, wind, observed_evening) if observed_evening is not None else None
    high_lower = high_target-high_budget if high_target is not None else -np.inf
    high_upper = high_target+high_budget if high_target is not None else np.inf
    evening_lower = inverse_felt(air, dew, wind, observed_evening-evening_budget) if observed_evening is not None else -np.inf
    evening_upper = inverse_felt(air, dew, wind, observed_evening+evening_budget) if observed_evening is not None else np.inf
    lower, upper = max(high_lower,evening_lower), min(high_upper,evening_upper)
    output = dict(slug=row['slug'],city=row['city'],month=int(row['month']),
        native_high_c=native_high,native_felt21_c=float(felt21(air,dew,wind,0)),
        observed_high_c=observed_high,observed_felt21_c=observed_evening,
        current_high_c=current_high,current_felt21_c=current_evening,
        current_high_error_c=None if high_budget is None else high_budget-margin,
        current_felt21_error_c=None if evening_budget is None else evening_budget-margin,
        high_perfect_shift_c=high_target,felt21_perfect_shift_c=evening_target,
        high_allowed_shift_lower_c=high_lower,high_allowed_shift_upper_c=high_upper,
        felt21_allowed_shift_lower_c=evening_lower,felt21_allowed_shift_upper_c=evening_upper,
        jointly_feasible=bool(lower<=upper),joint_lower_c=lower,joint_upper_c=upper,
        infeasible_shift_gap_c=max(0,lower-upper),
        high_observation_days=int(row['station_instrument_high_days']),
        felt21_observation_days=int(row['station_hourly_felt21_days']),
        high_coverage=float(row['station_instrument_high_coverage']),
        felt21_coverage=float(row['station_hourly_felt21_coverage']))
    return output, (air,dew,wind)


def annual_oracle(months, weather, margin, grid_step):
    """Bound annual MAE with arbitrary station-chosen monthly shifts (private).

    The weighted objective is separable by month. A supported solution is a
    feasible example; the Lagrangian lower bound can also disprove feasibility.
    Continuous minima lie between the two zero-error shifts because both
    temperature and felt temperature increase monotonically with the shift.
    """
    n_high = sum(row['observed_high_c'] is not None for row in months)
    n_evening = sum(row['observed_felt21_c'] is not None for row in months)
    high_budget = np.mean([r['current_high_error_c'] for r in months if r['current_high_error_c'] is not None])+margin
    evening_budget = np.mean([r['current_felt21_error_c'] for r in months if r['current_felt21_error_c'] is not None])+margin if n_evening else None
    candidates=[]
    for row,fields in zip(months,weather):
        targets=[row[key] for key in ('high_perfect_shift_c','felt21_perfect_shift_c') if row[key] is not None]
        shifts=np.unique(np.r_[np.arange(min(targets),max(targets),grid_step),targets])
        high_error=np.abs(shifts-row['high_perfect_shift_c'])/n_high if row['high_perfect_shift_c'] is not None else np.zeros(len(shifts))
        evening_error=np.abs(felt21(*fields,shifts)-row['observed_felt21_c'])/n_evening if row['observed_felt21_c'] is not None else np.zeros(len(shifts))
        candidates.append((shifts,high_error,evening_error))
    def solve(penalty):
        shifts=[]; high=evening=objective=0.
        for values,h,e in candidates:
            index=int(np.argmin(e+penalty*h));shifts.append(float(values[index]))
            high+=float(h[index]);evening+=float(e[index]);objective+=float((e+penalty*h)[index])
        return dict(shifts=shifts,high_mae_c=high,felt21_mae_c=evening,objective=objective)
    if not n_evening:
        best=solve(1)
        return dict(high_budget_c=high_budget,felt21_budget_c=None,status='evening_unavailable',oracle=best)
    lower,upper=0.,1.
    while solve(upper)['high_mae_c']>high_budget:
        upper*=2
    lower_bound=-np.inf; chosen=None
    for _ in range(55):
        penalty=(lower+upper)/2; result=solve(penalty)
        # Each sampled shift is at most half a grid cell from the continuum.
        # Felt derivative is <=4 over this experiment's air range; this loose
        # bound keeps a sampled minimum from overstating impossibility.
        bound=result['objective']-penalty*high_budget-(4+penalty)*grid_step
        lower_bound=max(lower_bound,bound)
        if result['high_mae_c']<=high_budget:
            upper=penalty;chosen=result
        else:
            lower=penalty
    feasible=chosen['felt21_mae_c']<=evening_budget+1e-8
    status='feasible_oracle' if feasible else ('impossible_uniform' if lower_bound>evening_budget else 'unresolved_within_grid_tolerance')
    return dict(high_budget_c=high_budget,felt21_budget_c=evening_budget,status=status,
        oracle=chosen,minimum_felt21_mae_lower_bound_c=lower_bound,grid_step_c=grid_step,
        warning='Oracle shifts use station truth privately and are not an allowed production method.')


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--validation',type=pathlib.Path,default=DATA_ROOT/'fine-validation/step-a')
    parser.add_argument('--raw-root',type=pathlib.Path,default=DATA_ROOT/'era5')
    parser.add_argument('--hourly-cache-root',type=pathlib.Path,default=PHASE1/'step-a-comparison')
    parser.add_argument('--observations',type=pathlib.Path,default=PHASE1/'stations')
    parser.add_argument('--adjustments',type=pathlib.Path,default=DATA_ROOT/'fine-build/step-a/adjustment.npz')
    parser.add_argument('--output',type=pathlib.Path,default=DATA_ROOT/'fine-validation/step-a-settlement')
    parser.add_argument('--margin',type=float,default=.3)
    parser.add_argument('--grid-step',type=float,default=.002)
    parser.add_argument('--candidate-adjustments',type=pathlib.Path,
        help='Evaluate a separately chosen population rule; must contain fine and uniform_offset_c')
    args=parser.parse_args();args.output.mkdir(parents=True,exist_ok=True);started=time.monotonic()
    if args.candidate_adjustments:
        evaluate_candidate(args)
        return
    metadata=json.loads((args.validation/'comparison_step_a_metadata.json').read_text())
    monthly=list(csv.DictReader((args.validation/'monthly_step_a.csv').open()))
    summary={row['slug']:row for row in json.loads((args.validation/'station_summary.json').read_text())}
    stations=read_stations(args.observations)
    with np.load(args.adjustments) as arrays:
        adjustments={int(fine):shift for fine,shift in zip(arrays['fine'],arrays['uniform_offset_c'])}
    rows=[]; paired_rows=[]; annual=[]; paired_annual=[];raw_files=set();qa=[];observation_files=[]
    for query in metadata['queries']:
        slug=query['slug'];selection=metadata['selections'][slug]['step_a_instrument'];fine=selection['fine']
        days=[];temperature=[];dew=[];wind=[]
        for chunk in metadata['step_a']['meta']['chunks']:
            weather=point_weather(str(args.raw_root),fine,chunk,ZONES[slug]);raw_files.update(weather['raw_files'])
            days.extend(weather['dates']);temperature.extend(weather['T']);dew.extend(weather['Td']);wind.extend(weather['W']/3.6)
        temperature,dew,wind=map(np.array,(temperature,dew,wind));daymonths=np.array([int(date[5:7]) for date in days])
        daily_path=args.observations/'daily'/f"{stations[slug]['station']}.dly"
        observation_files.extend([daily_path,args.hourly_cache_root/'hourly-cache'/f'{slug}.npz'])
        daily=instrument_daily_observations(daily_path)
        hourly=hourly_cache(args.hourly_cache_root,slug,ZONES[slug])
        local_rows=[];local_weather=[];local_paired=[];local_paired_weather=[]
        for row in [r for r in monthly if r['slug']==slug]:
            month=int(row['month']);mask=daymonths==month
            fields=(temperature[mask,11],dew[mask,11],wind[mask,11])
            native_high=float(temperature[mask,:11].max(1).mean())
            bounds,fields=monthly_bounds(row,*fields,native_high,args.margin)
            delta=float(adjustments[fine][month-1]);bounds['step_a_uniform_shift_c']=delta
            bounds['step_a_replay_high_c']=native_high+delta
            bounds['step_a_replay_felt21_c']=float(felt21(*fields,delta))
            bounds['step_a_high_c']=number(row,'step_a_instrument_high_c')
            bounds['step_a_felt21_c']=number(row,'step_a_instrument_felt21_c')
            differences=[abs(bounds['step_a_replay_high_c']-bounds['step_a_high_c']),abs(bounds['step_a_replay_felt21_c']-bounds['step_a_felt21_c'])]
            qa.append(dict(slug=slug,month=month,maximum_encoded_difference_c=max(differences)))
            if max(differences)>.201: raise ValueError(f'Raw replay differs from actual A: {slug}/{month}')
            rows.append(bounds);local_rows.append(bounds);local_weather.append(fields)
            high_mask=np.array([int(date[5:7])==month and 'TMAX' in daily.get(date,{}) for date in days])
            felt_mask=np.array([int(date[5:7])==month and hourly.get(date,{}).get('felt21') is not None for date in days])
            paired_input=dict(row)
            paired_input['current_instrument_high_c']=row['current_replayed_high_c_on_observed_instrument_high_days']
            paired_input['current_instrument_felt21_c']=row['current_replayed_felt21_c_on_observed_hourly_felt21_days']
            paired_fields=(temperature[felt_mask,11],dew[felt_mask,11],wind[felt_mask,11]) if felt_mask.any() else fields
            paired_high=float(temperature[high_mask,:11].max(1).mean()) if high_mask.any() else native_high
            paired_bounds,paired_fields=monthly_bounds(paired_input,*paired_fields,paired_high,args.margin)
            paired_rows.append(paired_bounds);local_paired.append(paired_bounds);local_paired_weather.append(paired_fields)
        result=annual_oracle(local_rows,local_weather,args.margin,args.grid_step)
        result.update(slug=slug,city=query['name'],monthly_infeasible=[r['month'] for r in local_rows if not r['jointly_feasible']],
            current_high_mae_c=summary[slug]['high_mae_current_c'],current_felt21_mae_c=summary[slug]['felt21_mae_current_c'],
            step_a_high_mae_c=summary[slug]['high_mae_step_a_c'],step_a_felt21_mae_c=summary[slug]['felt21_mae_step_a_c'])
        annual.append(result);print(slug,result['status'],result.get('oracle'),flush=True)
        paired_result=annual_oracle(local_paired,local_paired_weather,args.margin,args.grid_step)
        paired_result.update(slug=slug,city=query['name'],monthly_infeasible=[r['month'] for r in local_paired if not r['jointly_feasible']])
        paired_annual.append(paired_result)
    for name,values in [('monthly_feasibility.csv',rows),('paired_monthly_feasibility.csv',paired_rows)]:
        with (args.output/name).open('w') as stream:
            writer=csv.DictWriter(stream,fieldnames=list(values[0]));writer.writeheader();writer.writerows(values)
    provenance=dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',seconds=time.monotonic()-started,margin_c=args.margin,
        definition='Private station-oracle feasibility only; fixed native quarter-point weather; one arbitrary uniform shift per month, native Td and wind retained, exact corrected-air vapor cap. Monthly model means over all scored dates vs available-date observations, matching the previous actual-card MAE table. High window10–20 vs station24h TMAX.',
        caveats='Sparse/proxy observations retained. SF felt21 unavailable. Per-month error limit is stronger than annual acceptance. Annual oracle feasibility does not show any population/coast weighting can choose these shifts. Oracle outputs must never enter publication.',
        script_sha256=sha256(pathlib.Path(__file__)),comparison_sha256=sha256(args.validation/'comparison_step_a_metadata.json'),
        monthly_input_sha256=sha256(args.validation/'monthly_step_a.csv'),adjustment_sha256=sha256(args.adjustments),
        raw_weather_sha256={path:sha256(pathlib.Path(path)) for path in sorted(raw_files)},
        observation_inputs_sha256={str(path):sha256(path) for path in observation_files},
        raw_replay_checks=qa,annual=annual,paired_annual=paired_annual,
        paired_definition='Same-date native high predictions use dates with station TMAX; felt21 uses dates with observed air/dew/wind. Current baseline is the independent current-algorithm replay on the same dates; not encoded full-period cards.')
    (args.output/'feasibility.json').write_text(json.dumps(provenance,indent=2)+'\n')
    write_report(rows,annual,provenance,args.output)


def write_report(rows,annual,metadata,output):
    def fmt(value):return '—' if value is None else f'{value:.2f}'
    lines=['Written by AI on 2026.10.17 by model GPT-6.1 Sol.','','# Uniform correction feasibility','',
        'These are private station-oracle bounds, not a fitted candidate or publication inputs. Arbitrary monthly shifts give the uniform model its best chance; feasible does not prove that one global population/coastal rule can select suitable anchors. No existing data was changed.','',
        'Each annual budget is the current actual card monthly MAE plus0.3°C. Native air weather is shifted at every hour; native dew and wind stay fixed, with the exact corrected-air vapor cap in felt21. High compares10–20h model maxima to24h station TMAX. Sparse observations and instrument proxies are unchanged from the original comparison; SF felt21 is unavailable. Actual encoded outputs can add up to0.201°C rounding error and require their own final comparison.','',
        '| Place | High MAE current / A | Felt21 MAE current / A | Annual oracle high / felt21 | Status | Months failing stronger monthly rule |',
        '|---|---:|---:|---:|---|---|']
    for item in annual:
        oracle=item['oracle'];lines.append('| '+' | '.join([item['city'],fmt(item['current_high_mae_c'])+' / '+fmt(item['step_a_high_mae_c']),fmt(item['current_felt21_mae_c'])+' / '+fmt(item['step_a_felt21_mae_c']),fmt(oracle['high_mae_c'])+' / '+(fmt(oracle['felt21_mae_c']) if item['felt21_budget_c'] is not None else '—'),item['status'],','.join(map(str,item['monthly_infeasible'])) or 'none'])+' |')
    lines+=['','The monthly interval is the set of uniform shifts that keeps each individual monthly high error and felt21 error within its current error plus0.3°C. Empty monthly intersections alone do not prove annual failure: the annual calculation permits error to move between months. All twelve-month bounds and diagnostic oracle shifts are in monthly_feasibility.csv and feasibility.json.','',
        '| Place / month | Shift fitting high | Shift fitting felt21 | High allowed shift | Felt21 allowed shift | Jointly feasible |',
        '|---|---:|---:|---:|---:|---|']
    for row in rows:
        if row['slug'] in ('funchal','san_diego','seville'):
            lines.append('| '+' | '.join([row['city']+f" / {row['month']}",fmt(row['high_perfect_shift_c']),fmt(row['felt21_perfect_shift_c']),fmt(row['high_allowed_shift_lower_c'])+' to '+fmt(row['high_allowed_shift_upper_c']),fmt(row['felt21_allowed_shift_lower_c'])+' to '+fmt(row['felt21_allowed_shift_upper_c']),str(row['jointly_feasible'])])+' |')
    lines+=['',f"Independent raw replay matched actual Step A encoded high/felt21 in all156months within0.201°C; largest difference {max(q['maximum_encoded_difference_c'] for q in metadata['raw_replay_checks']):.6f}°C. Input hashes and method are retained in feasibility.json."]
    lines+=['','## Same-date sensitivity check','',
        'paired_monthly_feasibility.csv repeats the experiment on observed dates only, comparing against the current-algorithm replay on those same dates. It avoids mixing full-period weather means with sparse station periods; it is separate from the actual-card acceptance definition above.','',
        '| Place | Paired annual oracle high / felt21 | Current replay +0.3 budgets high / felt21 | Status |',
        '|---|---:|---:|---|']
    for item in metadata['paired_annual']:
        oracle=item['oracle'];lines.append('| '+' | '.join([item['city'],fmt(oracle['high_mae_c'])+' / '+(fmt(oracle['felt21_mae_c']) if item['felt21_budget_c'] is not None else '—'),fmt(item['high_budget_c'])+' / '+fmt(item['felt21_budget_c']),item['status']])+' |')
    (output/'FEASIBILITY.md').write_text('\n'.join(lines)+'\n')


def evaluate_candidate(args):
    """Evaluate a fixed external rule without selecting or changing its weights."""
    metadata=json.loads((args.validation/'comparison_step_a_metadata.json').read_text())
    inputs=list(csv.DictReader((args.validation/'monthly_step_a.csv').open()))
    stations=read_stations(args.observations)
    with np.load(args.candidate_adjustments) as arrays:
        adjustments={int(fine):shift for fine,shift in zip(arrays['fine'],arrays['uniform_offset_c'])}
    rows=[];raw_files=set();observations_files=[]
    for query in metadata['queries']:
        slug=query['slug'];selection=metadata['selections'][slug]
        hourly=hourly_cache(args.hourly_cache_root,slug,ZONES[slug])
        daily_path=args.observations/'daily'/f"{stations[slug]['station']}.dly"
        daily=instrument_daily_observations(daily_path)
        observations_files.extend([daily_path,args.hourly_cache_root/'hourly-cache'/f'{slug}.npz'])
        for kind in ('city','instrument'):
            fine=selection['step_a_'+kind]['fine']
            if fine not in adjustments:raise ValueError(f'Candidate lacks {slug}/{kind} fine point {fine}')
            days=[];temperature=[];dew=[];wind=[]
            for chunk in metadata['step_a']['meta']['chunks']:
                weather=point_weather(str(args.raw_root),fine,chunk,ZONES[slug]);raw_files.update(weather['raw_files'])
                days.extend(weather['dates']);temperature.extend(weather['T']);dew.extend(weather['Td']);wind.extend(weather['W']/3.6)
            temperature,dew,wind=map(np.array,(temperature,dew,wind));months=np.array([int(date[5:7]) for date in days])
            for original in [r for r in inputs if r['slug']==slug]:
                month=int(original['month']);mask=months==month;delta=float(adjustments[fine][month-1])
                high=float(temperature[mask,:11].max(1).mean()+delta)
                evening=float(felt21(temperature[mask,11],dew[mask,11],wind[mask,11],delta))
                observed_high=number(original,'station_instrument_high_c');observed_felt=number(original,'station_hourly_felt21_c')
                row=dict(slug=slug,city=query['name'],kind=kind,month=month,fine=fine,
                    point_lat=selection['step_a_'+kind]['lat'],point_lon=selection['step_a_'+kind]['lon'],
                    query_to_point_km=selection['step_a_'+kind]['distance_km'],
                    city_to_instrument_km=original['city_to_instrument_km'],
                    uniform_shift_c=delta,current_high_c=number(original,'current_'+kind+'_high_c'),
                    step_a_high_c=number(original,'step_a_'+kind+'_high_c'),candidate_high_c=high,
                    current_felt21_c=number(original,'current_'+kind+'_felt21_c'),
                    step_a_felt21_c=number(original,'step_a_'+kind+'_felt21_c'),candidate_felt21_c=evening,
                    observed_high_c=observed_high,observed_felt21_c=observed_felt,
                    candidate_air21_c=float(temperature[mask,11].mean()+delta),
                    observed_air21_c=number(original,'station_hourly_air21_c'),
                    high_observation_days=original['station_instrument_high_days'],
                    felt21_observation_days=original['station_hourly_felt21_days'],
                    high_coverage=original['station_instrument_high_coverage'],
                    felt21_coverage=original['station_hourly_felt21_coverage'])
                high_mask=np.array([int(date[5:7])==month and 'TMAX' in daily.get(date,{}) for date in days])
                row['candidate_high_same_observed_dates_c']=float(temperature[high_mask,:11].max(1).mean()+delta) if high_mask.any() else None
                felt_indices=[i for i,date in enumerate(days) if months[i]==month and hourly.get(date,{}).get('felt21') is not None]
                row.update(dict.fromkeys(['paired_model_air21_c','paired_observed_air21_c','paired_native_dew21_c',
                    'paired_observed_dew21_c','paired_native_wind21_ms','paired_observed_wind21_ms',
                    'paired_felt21_bias_c','paired_air21_bias_c','paired_vapor_bias_c','paired_wind_bias_c']))
                if felt_indices:
                    index=np.array(felt_indices);air=temperature[index,11]+delta;native_dew=dew[index,11];native_wind=wind[index,11]
                    observed=np.array([[hourly[days[i]]['air21'],hourly[days[i]]['hours'][21]['dew_point_temperature'][0],hourly[days[i]]['hours'][21]['wind_speed'][0]] for i in index])
                    def vapor(t,td):
                        capped=np.minimum(t,td);return 6.105*np.exp(17.27*capped/(237.7+capped))
                    air_bias=float((air-observed[:,0]).mean())
                    vapor_bias=float((.33*(vapor(air,native_dew)-vapor(observed[:,0],observed[:,1]))).mean())
                    wind_bias=float((-.70*(native_wind-observed[:,2])).mean())
                    row.update(paired_model_air21_c=float(air.mean()),paired_observed_air21_c=float(observed[:,0].mean()),
                        paired_native_dew21_c=float(native_dew.mean()),paired_observed_dew21_c=float(observed[:,1].mean()),
                        paired_native_wind21_ms=float(native_wind.mean()),paired_observed_wind21_ms=float(observed[:,2].mean()),
                        paired_felt21_bias_c=air_bias+vapor_bias+wind_bias,paired_air21_bias_c=air_bias,
                        paired_vapor_bias_c=vapor_bias,paired_wind_bias_c=wind_bias)
                rows.append(row)
        print('Candidate replay',slug,flush=True)
    annual=[]
    for query in metadata['queries']:
        for kind in ('city','instrument'):
            selected=[r for r in rows if r['slug']==query['slug'] and r['kind']==kind]
            result=dict(slug=query['slug'],city=query['name'],kind=kind)
            for variable,truth in [('high','observed_high_c'),('felt21','observed_felt21_c')]:
                valid=[r for r in selected if r[truth] is not None]
                for model in ('current','step_a','candidate'):
                    result[f'{model}_{variable}_mae_c']=float(np.mean([abs(r[model+'_'+variable+'_c']-r[truth]) for r in valid])) if valid else None
                result[variable+'_passes']=result['candidate_'+variable+'_mae_c']<=result['current_'+variable+'_mae_c']+args.margin if valid else None
            result['passes_both']=result['high_passes'] and result['felt21_passes'] if result['felt21_passes'] is not None else None
            annual.append(result)
    output=args.output/'candidate';output.mkdir(exist_ok=True)
    with (output/'monthly_candidate.csv').open('w') as stream:
        writer=csv.DictWriter(stream,fieldnames=list(rows[0]));writer.writeheader();writer.writerows(rows)
    provenance=dict(written_by='AI, GPT-6.1 Sol, 2026.10.17',definition='Fixed externally selected uniform shifts; no weight tuning; same frozen city/instrument lookup and observation coverage as original Step A. High10–20 vs station24h TMAX; native Td/wind retained, corrected-air vapor cap.',
        candidate_adjustments=dict(path=str(args.candidate_adjustments),sha256=sha256(args.candidate_adjustments)),
        script_sha256=sha256(pathlib.Path(__file__)),comparison_sha256=sha256(args.validation/'comparison_step_a_metadata.json'),
        monthly_input_sha256=sha256(args.validation/'monthly_step_a.csv'),
        raw_weather_sha256={path:sha256(pathlib.Path(path)) for path in sorted(raw_files)},
        observation_inputs_sha256={str(path):sha256(path) for path in observations_files},margin_c=args.margin,annual=annual)
    (output/'candidate_summary.json').write_text(json.dumps(provenance,indent=2)+'\n')
    def fmt(x):return '—' if x is None else f'{x:.2f}'
    lines=['Written by AI on 2026.10.17 by model GPT-6.1 Sol.','','# Fixed settlement-rule diagnostic','',
        'These are independent raw-hourly replays using the fixed candidate shifts, before any rebuilt compact facts. Native humidity and wind are retained. Station observations are private evaluation only. Actual-card quantization must still be checked after a build.','',
        '| Place | Query | High MAE current / A / candidate | Felt21 MAE current / A / candidate | Both within current +0.3°C |',
        '|---|---|---:|---:|---|']
    for r in annual:
        lines.append('| '+' | '.join([r['city'],r['kind'],' / '.join(fmt(r[m+'_high_mae_c']) for m in ('current','step_a','candidate')),' / '.join(fmt(r[m+'_felt21_mae_c']) for m in ('current','step_a','candidate')),str(r['passes_both'])])+' |')
    lines+=['','## July city queries','',
        '| Place | High current / A / candidate / instrument | Felt21 current / A / candidate / instrument |',
        '|---|---:|---:|']
    for r in rows:
        if r['kind']=='city' and r['month']==7:
            lines.append('| '+' | '.join([r['city'],' / '.join(fmt(r[k]) for k in ('current_high_c','step_a_high_c','candidate_high_c','observed_high_c')),' / '.join(fmt(r[k]) for k in ('current_felt21_c','step_a_felt21_c','candidate_felt21_c','observed_felt21_c'))])+' |')
    lines+=['','City observations are the same explicitly declared instruments/proxies, often airports away from the city. Coordinates, distances, all12months and observation counts are in monthly_candidate.csv. SF felt21 is unavailable; Valparaíso and some daily-high records are sparse. No full observed station score is inferred.']
    for r in rows:
        if r['slug']=='funchal' and r['kind']=='city' and r['month']==7:
            lines+=['',f"Funchal July, paired evenings: air21 {fmt(r['paired_model_air21_c'])} versus observed{fmt(r['paired_observed_air21_c'])}°C; native dew{fmt(r['paired_native_dew21_c'])} versus observed{fmt(r['paired_observed_dew21_c'])}°C; native wind{fmt(r['paired_native_wind21_ms'])} versus observed{fmt(r['paired_observed_wind21_ms'])}m/s. Felt bias{fmt(r['paired_felt21_bias_c'])}°C = air{fmt(r['paired_air21_bias_c'])} + vapor{fmt(r['paired_vapor_bias_c'])} + wind{fmt(r['paired_wind_bias_c'])}°C."]
    (output/'CANDIDATE.md').write_text('\n'.join(lines)+'\n')


if __name__=='__main__':main()
