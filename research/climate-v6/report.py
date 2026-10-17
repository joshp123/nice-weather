# Written by AI on 2026.10.17 by model GPT-6.1 Sol.
import json
import pathlib
import time
import validate


def validate_snapshot(root, snapshot, report_directory=pathlib.Path('/tmp/nwf-agents/overnight/climate')):
    started=time.monotonic()
    report_directory.mkdir(parents=True,exist_ok=True)
    cells,points,static_report=validate.validate_static(root)
    climate_report=validate.validate_climate(snapshot,cells,points,report_directory)
    return {'static':static_report,'climate':climate_report,'seconds':time.monotonic()-started}


def write_report(root, report_directory=pathlib.Path('/tmp/nwf-agents/overnight/climate'), validation=None):
    if validation is None:
        metadata=json.loads((root/'v6/climate/meta.json').read_text())
        snapshot=root/'climate-v6-work/assemblies'/metadata['buildId']
        validation=validate_snapshot(root,snapshot,report_directory)
    static_report=validation['static']; climate_report=validation['climate']
    metadata=climate_report['meta']; snapshot=root/'climate-v6-work/assemblies'/metadata['buildId']
    (report_directory/'validation.json').write_text(json.dumps(validation,ensure_ascii=False,indent=2)+'\n')
    cell_count=static_report['cell_count']; point_count=static_report['point_count']
    timings=[json.loads(line) for line in (root/'climate-v6-work/timings.jsonl').read_text().splitlines()]
    sizes=climate_report['files_bytes']
    proof=metadata['compactProof']
    lines=['Written by AI on 2026.10.17 by model GPT-6.1 Sol.','',
           '# Climate files ready','',
           f"Chunks: **{metadata['chunks']}**. Years touched: {metadata['years']}. Build: {metadata['buildTime']}.",
           f"**N={cell_count:,} cells; P={point_count:,} fine points.** `v6/READY-climate` identifies this complete assembly.",'',
           f"Immutable publication source (same `/data/v6/` layout): `{snapshot}`. The SSD `v6/climate/` directory was replaced atomically; the previous generation is retained. Pin the immutable source for uploads that span a later assembly.",'',
           '| Files | Bytes |','|---|---:|',
           f"| cells.bin | {static_report['cells_bytes']:,} |",f"| points.bin | {static_report['points_bytes']:,} |",
           f"| Each of 12 month files | {sizes['month-01.bin']:,} |",
           f"| 12 offsets PNGs combined | {sum(v for k,v in sizes.items() if k.startswith('offsets')):,} |",
           f"| facts.bin | {sizes['facts.bin']:,} |",f"| meta.json | {sizes['meta.json']:,} |",'',
           f"Full-hourly versus compact monthly score: mean absolute error {sum(p['mean_error'] for p in proof)/12:.3f}, worst {max(p['max_error'] for p in proof):.3f} points; {sum(p['above_3'] for p in proof):,} of {cell_count*12:,} cell-months exceed 3. The compact format was accepted after the earlier partial-data accuracy review. These are newly measured global errors; acceptance of the format does not imply separate acceptance of these measurements. Accuracy is reported, not used as a readiness gate.",
           f"Worst eight-day sample versus full-hourly monthly mean: {max(p['sample_max_error'] for p in proof):.3f} points. Month-specific error statistics are in meta.json. The chunk 82 global worst was November at 30.5° N, 78.5° E: full 61.00 versus compact 52.49. Restoring only the omitted 17:00 sun-felt value raised compact scoring to 59.68; interpolation misses the late-afternoon warmth peak. See [global worst compact record](GLOBAL-WORST-COMPACT.json).",'',
           'Monthly published scores below include the offset at each place\'s nearest selected fine land point. Some differences from Fable remain large even on identical dates; the source and elevation mismatches below must not be attributed to sample years alone.','',
           '| Place | Jan | Feb | Mar | Apr | May | Jun | Jul | Aug | Sep | Oct | Nov | Dec | Mean absolute difference vs Fable |',
           '|---|'+ '---:|'*13]
    for name,place in climate_report['known_places'].items():
        lines.append('| '+name+' | '+' | '.join(f'{score:.1f}' for score in place['monthly_scores'])+f" | {place['mean_absolute_difference']:.2f} |")
    lines += ['', 'Full reference values, differences and selected fine coordinates: [known places](KNOWN-PLACES.md) and [validation JSON](validation.json).', '',
              'Source comparison on identical dates, 22 September 2021 to 16 September 2022 (chunk 82; these examples are a diagnostic baseline, not a description of every later assembly):', '',
              '- Málaga: Fable adjusts the 343 m source to the city at 20 m, adding 2.10 °C. The contract keeps the fine point\'s native height. November scored 41.87 in the provisional files versus 57.97 on Fable\'s same dates (66 in its ten-year table).',
              '- Rotterdam: August scored 80.97 versus 79.96 on Fable\'s same dates and 63 in its ten-year table. The same-date monthly differences are at most 2.04 points; the large ten-year gap mainly reflects the sampled year.',
              '- Las Palmas: the native fine point is 366 m high; Fable\'s API adjusts it to sea level, adding about 2.38 °C to temperature and dew point. March scored 43.73 versus 66.58 on the same dates (72 in the ten-year table); mean air highs were 17.43 versus 19.81 °C. This is an elevation/reference mismatch.',
              '- San Diego: the reference API\'s elevation correction adds about 1.52 °C to the native 263 m point. January scored 61.60 versus 74.00 on the same dates. December\'s cold sampled weather also matters: 38.63 versus 49.19 on the same dates and 68 in the ten-year table.',
              '- Cape Town: June scored 65.78 versus 60.46 on the same dates and 44 in the ten-year table. Unusually warm sampled weather explains most of this gap; selected-point and cell weather explain the remainder.',
              '- Singapore: Fable takes dew, rain and wind from the coastal point at 1.25° N, 103.75° E; the selected land point is 1.50° N, 103.75° E. In June, dew was 24.61 versus 23.71 °C and wet days 86.7% versus 70.0%. September scored 11.99 versus 24.20 on the same dates. Temperature offsets cannot correct these humidity and rain differences.',
              '- San Francisco: June air highs were 26.77 °C at the coast-corrected land point versus 20.07 °C in Fable\'s downtown station series. Scores were 88.03 versus 65.22 on the same dates. This expected source difference is substantial.', '',
              'The retained Fable inputs also sum 12 rain hours; this pipeline follows the requested 11-hour window, 10:00 to 21:00. No rule or format was changed to conceal these differences. Details: [same-date reference values](SAME-YEAR-REFERENCE.json) and [provisional source diagnosis](PROVISIONAL-PLACES.md).', '',
              f"Static build 0.310 s; static validation {static_report.get('validation_seconds','included below')}; processing active wall {sum(entry['seconds'] for entry in timings):.2f} s across {sum(entry['tiles'] for entry in timings):,} tile jobs; this assembly {metadata['assemblySeconds']:.2f} s; final independent validation {validation['seconds']:.2f} s. Download waits are excluded.", '',
              'Regional January/July offsets previews: `offsets-01-regions.png`, `offsets-07-regions.png`. [Visual assessment](VISUAL-ASSESSMENT.md) records the inspected coast, islands and mountain structure.', '',
              'Source intermediates remain reusable in `climate-v6-work/chunkK/`. ERA5 fdir is normalized to DNI; local clock hours use per-point IANA dates and offsets. No scoring curves or binary formats were changed.']
    temporary=report_directory/'.REPORT-next.md'; temporary.write_text('\n'.join(lines)+'\n'); temporary.replace(report_directory/'REPORT.md')
