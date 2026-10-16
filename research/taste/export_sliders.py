# Written by AI on 2026.10.17 by model claude opus 5.5. Research only.
# Exports a compact per-day summary for every test place (2016 to 2025) for the slider page,
# and the nice-day counts the Python rule gives at Josh's default taste, to check the page against.
import base64, importlib.util, json, pathlib, zlib
import numpy as np
O = pathlib.Path('/tmp/nwf-agents/round3/opus'); D2 = pathlib.Path('/tmp/nwf-agents/round3/data2'); A = pathlib.Path('/tmp/nwf-agents/round3/data/analysis')
spec = importlib.util.spec_from_file_location('v', O/'v3soft.py'); v = importlib.util.module_from_spec(spec); spec.loader.exec_module(v)
SUN_HOURS = [0, 1, 2, 3, 5, 7, 9, 11]  # 10, 11, 12, 13, 15, 17, 19 and 21 h: cold is judged in the sun, mostly in the morning
SHADE_HOURS = [0, 3, 6, 9, 11]  # 10, 13, 16, 19 and 21 h: heat is judged in the shade; 21 h is the walk home
DEW, RAIN, WIND, DAYLIGHT_COLUMN, MONTH = 13, 14, 15, 16, 17  # columns after the felt temperatures
YEARS = (2016, 2025)

def open_meteo_inputs(path, tz):
    h = json.load(open(path))['hourly']; a = {k: np.array([np.nan if x is None else x for x in h[k]], float) for k in h if k != 'time'}
    ix, months = v.local_index(h['time'][0], len(h['time']), tz, YEARS)
    return a['temperature_2m'][ix], a['dew_point_2m'][ix], a['wind_speed_10m'][ix], np.nan_to_num(a['direct_normal_irradiance'][ix + 1]), np.nan_to_num(a['precipitation'][ix + 1]).sum(1), months

def corrected_inputs(name):
    slug = name.lower().replace(' ', '_')
    b = np.load(D2/f'{slug}_base.npz'); t = np.load(D2/'temperature'/f'{slug}_temperature_methods.npz')
    raw = json.load(open(A/f'{slug}_hourly.json'))['hourly']; sn = np.load(D2/'sun'/f'{slug}_physical_ratio.npz')
    rawT = np.array(raw['temperature_2m'], float)
    ix = np.concatenate([b['indices'], b['indices'][:, -1:] + 1], 1)[:, :12]
    T = np.concatenate([t['b_pure'], (rawT[ix[:, -1]] + t['b_pure'][:, -1] - rawT[b['indices'][:, -1]])[:, None]], 1)[:, :12]
    dni = np.nan_to_num(sn['clear_direct_normal'][ix + 1]) * np.clip(np.nan_to_num(sn['sun_hours_ratio'][ix + 1]), 0, 1)
    keep = np.array([int(d[:4]) >= YEARS[0] for d in b['dates']])
    rain = np.array(raw['precipitation'], float)[ix + 1].sum(1)
    return (T[keep], np.array(raw['dew_point_2m'], float)[ix][keep], np.array(raw['wind_speed_10m'], float)[ix][keep], dni[keep], rain[keep], b['months'][keep])

import pickle
WHERE, DAYLIGHT = pickle.load(open(O/'daylight.pkl', 'rb'))

def station_temperatures(path, tz, station_file):
    """ERA5's hourly shape, stretched to the station's measured daily high and low (days without both keep ERA5)."""
    import calendar, datetime as dt, zoneinfo
    h = json.load(open(path))['hourly']; t = np.array(h['temperature_2m'], float); zone = zoneinfo.ZoneInfo(tz)
    obs = {}
    for line in open(station_file).read().splitlines():
        year, month, element = int(line[11:15]), int(line[15:17]), line[17:21]
        if not YEARS[0] <= year <= YEARS[1] or element not in ('TMAX', 'TMIN'): continue
        for day in range(1, calendar.monthrange(year, month)[1] + 1):
            value = line[21 + (day - 1) * 8:29 + (day - 1) * 8]
            if int(value[:5]) != -9999 and value[6] == ' ': obs.setdefault(dt.date(year, month, day), {})[element] = int(value[:5]) / 10
    ix, months = v.local_index(h['time'][0], len(t), tz, YEARS)
    epoch = dt.datetime.fromisoformat(h['time'][0]).replace(tzinfo=dt.timezone.utc)
    T = t[ix].copy()
    for row, i in enumerate(ix):
        day = (epoch + dt.timedelta(hours=int(i[0]))).astimezone(zone).date(); o = obs.get(day, {})
        if 'TMAX' not in o or 'TMIN' not in o or o['TMAX'] <= o['TMIN']: continue
        start = int((dt.datetime.combine(day, dt.time(), zone).astimezone(dt.timezone.utc) - epoch).total_seconds() / 3600)
        lo, hi = t[start:start + 24].min(), t[start:start + 24].max()
        T[row] = o['TMIN'] + (t[i] - lo) / (hi - lo) * (o['TMAX'] - o['TMIN'])
    return T

