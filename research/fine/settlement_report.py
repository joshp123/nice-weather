"""Compare rebuilt settlement-weighted Step A with the frozen station baseline.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
Station-derived reports stay outside the repository and publication folders.
"""
import argparse
import csv
import hashlib
import json
import pathlib

import numpy as np


def read_rows(path):
    with path.open() as stream:
        return {(row['slug'],int(row['month'])):row for row in csv.DictReader(stream)}


def number(row,field):
    return float(row[field]) if row.get(field) not in ('',None) else None


def mae(rows,prediction,truth):
    pairs = [(number(row,prediction),number(row,truth)) for row in rows]
    errors = [abs(predicted-observed) for predicted,observed in pairs
              if predicted is not None and observed is not None]
    return float(np.mean(errors)) if errors else None


def display(value):
    return '—' if value is None else f'{value:.2f}'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--original',type=pathlib.Path,required=True)
    parser.add_argument('--candidate',type=pathlib.Path,required=True)
    parser.add_argument('--output',type=pathlib.Path,required=True)
    parser.add_argument('--margin',type=float,default=.3)
    args = parser.parse_args()
    original,candidate = read_rows(args.original),read_rows(args.candidate)
    if original.keys() != candidate.keys() or len(original) != 13*12:
        raise ValueError('Comparison must retain the same thirteen places and twelve months')
    frozen = ['station','station_lat','station_lon','query_lat','query_lon',
              'station_instrument_high_c','station_instrument_high_days',
              'station_hourly_felt21_c','station_hourly_felt21_days',
              'station_hourly_air21_c','station_hourly_air21_days',
              'current_instrument_high_c','current_instrument_felt21_c','current_air21_c',
              'current_city_high_c','current_city_felt21_c']
    for key,row in original.items():
        for field in frozen:
            if row[field] != candidate[key][field]:
                raise ValueError(f'Frozen baseline or observation changed: {key}/{field}')
    summaries = []
    for slug in dict.fromkeys(key[0] for key in original):
        before = [original[slug,month] for month in range(1,13)]
        after = [candidate[slug,month] for month in range(1,13)]
        evening_available = any(number(row,'station_hourly_felt21_c') is not None for row in before)
        evening_truth = 'station_hourly_felt21_c' if evening_available else 'station_hourly_air21_c'
        evening_current = 'current_instrument_felt21_c' if evening_available else 'current_air21_c'
        evening_step = 'step_a_instrument_felt21_c' if evening_available else 'step_a_air21_c'
        values = dict(slug=slug,place=before[0]['city'],evening_metric='felt21' if evening_available else 'air21 proxy',
            high_current=mae(before,'current_instrument_high_c','station_instrument_high_c'),
            high_original=mae(before,'step_a_instrument_high_c','station_instrument_high_c'),
            high_candidate=mae(after,'step_a_instrument_high_c','station_instrument_high_c'),
            evening_current=mae(before,evening_current,evening_truth),
            evening_original=mae(before,evening_step,evening_truth),
            evening_candidate=mae(after,evening_step,evening_truth))
        values['high_regression'] = values['high_candidate']-values['high_current']
        values['evening_regression'] = (values['evening_candidate']-values['evening_current']
                                        if values['evening_current'] is not None else None)
        values['passes'] = (values['high_regression']<=args.margin+1e-9 and
                           (values['evening_regression'] is None or values['evening_regression']<=args.margin+1e-9))
        summaries.append(values)
    lines = ['Written by AI on 2026.10.18 by model GPT-6.1 Sol.','',
        '# Settlement-weighted Step A: acceptance comparison','',
        'Monthly-mean absolute errors across the same twelve months and instruments; °C. '
        'Current means the frozen current-site capture used in the original comparison. '
        'The original and rebuilt products use the same native quarter-point selection. '
        'No validation observation enters the population weighting.','',
        'The pass limit is current-site MAE + '+display(args.margin)+' °C for both high and evening. '
        'High is product 10–20h versus station 24h TMAX, as in the original report. '
        'Evening is felt21 except San Francisco, where only the separately labelled air21 waterfront proxy is available. '
        'An observed full-weather score is unavailable.','',
        '| Place | High MAE current / original A / population | 21:00 MAE current / original A / population | Pass |',
        '|---|---:|---:|:---:|']
    for row in summaries:
        lines.append('| '+row['place']+(' (air21 proxy)' if row['evening_metric']=='air21 proxy' else '')+' | '+
            ' / '.join(display(row['high_'+version]) for version in ('current','original','candidate'))+' | '+
            ' / '.join(display(row['evening_'+version]) for version in ('current','original','candidate'))+' | '+
            ('yes' if row['passes'] else '**NO**')+' |')
    lines += ['','## July city cards','',
        'City-card locations can differ substantially from the instrument. '
        'The complete monthly CSV retains both queries, point coordinates, coverage and paired-date diagnostics.','',
        '| Place | High current / original A / population / instrument | Felt21 current / original A / population / instrument |',
        '|---|---:|---:|']
    for row in summaries:
        before,after = original[row['slug'],7],candidate[row['slug'],7]
        high = [number(before,'current_city_high_c'),number(before,'step_a_city_high_c'),
                number(after,'step_a_city_high_c'),number(after,'station_instrument_high_c')]
        evening = [number(before,'current_city_felt21_c'),number(before,'step_a_city_felt21_c'),
                   number(after,'step_a_city_felt21_c'),number(after,'station_hourly_felt21_c')]
        lines.append('| '+row['place']+' | '+' / '.join(map(display,high))+' | '+' / '.join(map(display,evening))+' |')
    passed = all(row['passes'] for row in summaries)
    lines += ['', '**Acceptance: '+('PASS' if passed else 'FAIL — recommend shipping nothing')+'.**','',
        'This test evaluates the temperature correction. Score aggregation has been developed separately; '
        'temperature acceptance does not use the default-score columns. '
        'Detailed results: [monthly_step_a.csv](monthly_step_a.csv).','']
    args.output.mkdir(parents=True,exist_ok=True)
    (args.output/'SETTLEMENT_COMPARISON.md').write_text('\n'.join(lines))
    result = dict(written_by='AI, GPT-6.1 Sol, 2026.10.18',margin_c=args.margin,passes=passed,places=summaries,
        baseline_sha256=hashlib.sha256(args.original.read_bytes()).hexdigest(),
        candidate_sha256=hashlib.sha256(args.candidate.read_bytes()).hexdigest(),
        scope='Private validation; no station-derived value is an input to the published correction')
    (args.output/'settlement-acceptance.json').write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result),flush=True)


if __name__ == '__main__': main()
