# Written by AI on 2026.10.17 by model claude opus 5.5. Research only.
# Nice day v2: hour by hour from 10:00 to 21:00 (the walk home included); warm enough judged in the sun,
# too hot in the shade; cold ramps down over 5 °C, heat over 10 °C; rain and strong wind as penalties.
import datetime as dt, json, pathlib, sys, zoneinfo
import numpy as np
D2 = pathlib.Path('/tmp/nwf-agents/round3/data2'); A = pathlib.Path('/tmp/nwf-agents/round3/data/analysis'); O = pathlib.Path('/tmp/nwf-agents/round3/opus')
WARM, HOT, COLD_RAMP, HOT_RAMP = 21.0, 30.0, 5.0, 10.0
HOURS = 12  # 10:00 to 21:00 inclusive
def curve(pts):
    xs, ys = zip(*pts); return lambda v: np.interp(v, xs, ys)
rain_factor = curve([(0, 1), (.2, .95), (1, .8), (3, .5), (6, .2), (10, 0)])
wind_factor = curve([(0, 1), (25, 1), (35, .8), (45, .5), (60, 0)])
muggy_factor = curve([(16, 1), (18, .95), (20, .8), (22, .55), (24, .3), (26, .1), (28, 0)])
EVENING_MIN = 15.0
def score(T, dew, ws_kmh, dni, rain_total):
    ws = ws_kmh / 3.6; dew = np.minimum(dew, T)
    e = 6.105 * np.exp(17.27 * dew / (237.7 + dew))
    shade = T + 0.33 * e - 0.70 * ws - 4
    sun = shade + 0.70 * (0.15 * dni) / (ws + 10)
    cold_ok = np.clip(1 - (WARM - sun) / COLD_RAMP, 0, 1)
    hot_ok = np.clip(1 - (shade - HOT) / HOT_RAMP, 0, 1)
    comfort = np.minimum(cold_ok, hot_ok).mean(1) * 100
    s = comfort * rain_factor(rain_total) * wind_factor(ws_kmh.mean(1)) * muggy_factor(dew.mean(1))
    s = np.where(shade[:, -1] < EVENING_MIN, np.minimum(s, 54), s)  # the walk home needs a layer
    return s, cold_ok, hot_ok
def local_index(times0, n, tz, years):
    epoch = dt.datetime.fromisoformat(times0).replace(tzinfo=dt.timezone.utc)
    ix, months = [], []
    day = dt.date(years[0], 1, 1)
    while day < dt.date(years[1] + 1, 1, 1):
        s = dt.datetime.combine(day, dt.time(10), zoneinfo.ZoneInfo(tz)).astimezone(dt.timezone.utc)
        i = int((s - epoch).total_seconds() / 3600)
        if i >= 0 and i + HOURS + 1 < n: ix.append(list(range(i, i + HOURS))); months.append(day.month)
        day += dt.timedelta(days=1)
    return np.array(ix), np.array(months)
def from_open_meteo(path, tz, years):
    h = json.load(open(path))['hourly']
    a = {k: np.array([np.nan if v is None else v for v in h[k]], float) for k in h if k != 'time'}
    ix, months = local_index(h['time'][0], len(h['time']), tz, years)
    s, c, hh = score(a['temperature_2m'][ix], a['dew_point_2m'][ix], a['wind_speed_10m'][ix], np.nan_to_num(a['direct_normal_irradiance'][ix + 1]), np.nan_to_num(a['precipitation'][ix + 1]).sum(1))
    return s, months, c, hh, np.nan_to_num(a['precipitation'][ix + 1]).sum(1)
def from_corrected(name):
    slug = name.lower().replace(' ', '_')
    b = np.load(D2/f'{slug}_base.npz'); t = np.load(D2/'temperature'/f'{slug}_temperature_methods.npz')
    raw = json.load(open(A/f'{slug}_hourly.json'))['hourly']; sn = np.load(D2/'sun'/f'{slug}_physical_ratio.npz')
    rawT = np.array(raw['temperature_2m'], float)
    ix = np.concatenate([b['indices'], b['indices'][:, -1:] + 1], 1)[:, :HOURS]  # 10..21
    T = np.concatenate([t['b_pure'], (rawT[ix[:, -1]] + t['b_pure'][:, -1] - rawT[b['indices'][:, -1]])[:, None]], 1)[:, :HOURS]
    dni = np.nan_to_num(sn['clear_direct_normal'][ix + 1]) * np.clip(np.nan_to_num(sn['sun_hours_ratio'][ix + 1]), 0, 1)
    rain = np.array(raw['precipitation'], float)[ix + 1].sum(1)
    s, c, hh = score(T, np.array(raw['dew_point_2m'], float)[ix], np.array(raw['wind_speed_10m'], float)[ix], dni, rain)
    return s, b['months'], c, hh, rain
def summary(s, months):
    nice = s >= 55
    yrs = len(months) / 365.25
    fair = (s >= 40) & (s < 55); w = np.isin(months, [12, 1, 2])
    return dict(year=round(float(nice.sum()) / yrs), winter=round(float((nice & w).sum()) / yrs), winter_fair=round(float((fair & w).sum()) / yrs), winter_poor_or_worse=round(float(((s < 40) & w).sum()) / yrs),
                months=[round(100 * float(nice[months == m].mean())) for m in range(1, 13)])
if __name__ == '__main__':
    out = {}
    for n in ['Cape Town', 'Santa Cruz de Tenerife', 'Marrakech', 'Málaga', 'Seville', 'Bangkok', 'Singapore', 'Chania', 'Lisbon', 'London', 'Rotterdam', 'Denver']:
        s, m, *_ = from_corrected(n); out[n] = summary(s, m)
    for name, label in (('san_diego', 'San Diego'), ('los_angeles', 'Los Angeles')):
        s, m, *_ = from_open_meteo(O/f'{name}_hourly.json', 'America/Los_Angeles', (2006, 2025)); out[label] = summary(s, m)
    for line in open(O/'candidates/places.tsv'):
        name, lat, lon, tz = line.strip().split('\t'); p = O/'candidates'/f'{name}.json'
        try:
            if not json.load(open(p)).get('hourly'): continue
        except Exception: continue
        s, m, *_ = from_open_meteo(p, tz, (2016, 2025)); out[name.replace('_', ' ').title()] = summary(s, m)
    json.dump(out, open(O/'v3soft_summary.json', 'w'), ensure_ascii=False, indent=1)
    for k, v in sorted(out.items(), key=lambda kv: -kv[1]['year']):
        print(f"{k:24s} year {v['year']:3}  winter {v['winter']:3}  {v['months']}")