def day_summary(T, dew, ws_kmh, dni, rain, months, daylight):
    ws = ws_kmh / 3.6; dew = np.minimum(dew, T); e = 6.105 * np.exp(17.27 * dew / (237.7 + dew))
    shade = T + 0.33 * e - 0.70 * ws - 4; sun = shade + 0.70 * (0.15 * dni) / (ws + 10)
    q = lambda x, lo, step: np.clip(np.floor((x - lo) / step), 0, 255).astype(np.uint8)  # read back as the bin centre
    cols = [q(sun[:, k], -25, 1/3) for k in SUN_HOURS] + [q(shade[:, k], -25, 1/3) for k in SHADE_HOURS]
    cols += [q(dew.mean(1), -25, 1/3), np.clip(np.round(np.sqrt(rain) * 50), 0, 255).astype(np.uint8), q(ws_kmh.mean(1), 0, 0.5), daylight[:len(months)].astype(np.uint8), months.astype(np.uint8)]
    return np.stack(cols, 1)  # 18 bytes a day

places = []
labels = {'Cape Town': 'Cape Town', 'Santa Cruz de Tenerife': 'Santa Cruz, north-east Tenerife', 'Marrakech': 'Marrakech', 'Málaga': 'Málaga', 'Seville': 'Seville', 'Bangkok': 'Bangkok',
          'Singapore': 'Singapore', 'Chania': 'Chania, Crete', 'Lisbon': 'Lisbon', 'London': 'London', 'Rotterdam': 'Rotterdam', 'Denver': 'Denver'}
short = {'Santa Cruz de Tenerife': 'Santa Cruz'}
for name, label in labels.items():
    places.append((label, day_summary(*corrected_inputs(name), DAYLIGHT[short.get(name, name)])))
for slug, label, tz in (('san_diego', 'San Diego', 'America/Los_Angeles'), ('los_angeles', 'Los Angeles', 'America/Los_Angeles')):
    places.append((label, day_summary(*open_meteo_inputs(O/f'{slug}_hourly.json', tz), DAYLIGHT[label])))
places.append(('Austin', day_summary(*open_meteo_inputs(O/'candidates/austin.json', 'America/Chicago'), DAYLIGHT['Austin'])))
nicer = {'san_francisco': 'San Francisco, downtown station', 'funchal': 'Funchal, Madeira', 'las_palmas': 'Las Palmas, Gran Canaria', 'los_cristianos': 'Los Cristianos, south Tenerife',
         'mindelo': 'Mindelo, Cape Verde', 'kunming': 'Kunming, China', 'medellin': 'Medellín, Colombia', 'nairobi': 'Nairobi, Kenya', 'perth': 'Perth', 'valletta': 'Valletta, Malta',
         'faro': 'Faro, Algarve', 'limassol': 'Limassol, Cyprus', 'puerto_vallarta': 'Puerto Vallarta, Mexico', 'guadalajara': 'Guadalajara, Mexico', 'honolulu': 'Honolulu', 'sydney': 'Sydney'}
for line in open(O/'candidates/places.tsv'):
    slug, lat, lon, tz = line.strip().split('\t')
    inputs = list(open_meteo_inputs(O/'candidates'/f'{slug}.json', tz))
    if slug == 'san_francisco': inputs[0] = station_temperatures(O/'candidates/san_francisco.json', tz, '/tmp/nwf-agents/round3/bayarea/sf.dly')
    places.append((nicer[slug], day_summary(*inputs, DAYLIGHT[slug.replace('_', ' ').title()])))

blob = b''.join(np.ascontiguousarray(d.T).tobytes() for _, d in places)  # column by column: compresses better
payload = {'sunHours': SUN_HOURS, 'shadeHours': SHADE_HOURS, 'places': [{'name': n, 'days': int(len(d))} for n, d in places], 'data': base64.b64encode(zlib.compress(blob, 9)).decode()}
json.dump(payload, open(O/'slider_data.json', 'w'))
print('places', len(places), 'raw bytes', len(blob), 'compressed base64', len(payload['data']))
