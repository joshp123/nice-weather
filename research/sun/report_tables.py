"""Written by AI on 2026.10.17 by model GPT-6.1 Sol.

Render measured CSV outputs into the report's monthly comparison tables.
Run after compare_sources.py and prototype.py have completed.
"""
import calendar
import csv
from pathlib import Path

ROOT = Path('/Volumes/Josh Extreme Pro USB4 4TB/Code/nice-weather-data/sun')
ORDER = ['lima', 'antofagasta', 'swakopmund', 'valparaiso', 'san-francisco',
         'malaga', 'seville', 'cape-town', 'tenerife-south', 'san-diego', 'rotterdam', 'singapore']


def read(name):
    return list(csv.DictReader((ROOT/name).open()))


def main():
    monthly = read('prototype/monthly.csv')
    paired = read('comparison/matched-monthly.csv')
    holdout = read('comparison/year-holdout.csv')
    output = ['<!-- Written by AI on 2026.10.17 by model GPT-6.1 Sol. -->',
              '## Monthly sunshine and default scores', '',
              'All sunshine columns are percent. Scores are out of 100. “Card” is the published native-point fact; “engine” is the existing eight-day, four-cell blend. “After” is the encoded sunshine-offset probe. Temperature and all other weather stay fixed. Thus the card-to-after difference is not entirely a change to engine sunshine. Singapore has no independent DNI sample here: its engine and score are unchanged, and its card should remain unchanged too.',
              '', '| Place | Independent radiation period | Mean monthly score before → after |',
              '| --- | --- | ---: |']
    for slug in ORDER:
        rows = [row for row in monthly if row['slug'] == slug]
        if len(rows) != 12:
            raise ValueError(f'{slug}: expected twelve months, found {len(rows)}')
        name = rows[0]['place']
        period = rows[0]['source_years'].replace(';', ', ') or 'No DNI correction'
        mean = lambda field: sum(float(row[field]) for row in rows)/12
        output.append(f"| {name} | {period} | {mean('before_score'):.1f} → {mean('after_score'):.1f} |")
    for slug in ORDER:
        rows = sorted([row for row in monthly if row['slug'] == slug], key=lambda row: int(row['month']))
        output += ['', f"### {rows[0]['place']}", '',
                   f"Source: {rows[0]['source']}; years: {rows[0]['source_years'].replace(';', ', ') or 'none'}. These are observed-period sensitivity targets, not established long-term climate normals.",
                   '', '| Month | Card sun | Engine sun | After sun | Default score before → after |',
                   '| --- | ---: | ---: | ---: | ---: |']
        for row in rows:
            number = lambda field: float(row[field])
            output.append(f"| {calendar.month_abbr[int(row['month'])]} | {number('before_card_sunny_percent'):.0f}% | {number('before_engine_sunny_percent'):.1f}% | {number('after_sunny_percent'):.1f}% | {number('before_score'):.1f} → {number('after_score'):.1f} |")
    output += ['', '## Matched July dates', '',
               'Both ERA5 columns and the independent source use exactly the same retained local dates. Satellite values here are at town centres; the co-located check in the main report addresses the remaining location difference.',
               '', '| Place | Paired days | 1° ERA5 record | Native ERA5 point | Independent satellite |',
               '| --- | ---: | ---: | ---: | ---: |']
    for slug in ORDER:
        for row in paired:
            if row['slug'] == slug and int(row['month']) == 7:
                output.append(f"| {row['place']} | {row['paired_days']} | {float(row['era5_paired_cell_percent']):.1f}% | {float(row['era5_paired_native_percent']):.1f}% | {float(row['source_paired_percent']):.1f}% |")
    output += ['', '## Held-out year check', '',
               'For each month, fit native ERA5-to-satellite bias in the earlier source year, add it to each retained ERA5 day in the later year and clamp to 0–100%. Compare monthly means against that later satellite year, without fitting to it. Only paired months with at least 15 days in both years enter. The retained ERA5 chunks provide January–September for the PVGIS comparison and January–August for the NSRDB comparison, not a full held-out year. This tests transfer across years within the independent source; it does not prove ground-radiometer accuracy or validate every month individually.',
               '', '| Place | Train → test | Months | Mean absolute sunny-share error before → after | Months with larger error |',
               '| --- | --- | ---: | ---: | ---: |']
    for slug in ORDER:
        rows = [row for row in holdout if row['slug'] == slug]
        if not rows:
            continue
        name = next(row['place'] for row in monthly if row['slug'] == slug)
        mean = lambda field: sum(float(row[field]) for row in rows)/len(rows)
        worse = sum(float(row['after_error_percentage_points']) > float(row['before_error_percentage_points']) for row in rows)
        output.append(f"| {name} | {rows[0]['train_year']} → {rows[0]['test_year']} | {len(rows)} | {mean('before_error_percentage_points'):.1f} → {mean('after_error_percentage_points'):.1f} percentage points | {worse} |")
    path = ROOT/'comparison/report-tables.md'
    path.write_text('\n\n'.join([output[0], '\n'.join(output[1:])])+'\n')
    print(path)


if __name__ == '__main__':
    main()
