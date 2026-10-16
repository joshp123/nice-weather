# Written by AI on 2026.10.17 by model claude opus 5.5. Research only: which stored hours let a compact day match the full hourly rule.
import importlib.util, pathlib, pickle, numpy as np
O = pathlib.Path('/tmp/nwf-agents/round3/opus')
s = importlib.util.spec_from_file_location('v5', O/'v5.py'); v5 = importlib.util.module_from_spec(s); s.loader.exec_module(v5)
inputs = pickle.load(open(O/'inputs_2016_2025.pkl', 'rb')); where, daylight = pickle.load(open(O/'daylight.pkl', 'rb'))
prep = {}
for n, (T, dew, ws, dni, rain, m) in inputs.items():
    sun, shade, d = v5.felt(T, dew, ws, dni); prep[n] = (sun, shade, daylight[n][:len(m)], d.mean(1), rain, ws.mean(1), m)
def count(sun, shade, dl, dew, rain, wind, m): return round(float((v5.score_from_felt(sun, shade, dl, dew, rain, wind) >= 55).sum()) / (len(m) / 365.25))
full = {n: count(*p) for n, p in prep.items()}
hours = np.arange(12)
def squeeze(a, keys): return np.stack([np.interp(hours, keys, r[keys]) for r in a])
for sun_keys, shade_keys in (([0, 3, 6, 9, 11], [0, 3, 6, 9, 11]), ([0, 1, 2, 3, 5, 7, 9, 11], [0, 3, 6, 9, 11]), (list(range(12)), [0, 3, 6, 9, 11]),
                             ([0, 1, 2, 4, 6, 8, 9, 11], [0, 3, 5, 7, 11]), (list(range(12)), [0, 2, 4, 6, 8, 11])):
    gaps = []
    for n, (sun, shade, dl, dew, rain, wind, m) in prep.items():
        gaps.append(count(squeeze(sun, sun_keys), squeeze(shade, shade_keys), dl, dew, rain, wind, m) - full[n])
    gaps = np.array(gaps); print(f'sun hours {[k + 10 for k in sun_keys]}, shade hours {[k + 10 for k in shade_keys]}: bytes {len(sun_keys) + len(shade_keys) + 5}, worst {np.abs(gaps).max()}, mean {gaps.mean():+.1f}, within 1: {(np.abs(gaps) <= 1).sum()}/31')
