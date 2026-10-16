# Written by AI on 2026.10.17 by model claude opus 5.5. Research only: how fast cold and heat fade out.
import importlib.util, pathlib, pickle, numpy as np
O = pathlib.Path('/tmp/nwf-agents/round3/opus')
s = importlib.util.spec_from_file_location('v4', O/'v4.py'); v4 = importlib.util.module_from_spec(s); s.loader.exec_module(v4)
inputs = pickle.load(open(O/'inputs_2016_2025.pkl', 'rb')); cache = {}
for n in inputs:
    T, dew, ws, dni, rain, m = inputs[n]; sun, shade, dewc = v4.felt(T, dew, ws, dni); cache[n] = (sun, shade, dewc.mean(1), rain, ws.mean(1), m)
TASTE = dict(humidity=20.0, rain=1.0, wind=35.0, evening=12.0)
def nice(n, months=None, **t):
    sun, shade, dew, rain, wind, m = cache[n]; ok = v4.score_from_felt(sun, shade, dew, rain, wind, {**TASTE, **t}) >= 55; yrs = len(m) / 365.25
    return round(ok.sum() / yrs) if months is None else round(100 * ok[np.isin(m, months)].mean())
for cold, hot in ((5, 10), (4, 6), (4, 5), (3, 5), (3, 4)):
    v4.COLD_RAMP, v4.HOT_RAMP = cold, hot
    print(f'cold fade {cold}, heat fade {hot}: Seville Jul {nice("Seville", [7])}%  Marrakech Jul {nice("Marrakech", [7])}%  Austin Aug {nice("Austin", [8])}%  Singapore no humidity limit {nice("Singapore", humidity=None)}  '
          f'Malaga Nov {nice("Málaga", [11])}%  SD Jan {nice("San Diego", [1])}%  SD year {nice("San Diego")}  Cape Town DJF {nice("Cape Town", [12, 1, 2])}%  Tenerife S DJF {nice("Los Cristianos", [12, 1, 2])}%  London Jul {nice("London", [7])}%')
v4.COLD_RAMP, v4.HOT_RAMP = 5, 10
sun, shade, *_ , m = cache['Seville']; jul = m == 7
print('Seville July felt in shade at 13, 16, 19 h (median):', [round(float(np.median(shade[jul, h])), 1) for h in (3, 6, 9)])
