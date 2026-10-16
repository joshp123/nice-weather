# Written by AI on 2026.10.17 by model claude opus 5.5. Research only.
# Exports a compact per-day summary for every test place (2016 to 2025) for the slider page,
# and the nice-day counts the Python rule gives at Josh's default taste, to check the page against.
import base64, importlib.util, json, pathlib, zlib
import numpy as np
O = pathlib.Path('/tmp/nwf-agents/round3/opus'); D2 = pathlib.Path('/tmp/nwf-agents/round3/data2'); A = pathlib.Path('/tmp/nwf-agents/round3/data/analysis')
spec = importlib.util.spec_from_file_location('v', O/'v3soft.py'); v = importlib.util.module_from_spec(spec); spec.loader.exec_module(v)
KEY = [0, 3, 6, 9, 11]  # 10, 13, 16, 19 and 21 h
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

def day_summary(T, dew, ws_kmh, dni, rain, months):
    ws = ws_kmh / 3.6; dew = np.minimum(dew, T); e = 6.105 * np.exp(17.27 * dew / (237.7 + dew))
    shade = T + 0.33 * e - 0.70 * ws - 4; sun = shade + 0.70 * (0.15 * dni) / (ws + 10)
    q = lambda x, lo, step: np.clip(np.floor((x - lo) / step), 0, 255).astype(np.uint8)  # read back as the bin centre
    cols = [q(sun[:, k], -25, 1/3) for k in KEY] + [q(shade[:, k], -25, 1/3) for k in KEY]
    cols += [q(dew.mean(1), -25, 1/3), np.clip(np.round(np.sqrt(rain) * 50), 0, 255).astype(np.uint8), q(ws_kmh.mean(1), 0, 0.5), (dni >= 120).sum(1).astype(np.uint8), months.astype(np.uint8)]
    return np.stack(cols, 1)  # 15 bytes a day

def nice_days_from_summary(day):
    f = day.astype(float) + 0.5; f[:, 11] -= 0.5; sun = f[:, 0:5] / 3 - 25; shade = f[:, 5:10] / 3 - 25
    hours = np.arange(12); interp = lambda a: np.stack([np.interp(hours, KEY, r) for r in a])
    sun, shade = interp(sun), interp(shade)
    dew = f[:, 10] / 3 - 25; rain = (f[:, 11] / 50) ** 2; wind = f[:, 12] / 2
    comfort = np.minimum(np.clip(1 - (v.WARM - sun) / v.COLD_RAMP, 0, 1), np.clip(1 - (shade - v.HOT) / v.HOT_RAMP, 0, 1)).mean(1) * 100
    s = comfort * v.rain_factor(rain) * v.wind_factor(wind) * v.muggy_factor(dew)
    s = np.where(shade[:, -1] < v.EVENING_MIN, np.minimum(s, 54), s)
    return round(float((s >= 55).sum()) / (len(day) / 365.25))

places = []
labels = {'Cape Town': 'Cape Town', 'Santa Cruz de Tenerife': 'Santa Cruz, north-east Tenerife', 'Marrakech': 'Marrakech', 'Málaga': 'Málaga', 'Seville': 'Seville', 'Bangkok': 'Bangkok',
          'Singapore': 'Singapore', 'Chania': 'Chania, Crete', 'Lisbon': 'Lisbon', 'London': 'London', 'Rotterdam': 'Rotterdam', 'Denver': 'Denver'}
for name, label in labels.items():
    places.append((label, day_summary(*corrected_inputs(name))))
for slug, label, tz in (('san_diego', 'San Diego', 'America/Los_Angeles'), ('los_angeles', 'Los Angeles', 'America/Los_Angeles')):
    places.append((label, day_summary(*open_meteo_inputs(O/f'{slug}_hourly.json', tz))))
places.append(('Austin', day_summary(*open_meteo_inputs(O/'candidates/austin.json', 'America/Chicago'))))
nicer = {'san_francisco': 'San Francisco', 'funchal': 'Funchal, Madeira', 'las_palmas': 'Las Palmas, Gran Canaria', 'los_cristianos': 'Los Cristianos, south Tenerife',
         'mindelo': 'Mindelo, Cape Verde', 'kunming': 'Kunming, China', 'medellin': 'Medellín, Colombia', 'nairobi': 'Nairobi, Kenya', 'perth': 'Perth', 'valletta': 'Valletta, Malta',
         'faro': 'Faro, Algarve', 'limassol': 'Limassol, Cyprus', 'puerto_vallarta': 'Puerto Vallarta, Mexico', 'guadalajara': 'Guadalajara, Mexico', 'honolulu': 'Honolulu', 'sydney': 'Sydney'}
for line in open(O/'candidates/places.tsv'):
    slug, lat, lon, tz = line.strip().split('\t')
    places.append((nicer[slug], day_summary(*open_meteo_inputs(O/'candidates'/f'{slug}.json', tz))))

blob = b''.join(np.ascontiguousarray(d.T).tobytes() for _, d in places)  # column by column: compresses better
payload = {'places': [{'name': n, 'days': int(len(d))} for n, d in places], 'data': base64.b64encode(zlib.compress(blob, 9)).decode()}
json.dump(payload, open(O/'slider_data.json', 'w'))
reference = {n: nice_days_from_summary(d) for n, d in places}
json.dump(reference, open(O/'slider_reference.json', 'w'), ensure_ascii=False, indent=1)
print('places', len(places), 'raw bytes', len(blob), 'compressed base64', len(payload['data']))
for n, c in sorted(reference.items(), key=lambda kv: -kv[1]): print(f'{n:34s} {c}')
