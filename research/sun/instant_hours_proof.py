# Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""Check instantaneous DNI integration against SARAH and civil-time examples."""
import argparse
import csv
import datetime as dt
import gzip
import hashlib
import json
from pathlib import Path
import time

import numpy as np

from instant_hours import integrate_civil_hours
from named_satellite import summarize_tmy, solar_sunset

UTC = dt.timezone.utc


def main(args):
    comparisons = 0
    maximum_difference = 0
    targets = json.loads(args.targets.read_text())['records']
    started = time.monotonic()
    for target in targets:
        key = f"{round(target['latitude']*1e5)},{round(target['longitude']*1e5)}"
        directory = args.source/'pvgis'/hashlib.sha256(key.encode()).hexdigest()[:2]
        metadata = json.loads((directory/f'{key}.metadata.json').read_text())
        lines = gzip.decompress((directory/f'{key}.csv.gz').read_bytes()).decode().splitlines()
        header = next(i for i, line in enumerate(lines) if line.startswith('time(UTC),'))
        hourly = list(csv.DictReader(lines[header:header+8761]))
        payload = dict(inputs=metadata['inputs'], outputs=dict(months_selected=metadata['outputs']['months_selected'], tmy_hourly=[{'time(UTC)':row['time(UTC)'], 'Gb(n)':float(row['Gb(n)'])} for row in hourly]))
        source_shift = metadata['inputs']['location']['irradiance_time_offset']
        nominal_start = dt.datetime(2001, 1, 1, tzinfo=UTC)
        stamps = [(nominal_start+dt.timedelta(hours=i+source_shift)).isoformat() for i in range(8760)]
        years = [row['year'] for row in metadata['outputs']['months_selected']]
        days = integrate_civil_hours(np.array([float(row['Gb(n)']) for row in hourly]), stamps, target['timezone'], years)
        expected = summarize_tmy(payload, target['latitude'], target['longitude'], target['timezone'])
        actual = [[] for _ in range(12)]
        for day in days:
            assert day['complete'].all()
            actual_date = day['date'].replace(year=day['actual_year'])
            daylight = solar_sunset(actual_date, target['latitude'], target['longitude'], day['utc_offset_hours'])
            share = np.count_nonzero(day['hourly_dni'][:daylight] >= 300)/daylight
            actual[day['date'].month-1].append([share, daylight, *day['hourly_dni']])
        for month, rows in enumerate(actual):
            average = np.mean(rows, axis=0)
            reference = np.array([expected[month]['sunnyShare'], expected[month]['daylightHours'], *expected[month]['hourlyDni']])
            difference = float(np.max(np.abs(average-reference)))
            maximum_difference = max(maximum_difference, difference)
            assert difference < 1e-9, (key, month+1, difference)
            comparisons += len(average)

    # Modern NSRDB midpoint snapshots: hour10 averages three samples, not one.
    dates = [dt.datetime(2024, 1, 1, tzinfo=UTC)+dt.timedelta(hours=i, minutes=30) for i in range(48)]
    values = np.array([float(date.hour**2) for date in dates])
    result = integrate_civil_hours(values, [date.isoformat() for date in dates], 'UTC')
    expected = .125*values[9]+.75*values[10]+.125*values[11]
    assert abs(result[0]['hourly_dni'][0]-expected) < 1e-12
    assert expected != values[10]
    partial = integrate_civil_hours([100, 200], [dates[10].isoformat(), dates[11].isoformat()], 'UTC')
    assert not partial[0]['complete'][0]

    # Fractional offsets integrate the same linear instant series exactly.
    base = dt.datetime(2024, 1, 1, tzinfo=UTC)
    dates = [base+dt.timedelta(minutes=10*i) for i in range(3*24*6)]
    values = np.arange(len(dates), dtype=float)/6
    for zone, offset in [('Asia/Kolkata', 5.5), ('Asia/Kathmandu', 5.75)]:
        day = next(day for day in integrate_civil_hours(values, [date.isoformat() for date in dates], zone) if day['date']==dt.date(2024, 1, 2))
        assert day['complete'].all()
        np.testing.assert_allclose(day['hourly_dni'], 24+np.arange(10, 22)+.5-offset, atol=1e-12)

    # Real full-disc leap days survive; synthetic years omit February29 and
    # preserve all365joined days even when source years change each month.
    dates = [dt.datetime(2024, 2, 28, tzinfo=UTC)+dt.timedelta(hours=i) for i in range(72)]
    real_days = integrate_civil_hours(np.ones(72), [date.isoformat() for date in dates], 'UTC')
    assert dt.date(2024, 2, 29) in [day['date'] for day in real_days]
    dates = [dt.datetime(2024, 1, 1, tzinfo=UTC)+dt.timedelta(hours=i, minutes=30) for i in range(8784)]
    stamps = [date.isoformat() for date in dates if (date.month, date.day)!=(2, 29)]
    years = [1998+i for i in range(12)]
    typical_days = integrate_civil_hours(np.ones(8760)*400, stamps, 'America/Los_Angeles', years)
    assert len(typical_days)==365
    for day in typical_days:
        assert day['date'].year==2001 and day['actual_year']==years[day['date'].month-1]
        assert day['complete'].all()
        np.testing.assert_allclose(day['hourly_dni'], 400, atol=1e-10)
    report = dict(writtenBy='AI, GPT-6.1 Sol, 2026-10-18', passes=True, sarahPlaces=len(targets), sarahNumericComparisons=comparisons, maximumSarahDifference=maximum_difference, midpointCase=True, fractionalZones=['Asia/Kolkata','Asia/Kathmandu'], observedLeapDay=True, joinedTypicalDays=len(typical_days), selectedYearOffsets=True, seconds=time.monotonic()-started)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2)+'\n')
    print(json.dumps(report))


if __name__=='__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source',type=Path,required=True)
    parser.add_argument('--targets',type=Path,required=True)
    parser.add_argument('--output',type=Path,required=True)
    main(parser.parse_args())
