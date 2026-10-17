"""Validate delivered exact-pixel B1 replay bytes against frozen city evidence.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
All station values remain private comparison inputs. The production candidate
contains no observations, fitted adjustments or per-place numerical exceptions.
"""
import argparse
import csv
import gzip
import json
import pathlib
import statistics
import time

import numpy as np

from compare_step_a import climate_pipeline
from fine_city_probe import fine_pixel
from native_normals import file_sha256
from tiles import decode_tile


def corrected_air(air, high, low, high_offset, low_offset):
    width = high-low
    ordered_exception = (width == 0) | (high+high_offset <= low+low_offset)
    weight = np.divide(air-low, width, out=np.full_like(air, .5), where=width != 0)
    delta = low_offset+weight*(high_offset-low_offset)
    delta = np.where(ordered_exception, (high_offset+low_offset)/2, delta)
    return air+delta


def packet_facts(packet, offsets):
    days = len(packet['dates'])
    air = np.array(packet['airC']).reshape(days, 12)
    dew = np.array(packet['dewC']).reshape(days, 12)
    high, low = np.array(packet['highC']), np.array(packet['lowC'])
    months = np.array([int(date[5:7]) for date in packet['dates']])
    hi, lo = offsets[months-1].T
    corrected = corrected_air(air, high[:, None], low[:, None], hi[:, None], lo[:, None])
    native_shade = np.array(packet['shadeC']).reshape(days, 12)
    vapor = lambda value: 6.105*np.exp(17.27*value/(237.7+value))
    shade = native_shade+corrected-air+.33*(vapor(np.minimum(dew, corrected))-vapor(np.minimum(dew, air)))
    return [dict(month=month, high_c=float(corrected[months == month, :11].max(1).mean()),
        air21_c=float(corrected[months == month, 11].mean()),
        felt21_c=float(shade[months == month, 11].mean())) for month in range(1, 13)]


