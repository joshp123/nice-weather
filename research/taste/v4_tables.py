# Written by AI on 2026.10.17 by model claude opus 5.5. Research only.
import importlib.util, pathlib, pickle, numpy as np
O = pathlib.Path('/tmp/nwf-agents/round3/opus')
s = importlib.util.spec_from_file_location('v4', O/'v4.py'); v4 = importlib.util.module_from_spec(s); s.loader.exec_module(v4)
inputs = pickle.load(open(O/'inputs_2016_2025.pkl', 'rb'))
cache = {}
def prep(n):
    if n not in cache:
        T, dew, ws, dni, rain, m = inputs[n]; sun, shade, dewc = v4.felt(T, dew, ws, dni); cache[n] = (sun, shade, dewc.mean(1), rain, ws.mean(1), m)
    return cache[n]
def nice(n, months=None, **taste):
    sun, shade, dew, rain, wind, m = prep(n); ok = v4.score_from_felt(sun, shade, dew, rain, wind, taste) >= 55; yrs = len(m) / 365.25
    if months is None: return round(ok.sum() / yrs)
    return round(100 * ok[np.isin(m, months)].mean())
DJF = [12, 1, 2]
print('Singapore nice days a year: humidity limit (rows) x too-hot limit (columns 30, 34, 37 felt in shade)')
for h in (None, 26, 25, 24, 23, 22, 20, 19):
    print(f'  dew <= {str(h):4s}', [nice('Singapore', humidity=h, high=hi, evening=None, rain=None, wind=None) for hi in (30, 34, 37)], ' with your other defaults:', [nice('Singapore', humidity=h, high=hi) for hi in (30, 34, 37)])
print('Bangkok same:'); 
for h in (None, 24, 22, 20, 19): print(f'  dew <= {str(h):4s}', [nice('Bangkok', humidity=h, high=hi) for hi in (30, 34, 37)])
print('\nEvening limit: SD winter %, SD year, LA winter %, S Tenerife winter %, Cape Town winter %, Malaga Nov %, Rotterdam year')
for e in (None, 10, 12, 13, 14, 15, 16, 18):
    print(f'  9pm >= {str(e):4s}', nice('San Diego', DJF, evening=e), nice('San Diego', evening=e), nice('Los Angeles', DJF, evening=e), nice('Los Cristianos', DJF, evening=e), nice('Cape Town', DJF, evening=e), nice('Málaga', [11], evening=e), nice('Rotterdam', evening=e))
print('\nHumidity limit: SD Aug %, Malaga Aug %, Honolulu year, Honolulu Aug %, Austin Oct %, Bangkok DJF %, Las Palmas Aug %, Puerto Vallarta year')
for h in (17, 18, 19, 20, 21):
    print(f'  dew <= {h}', nice('San Diego', [8], humidity=h), nice('Málaga', [8], humidity=h), nice('Honolulu', humidity=h), nice('Honolulu', [8], humidity=h), nice('Austin', [10], humidity=h), nice('Bangkok', DJF, humidity=h), nice('Las Palmas', [8], humidity=h), nice('Puerto Vallarta', humidity=h))
print('\nRain limit (mm, 10:00-21:00): London Jul %, Rotterdam year, Malaga Jan %, SF year, Funchal year, Lisbon Nov %')
for r in (0.5, 1, 2, 3, 5):
    print(f'  rain <= {r}', nice('London', [7], rain=r), nice('Rotterdam', rain=r), nice('Málaga', [1], rain=r), nice('San Francisco', rain=r), nice('Funchal', rain=r), nice('Lisbon', [11], rain=r))
print('\nWind limit (km/h daytime mean): Cape Town DJF %, Cape Town year, Chania Apr %, Las Palmas year, Mindelo year')
for w in (25, 30, 35, 40, None):
    print(f'  wind <= {w}', nice('Cape Town', DJF, wind=w), nice('Cape Town', wind=w), nice('Chania', [4], wind=w), nice('Las Palmas', wind=w), nice('Mindelo', wind=w))
print('\nDefaults: year, winter (DJF %) for all places')
rows = sorted(inputs, key=lambda n: -nice(n))
for n in rows: print(f'  {n:16s} year {nice(n):3d}  DJF {nice(n, DJF):3d}%  months', [nice(n, [m]) for m in range(1, 13)])
