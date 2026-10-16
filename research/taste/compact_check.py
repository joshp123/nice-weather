# Written by AI on 2026.10.17 by model claude opus 5.5. Research only.
# Can a compact day summary (felt at 10, 13, 16, 19, 21 h; dew; rain; wind) reproduce the full hourly nice-day counts?
import importlib.util, json, pathlib
import numpy as np
O = pathlib.Path('/tmp/nwf-agents/round3/opus'); D2 = pathlib.Path('/tmp/nwf-agents/round3/data2'); A = pathlib.Path('/tmp/nwf-agents/round3/data/analysis')
spec = importlib.util.spec_from_file_location('v', O/'v3soft.py'); v = importlib.util.module_from_spec(spec); spec.loader.exec_module(v)
def inputs_open_meteo(path, tz, years):
    h = json.load(open(path))['hourly']; a = {k: np.array([np.nan if x is None else x for x in h[k]], float) for k in h if k != 'time'}
    ix, m = v.local_index(h['time'][0], len(h['time']), tz, years)
    return a['temperature_2m'][ix], a['dew_point_2m'][ix], a['wind_speed_10m'][ix], np.nan_to_num(a['direct_normal_irradiance'][ix + 1]), np.nan_to_num(a['precipitation'][ix + 1]).sum(1), m
def felt(T, dew, ws_kmh, dni):
    ws = ws_kmh / 3.6; dew = np.minimum(dew, T); e = 6.105 * np.exp(17.27 * dew / (237.7 + dew))
    shade = T + 0.33 * e - 0.70 * ws - 4; return shade + 0.70 * (0.15 * dni) / (ws + 10), shade, dew
def score_from(sun, shade, dew, ws_kmh, rain):
    cold_ok = np.clip(1 - (v.WARM - sun) / v.COLD_RAMP, 0, 1); hot_ok = np.clip(1 - (shade - v.HOT) / v.HOT_RAMP, 0, 1)
    s = np.minimum(cold_ok, hot_ok).mean(1) * 100 * v.rain_factor(rain) * v.wind_factor(ws_kmh.mean(1) if ws_kmh.ndim > 1 else ws_kmh) * v.muggy_factor(dew.mean(1) if dew.ndim > 1 else dew)
    return np.where(shade[:, -1] < v.EVENING_MIN, np.minimum(s, 54), s)
KEY = [0, 3, 6, 9, 11]  # 10, 13, 16, 19, 21 h
def compact(arr):  # rebuild 12 hourly values from 5 stored ones by straight lines
    hours = np.arange(12); return np.stack([np.interp(hours, KEY, row) for row in arr[:, KEY]])
rows = []
places = [(l.split('\t')[0], O/'candidates'/f"{l.split('\t')[0]}.json", l.strip().split('\t')[3], (2016, 2025)) for l in open(O/'candidates/places.tsv')]
places += [('san_diego', O/'san_diego_hourly.json', 'America/Los_Angeles', (2006, 2025)), ('los_angeles', O/'los_angeles_hourly.json', 'America/Los_Angeles', (2006, 2025)), ('austin', O/'candidates/austin.json', 'America/Chicago', (2006, 2025))]
for name, path, tz, years in places:
    T, dew, ws, dni, rain, m = inputs_open_meteo(path, tz, years)
    sun, shade, dew = felt(T, dew, ws, dni)
    full = score_from(sun, shade, dew, ws, rain) >= 55
    small = score_from(compact(sun), compact(shade), dew.mean(1), ws.mean(1), rain) >= 55
    yrs = len(m) / 365.25
    rows.append((name, round(full.sum() / yrs), round(small.sum() / yrs), round(100 * float(np.mean(full == small)))))
for r in rows: print(f"{r[0]:16s} full {r[1]:3}  compact {r[2]:3}  same verdict on {r[3]}% of days")
d = np.array([abs(r[1] - r[2]) for r in rows]); print('largest difference in nice days a year:', d.max(), ' mean:', round(float(d.mean()), 1))
