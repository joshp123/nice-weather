# Written by AI on 2026.10.17 by model gpt-6.1-sol.
"""Compare check_browser.py's weighted published samples with nice_v6.py's actual curves.
Run through the project environment with numpy, passing one or more *-reference.json files.
"""
import importlib.util, json, pathlib, sys
import numpy as np
reference_path = pathlib.Path(__file__).resolve().parents[2] / 'research/taste/nice_v6.py'
spec = importlib.util.spec_from_file_location('nice_v6', reference_path)
reference = importlib.util.module_from_spec(spec)
spec.loader.exec_module(reference)


def judge(samples, taste):
    raw = np.array([sample['record'] for sample in samples], float)
    offsets = np.array([sample['offset'] for sample in samples])[:, None]
    weights = np.array([sample['weight'] for sample in samples])
    sun = np.array([np.interp(np.arange(11), [0, 2, 4, 6, 8, 10], row[:6] / 3 - 30) for row in raw]) + offsets
    shade = np.array([np.interp(np.arange(12), [2, 5, 8, 11], row[6:10] / 3 - 30) for row in raw]) + offsets
    daylight = np.maximum(raw[:, 14], 1)
    hourly = np.minimum(reference.COLD_GRADE(sun - taste['warmShift']), reference.HEAT_GRADE(shade[:, :11] + taste['heatShift']))
    daytime = (hourly * (np.arange(11)[None, :] < daylight[:, None])).sum(axis=1)
    evening = np.minimum(reference.EVENING_GRADE(shade[:, 11] - taste['eveningShift']), reference.HEAT_GRADE(shade[:, 11] + taste['heatShift']))
    parts = dict(
        warmth=(daytime + reference.EVENING_HOURS_WORTH * evening) / (daylight + reference.EVENING_HOURS_WORTH),
        evening=evening,
        rainFactor=reference.RAIN_FACTOR((raw[:, 11] / 50) ** 2 * taste['rainScale']),
        windFactor=reference.WIND_FACTOR(raw[:, 12] / 2 - taste['windShift']),
        humidityFactor=reference.HUMIDITY_FACTOR(raw[:, 10] / 3 - 30 - taste['humidityShift']),
        sunFactor=reference.SUN_FACTOR(raw[:, 13] / daylight),
    )
    score = parts['warmth'] * parts['rainFactor'] * parts['windFactor'] * parts['humidityFactor'] * parts['sunFactor']
    return dict(meanScore=float(score @ weights), niceDayShare=float((score >= reference.NICE) @ weights), parts={name:float(value @ weights) for name,value in parts.items()})


for filename in sys.argv[1:]:
    for place in json.loads(pathlib.Path(filename).read_text()):
        expected = judge(place['samples'], place['taste'])
        actual = place['verdict']
        errors = [abs(expected[key] - actual[key]) for key in ['meanScore', 'niceDayShare']]
        errors += [abs(expected['parts'][key] - actual['parts'][key]) for key in expected['parts']]
        assert max(errors) < 1e-9, (place['place'], expected, actual)
        print(f"{pathlib.Path(filename).name} {place['place']}: Python {expected['meanScore']:.9f}, engine {actual['meanScore']:.9f}; max part error {max(errors):.3g}")