def packet_month_judgements(packet, offsets):
    """Independent Python reference scores, then the current threshold median."""
    days = len(packet['dates'])
    air = np.array(packet['airC']).reshape(days, 12)
    dew = np.array(packet['dewC']).reshape(days, 12)
    months = np.array([int(date[5:7]) for date in packet['dates']])
    corrected = corrected_air(air, np.array(packet['highC'])[:, None], np.array(packet['lowC'])[:, None],
        offsets[months-1, 0, None], offsets[months-1, 1, None])
    vapor = lambda value: 6.105*np.exp(17.27*value/(237.7+value))
    felt_delta = corrected-air+.33*(vapor(np.minimum(dew, corrected))-vapor(np.minimum(dew, air)))
    features = np.zeros((days, 29))
    features[:, :11] = (np.array(packet['sunC']).reshape(days, 12)+felt_delta)[:, :11]
    features[:, 11:23] = np.array(packet['shadeC']).reshape(days, 12)+felt_delta
    features[:, 23] = np.minimum(dew, corrected).mean(1)
    for column, key in enumerate(('rainMm', 'windKmh', 'sunnyHours', 'daylightHours'), 24):
        features[:, column] = packet[key]
    scores = np.floor(climate_pipeline().score_features(features)*10000+.5)/10000
    thresholds = np.array([0, 25, 40, 55, 70, 85, 100])
    results = []
    for month, calendar_days in enumerate((31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31), 1):
        selected = [index for index in packet['selectedIndices'] if months[index] == month]
        shares = (scores[selected, None] >= thresholds).mean(0)
        band = np.flatnonzero(shares[:6] >= .5-1e-9)[-1]
        score = 85+(shares[5]-.5)*30 if band == 5 else thresholds[band]+(thresholds[band+1]-thresholds[band])*(shares[band]-.5)/(shares[band]-shares[band+1])
        results.append(dict(score=float(np.floor(max(thresholds[band], score)*10000+.5)/10000),
            niceDayShare=float(shares[3]), perfectDayShare=float(shares[5]), awfulDayShare=float(1-shares[1]),
            niceDayCount=float(shares[3]*calendar_days), perfectDayCount=float(shares[5]*calendar_days),
            awfulDayCount=float((1-shares[1])*calendar_days)))
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bundle', type=pathlib.Path, required=True)
    parser.add_argument('--tiles', type=pathlib.Path, required=True)
    parser.add_argument('--baseline', type=pathlib.Path, required=True)
    parser.add_argument('--today-capture', type=pathlib.Path, help='Fresh live city API capture; assert all frozen baseline high/felt values remain current')
    parser.add_argument('--module-results', type=pathlib.Path, help='Shared module JSONL {slug,months,judgements}; independently compare all monthly facts and typical-day counts')
    parser.add_argument('--output', type=pathlib.Path, required=True)
    parser.add_argument('--margin', type=float, default=.3)
    args = parser.parse_args()
    started = time.monotonic()
    args.output.mkdir(parents=True, exist_ok=True)
    manifest = json.loads((args.bundle/'manifest.json').read_text())
    tile_configuration = json.loads((args.tiles/'configuration.json').read_text())
    if manifest['generation'] != tile_configuration['generation'] or manifest['chunks'] != tile_configuration['chunks']:
        raise ValueError('Replay packets and correction tiles belong to different generations')
    baseline = {(row['slug'], int(row['month'])): row for row in csv.DictReader(args.baseline.open())}
    live_comparisons = 0
    if args.today_capture:
        capture = json.loads(args.today_capture.read_text())
        for place in capture['places']:
            for month in place['months']:
                prior = baseline[place['slug'], month['month']]
                for field, original in (('highC', 'current_city_high_c'), ('feelsAt21C', 'current_city_felt21_c')):
                    if month[field] != float(prior[original]):
                        raise ValueError(f'Frozen baseline no longer equals today: {place["slug"]}/{month["month"]}/{field}')
                    live_comparisons += 1
    rows, summary, hashes = [], [], {}
    module = {row['slug']: row for line in args.module_results.read_text().splitlines() if line.startswith('{') for row in [json.loads(line)]} if args.module_results else {}
    parity = dict(facts_compared=0, judgements_compared=0, maximum_absolute_difference=0.)
    for query in manifest['queries']:
        pixel = fine_pixel(query['lat'], query['lon'])
        row = int(np.floor((pixel['row']+.5)/30+.5))
        column = int(np.floor((pixel['column']+.5)/30+.5))%1440
        fine = row*1440+column
        path = args.bundle/f'native/{fine}.json.gz'
        hashes[str(path)] = file_sha256(path)
        packet = json.loads(gzip.decompress(path.read_bytes()))
        if packet['fineIndex'] != fine or packet['generation'] != manifest['generation']:
            raise ValueError('Replay identity differs from exact fine pixel native reference')
        offsets = []
        supports = []
        for month in range(1, 13):
            path = args.tiles/f'tiles/{month:02d}/{pixel["row"]//256:03d}/{pixel["column"]//256:03d}.nwf.gz'
            tile = decode_tile(path)
            hashes[str(path)] = file_sha256(path)
            support = int(tile['support'][pixel['row']%256, pixel['column']%256])
            if tile['generation'] != manifest['generation'] or not support & 8:
                raise ValueError('City fine pixel has no same-generation native replay support')
            offsets.append(tile['offsets_c'][pixel['row']%256, pixel['column']%256])
            supports.append(support)
        facts = packet_facts(packet, np.array(offsets))
        if module:
            delivered = module[query['slug']]
            judgements = packet_month_judgements(packet, np.array(offsets))
            for index in range(12):
                for name, field in (('highC', 'high_c'), ('feelsAt21C', 'felt21_c')):
                    difference = abs(delivered['months'][index][name]-facts[index][field])
                    parity['facts_compared'] += 1
                    parity['maximum_absolute_difference'] = max(parity['maximum_absolute_difference'], difference)
                for name, expected in judgements[index].items():
                    difference = abs(delivered['judgements'][index][name]-expected)
                    parity['judgements_compared'] += 1
                    parity['maximum_absolute_difference'] = max(parity['maximum_absolute_difference'], difference)
            if parity['maximum_absolute_difference'] > 1e-5:
                raise ValueError('Shared module differs from independent delivered-byte replay')
        city_rows = []
        for values in facts:
            original = baseline[query['slug'], values['month']]
            metric = 'air21' if query['slug'] == 'sf' else 'felt21'
            current_evening_key = 'current_air21_c' if metric == 'air21' else 'current_city_felt21_c'
            observed_evening_key = 'station_hourly_air21_c' if metric == 'air21' else 'station_hourly_felt21_c'
            month_index = values['month']-1
            record = dict(slug=query['slug'], city=query['name'], month=values['month'],
                query_latitude=query['lat'], query_longitude=query['lon'], pixel_row=pixel['row'], pixel_column=pixel['column'],
                native_fine=fine, source=supports[month_index]&7, support=supports[month_index],
                high_offset_c=float(offsets[month_index][0]), low_offset_c=float(offsets[month_index][1]),
                current_high_c=float(original['current_city_high_c']), candidate_high_c=values['high_c'],
                observed_high_c=float(original['station_instrument_high_c']) if original['station_instrument_high_c'] else None,
                evening_metric=metric, current_evening_c=float(original[current_evening_key]),
                candidate_evening_c=values[metric+'_c'], observed_evening_c=float(original[observed_evening_key]) if original[observed_evening_key] else None,
                candidate_felt21_c=values['felt21_c'], candidate_air21_c=values['air21_c'],
                observed_high_days=int(original['station_instrument_high_days']),
                observed_evening_days=int(original['station_hourly_'+metric+'_days']))
            rows.append(record); city_rows.append(record)
        result = dict(slug=query['slug'], city=query['name'], evening_metric=city_rows[0]['evening_metric'])
        for field in ('high', 'evening'):
            for model in ('current', 'candidate'):
                result[model+'_'+field+'_mae_c'] = statistics.mean(abs(record[model+'_'+field+'_c']-record['observed_'+field+'_c']) for record in city_rows if record['observed_'+field+'_c'] is not None)
            result[field+'_regression_c'] = result['candidate_'+field+'_mae_c']-result['current_'+field+'_mae_c']
        result['passes_margin'] = max(result['high_regression_c'], result['evening_regression_c']) <= args.margin+1e-9
        result['passes_strict_no_regression'] = max(result['high_regression_c'], result['evening_regression_c']) <= 1e-9
        summary.append(result)
    with (args.output/'monthly.csv').open('w') as stream:
        writer = csv.DictWriter(stream, fieldnames=list(rows[0])); writer.writeheader(); writer.writerows(rows)
    report = dict(written_by='AI, GPT-6.1 Sol, 2026.10.18', generation=manifest['generation'], chunks=manifest['chunks'],
        passes=all(record['passes_margin'] for record in summary), margin_c=args.margin, places=summary,
        definition='Delivered native JSON gzip and exact containing NWFT pixels. Full assembled-period mean high10–20 and felt21, not representative-day means. Frozen current city cards and same instruments/observations; SF air21 is the original waterfront proxy, not felt21.',
        baseline_sha256=file_sha256(args.baseline), manifest_sha256=file_sha256(args.bundle/'manifest.json'),
        live_baseline=dict(path=str(args.today_capture), sha256=file_sha256(args.today_capture), identical_values=live_comparisons) if args.today_capture else None,
        module_parity=dict(path=str(args.module_results), sha256=file_sha256(args.module_results), **parity) if args.module_results else None,
        decoded_input_sha256=hashes, seconds=time.monotonic()-started)
    (args.output/'acceptance.json').write_text(json.dumps(report, indent=2)+'\n')
    lines = ['Written by AI on 2026.10.18 by model GPT-6.1 Sol.', '',
        'This candidate fails the city non-regression gate. Do not publish it.' if not report['passes'] else 'All city errors pass the stated margin.', '',
        'Monthly mean absolute errors, °C. Evening is felt21 except San Francisco’s same air21 waterfront proxy. Full-period physical means come from actual delivered replay packets and correction tile bytes. High compares model10–20 with observed24h TMAX; the same instrument and coverage caveats remain.', '',
        '| Place | High current → exact1km | 21:00 current → exact1km | +0.3°C gate |', '|---|---:|---:|---|']
    for record in summary:
        lines.append(f'| {record["city"]} | {record["current_high_mae_c"]:.2f} → {record["candidate_high_mae_c"]:.2f} | {record["current_evening_mae_c"]:.2f} → {record["candidate_evening_mae_c"]:.2f} | {"pass" if record["passes_margin"] else "FAIL"} |')
    (args.output/'COMPARISON.md').write_text('\n'.join(lines)+'\n')
    print(json.dumps(dict(passes=report['passes'], failures=[record['slug'] for record in summary if not record['passes_margin']], seconds=report['seconds'])), flush=True)


if __name__ == '__main__': main()
