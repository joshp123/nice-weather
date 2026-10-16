# Written by AI on 2026.10.17 by model claude opus 5.5. Research only: v5 against Josh's anchors.
import datetime as dt, importlib.util, json, pathlib, pickle, numpy as np
O = pathlib.Path('/tmp/nwf-agents/round3/opus'); A = pathlib.Path('/tmp/nwf-agents/round3/data/analysis')
s = importlib.util.spec_from_file_location('v5', O/'v5.py'); v5 = importlib.util.module_from_spec(s); s.loader.exec_module(v5)
inputs = pickle.load(open(O/'inputs_2016_2025.pkl', 'rb'))
where = {l.split('\t')[0].replace('_', ' ').title(): (float(l.split('\t')[1]), float(l.split('\t')[2]), l.strip().split('\t')[3]) for l in open(O/'candidates/places.tsv')}
corr = {'Cape Town': ('cape_town', 'Africa/Johannesburg'), 'Santa Cruz': ('santa_cruz_de_tenerife', 'Atlantic/Canary'), 'Marrakech': ('marrakech', 'Africa/Casablanca'), 'Málaga': ('málaga', 'Europe/Madrid'),
        'Seville': ('seville', 'Europe/Madrid'), 'Bangkok': ('bangkok', 'Asia/Bangkok'), 'Singapore': ('singapore', 'Asia/Singapore'), 'Chania': ('chania', 'Europe/Athens'), 'Lisbon': ('lisbon', 'Europe/Lisbon'),
        'London': ('london', 'Europe/London'), 'Rotterdam': ('rotterdam', 'Europe/Amsterdam'), 'Denver': ('denver', 'America/Denver')}
for n, (slug, tz) in corr.items():
    j = json.load(open(A/f'{slug}_hourly.json')); where[n] = (j['latitude'], j['longitude'], tz)
for n, f, tz in (('San Diego', 'san_diego_hourly.json', 'America/Los_Angeles'), ('Los Angeles', 'los_angeles_hourly.json', 'America/Los_Angeles'), ('Austin', 'candidates/austin.json', 'America/Chicago')):
    j = json.load(open(O/f)); where[n] = (j['latitude'], j['longitude'], tz)
days = [dt.date(2016, 1, 1) + dt.timedelta(i) for i in range(3653)]
daylight = {}
for n, (lat, lon, tz) in where.items():
    m = inputs[n][5]; dl = v5.daylight_hours(days, lat, lon, tz)
    daylight[n] = dl[:len(m)] if len(dl) >= len(m) else dl  # inputs start 2016-01-01, one row per local day
pickle.dump((where, daylight), open(O/'daylight.pkl', 'wb'))
print('SD daylight hours Jan/Jun:', daylight['San Diego'][:31].mean().round(1), daylight['San Diego'][160:170].mean().round(1), ' Rotterdam Dec', daylight['Rotterdam'][335:365].mean().round(1))
cache = {}
for n in inputs:
    T, dew, ws, dni, rain, m = inputs[n]; sun, shade, d = v5.felt(T, dew, ws, dni); cache[n] = (sun, shade, daylight[n][:len(m)], d.mean(1), rain, ws.mean(1), m)
def nice(n, months=None, **t):
    sun, shade, dl, dew, rain, wind, m = cache[n]; ok = v5.score_from_felt(sun, shade, dl, dew, rain, wind, t) >= 55; yrs = len(m) / 365.25
    return round(ok.sum() / yrs) if months is None else round(100 * ok[np.isin(m, months)].mean())
DJF = [12, 1, 2]
for cf, hf in ((0, 3), (1, 3), (0, 2), (0, 4)):
    v5.COLD_FADE, v5.HEAT_FADE = cf, hf
    print(f'\ncold fade {cf} heat fade {hf}: SD year {nice("San Diego")} DJF {nice("San Diego", DJF)}%  LA {nice("Los Angeles")}  Malaga Nov {nice("Málaga", [11])}%  Seville Jul {nice("Seville", [7])}%  Singapore heat only {nice("Singapore", humidity=None, rain=None, wind=None, evening=None)}')
    print('  Dec-Feb %:', {k: nice(k, DJF) for k in ['Cape Town', 'Los Cristianos', 'Santa Cruz', 'Marrakech', 'Seville', 'Málaga', 'Bangkok', 'Chania', 'Rotterdam']})
v5.COLD_FADE, v5.HEAT_FADE = 0, 3
print('\nevening sweep (SD DJF %, SD year, Tenerife S DJF %, Cape Town DJF %):')
for e in (None, 8, 10, 12, 14, 16):
    print(' ', e, nice('San Diego', DJF, evening=e), nice('San Diego', evening=e), nice('Los Cristianos', DJF, evening=e), nice('Cape Town', DJF, evening=e))
print('\nall places, defaults:'); 
for n in sorted(inputs, key=lambda n: -nice(n)): print(f'  {n:16s} {nice(n):3d}  DJF {nice(n, DJF):3d}%', [nice(n, [k]) for k in range(1, 13)])
