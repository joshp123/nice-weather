# Written by AI on 2026.10.17 by model Claude Fable 5.1. Research only.
# A graded nice-weather method: every hour gets a warmth grade on a smooth curve; the day's thermal
# grade is the mean over daylight hours plus the walk home at 21:00; rain, wind, humidity and sun then
# scale the day down. A day scores 0 to 100. A month is summarised by its mean day score and by the
# share of days that are Nice (>= NICE) or Awful (< AWFUL).
import calendar, datetime as dt, json, pathlib, pickle, zoneinfo
import numpy as np

OPUS = pathlib.Path('/tmp/nwf-agents/round3/opus')
YEARS = (2016, 2025)
NICE, AWFUL = 55.0, 25.0
BANDS = [(85, 'Perfect'), (70, 'Beautiful'), (55, 'Nice'), (40, 'Fair'), (25, 'Poor'), (-1, 'Awful')]
EVENING_HOURS_WORTH = 2.0  # the walk home at 21:00 counts like two daytime hours


def piecewise(points):
    xs, ys = zip(*points)
    return lambda v: np.interp(v, xs, ys)


# Hourly warmth, judged in the sun when it is cool (felt-in-sun) and in the shade when it is hot.
# Cold falls faster than heat: six degrees too cold (feels 17 in the sun) scores 40; six degrees too hot (feels 34 in the shade) scores 60.
COLD_GRADE = piecewise([(9, 0), (11, 3), (13, 10), (15, 22), (17, 40), (19, 62), (21, 85), (23, 100)])
HEAT_GRADE = piecewise([(28, 100), (31, 85), (34, 60), (37, 30), (40, 10), (43, 0)])
# The walk home at 21:00 (felt in the shade; walking keeps you warm): 20 or more is best, 12 is fine.
EVENING_GRADE = piecewise([(4, 0), (8, 30), (12, 65), (16, 90), (19, 100)])
# Nuisance factors, each 1.0 when harmless.
RAIN_FACTOR = piecewise([(0, 1.0), (0.5, 0.95), (1, 0.88), (2, 0.74), (4, 0.5), (8, 0.22), (15, 0.05)])
WIND_FACTOR = piecewise([(15, 1.0), (20, 0.97), (25, 0.92), (30, 0.85), (40, 0.65), (50, 0.4), (65, 0.15)])
HUMIDITY_FACTOR = piecewise([(16, 1.0), (18, 0.95), (20, 0.85), (22, 0.65), (24, 0.4), (26, 0.2), (28, 0.1)])
SUN_FACTOR = piecewise([(0, 0.75), (0.3, 0.85), (0.6, 0.95), (0.8, 1.0)])  # share of daylight hours with sun
SUNSHINE_DNI = 300.0  # W/m2 as an hourly mean: the sun was out for most of the hour (WMO's instantaneous threshold is 120)


def felt(T, dew, wind_kmh, dni):
    """Steadman's apparent temperature (the Australian weather service's), in the shade and in the sun."""
    ws = wind_kmh / 3.6
    dew = np.minimum(dew, T)
    vapour = 6.105 * np.exp(17.27 * dew / (237.7 + dew))
    shade = T + 0.33 * vapour - 0.70 * ws - 4
    sun = shade + 0.70 * (0.15 * dni) / (ws + 10)
    return sun, shade, dew


def day_scores(T, dew, wind_kmh, dni, rain_total, daylight, taste=None):
    """Score every day 0 to 100. Arrays are (days, 12) for local 10:00 to 21:00; daylight is hours from 10:00."""
    sun, shade, dew = felt(T, dew, wind_kmh, dni)
    t = taste or {}
    cold = COLD_GRADE(sun[:, :11] - t.get('warm_shift', 0.0))
    heat = HEAT_GRADE(shade[:, :11] + t.get('heat_shift', 0.0))
    hourly = np.minimum(cold, heat)
    in_daylight = np.arange(11)[None, :] < daylight[:, None]
    daytime_sum = (hourly * in_daylight).sum(1)
    evening = np.minimum(EVENING_GRADE(shade[:, 11] - t.get('evening_shift', 0.0)), HEAT_GRADE(shade[:, 11] + t.get('heat_shift', 0.0)))
    thermal = (daytime_sum + EVENING_HOURS_WORTH * evening) / (daylight + EVENING_HOURS_WORTH)
    sunny_share = ((dni[:, :11] >= SUNSHINE_DNI) & in_daylight).sum(1) / daylight
    parts = dict(
        thermal=thermal,
        daytime=daytime_sum / daylight,
        evening=evening,
        rain=RAIN_FACTOR(rain_total * t.get('rain_scale', 1.0)),
        wind=WIND_FACTOR(wind_kmh.mean(1) - t.get('wind_shift', 0.0)),
        humidity=HUMIDITY_FACTOR(dew.mean(1) - t.get('humidity_shift', 0.0)),
        sun=SUN_FACTOR(sunny_share),
    )
    score = thermal * parts['rain'] * parts['wind'] * parts['humidity'] * parts['sun']
    return score, parts


def band(score):
    return next(name for floor, name in BANDS if score >= floor)


# --- data -------------------------------------------------------------------------------------

def local_index(first_time, n, tz, years, hours=12):
    epoch = dt.datetime.fromisoformat(first_time).replace(tzinfo=dt.timezone.utc)
    ix = []
    day = dt.date(years[0], 1, 1)
    while day < dt.date(years[1] + 1, 1, 1):
        start = dt.datetime.combine(day, dt.time(10), zoneinfo.ZoneInfo(tz)).astimezone(dt.timezone.utc)
        i = int((start - epoch).total_seconds() / 3600)
        if i >= 0 and i + hours + 1 < n:
            ix.append(list(range(i, i + hours)))
        day += dt.timedelta(days=1)
    return np.array(ix)


def station_temperatures(path, tz, station_file):
    """ERA5's hourly shape, stretched to the station's measured daily high and low (as export_sliders.py does)."""
    h = json.load(open(path))['hourly']
    t = np.array(h['temperature_2m'], float)
    zone = zoneinfo.ZoneInfo(tz)
    obs = {}
    for line in open(station_file).read().splitlines():
        year, month, element = int(line[11:15]), int(line[15:17]), line[17:21]
        if not YEARS[0] <= year <= YEARS[1] or element not in ('TMAX', 'TMIN'):
            continue
        for day in range(1, calendar.monthrange(year, month)[1] + 1):
            value = line[21 + (day - 1) * 8:29 + (day - 1) * 8]
            if int(value[:5]) != -9999 and value[6] == ' ':
                obs.setdefault(dt.date(year, month, day), {})[element] = int(value[:5]) / 10
    ix = local_index(h['time'][0], len(t), tz, YEARS)
    epoch = dt.datetime.fromisoformat(h['time'][0]).replace(tzinfo=dt.timezone.utc)
    T = t[ix].copy()
    for row, i in enumerate(ix):
        day = (epoch + dt.timedelta(hours=int(i[0]))).astimezone(zone).date()
        o = obs.get(day, {})
        if 'TMAX' not in o or 'TMIN' not in o or o['TMAX'] <= o['TMIN']:
            continue
        start = int((dt.datetime.combine(day, dt.time(), zone).astimezone(dt.timezone.utc) - epoch).total_seconds() / 3600)
        lo, hi = t[start:start + 24].min(), t[start:start + 24].max()
        T[row] = o['TMIN'] + (t[i] - lo) / (hi - lo) * (o['TMAX'] - o['TMIN'])
    return T


def load_places():
    inputs = pickle.load(open(OPUS / 'inputs_2016_2025.pkl', 'rb'))
    where, daylight = pickle.load(open(OPUS / 'daylight.pkl', 'rb'))
    places = {}
    for name, (T, dew, wind, dni, rain, months) in inputs.items():
        if name == 'San Francisco':
            T = station_temperatures(OPUS / 'candidates/san_francisco.json', 'America/Los_Angeles', '/tmp/nwf-agents/round3/bayarea/sf.dly')
        n = len(months)
        places[name] = dict(T=T[:n], dew=dew[:n], wind=wind[:n], dni=dni[:n], rain=rain[:n], months=months, daylight=daylight[name][:n].astype(float))
    return places, where


SEASONS = {'Dec-Feb': [12, 1, 2], 'Mar-May': [3, 4, 5], 'Jun-Aug': [6, 7, 8], 'Sep-Nov': [9, 10, 11]}


def summarise(score, months):
    out = {'month_mean': [float(score[months == m].mean()) for m in range(1, 13)],
           'month_nice': [float((score[months == m] >= NICE).mean() * 100) for m in range(1, 13)],
           'month_awful': [float((score[months == m] < AWFUL).mean() * 100) for m in range(1, 13)],
           'year_mean': float(score.mean()),
           'nice_days': float((score >= NICE).sum() / (len(score) / 365.25)),
           'awful_days': float((score < AWFUL).sum() / (len(score) / 365.25))}
    for s, ms in SEASONS.items():
        sel = np.isin(months, ms)
        out[s] = dict(mean=float(score[sel].mean()), nice=float((score[sel] >= NICE).mean() * 100), awful=float((score[sel] < AWFUL).mean() * 100))
    return out


def run_all(taste=None):
    places, where = load_places()
    results = {}
    for name, p in places.items():
        score, parts = day_scores(p['T'], p['dew'], p['wind'], p['dni'], p['rain'], p['daylight'], taste)
        results[name] = summarise(score, p['months'])
        results[name]['parts'] = {k: [float(v[p['months'] == m].mean()) for m in range(1, 13)] for k, v in parts.items()}
    return results, places


if __name__ == '__main__':
    results, places = run_all()
    json.dump(results, open('/tmp/nwf-agents/round3/fable/results.json', 'w'), ensure_ascii=False, indent=1)
    print(f"{'place':16s} year  DJF  MAM  JJA  SON | nice awful |  J   F   M   A   M   J   J   A   S   O   N   D")
    for n, r in sorted(results.items(), key=lambda kv: -kv[1]['year_mean']):
        print(f"{n:16s} {r['year_mean']:4.0f} {r['Dec-Feb']['mean']:4.0f} {r['Mar-May']['mean']:4.0f} {r['Jun-Aug']['mean']:4.0f} {r['Sep-Nov']['mean']:4.0f} | {r['nice_days']:4.0f} {r['awful_days']:5.0f} | " + ' '.join(f"{x:3.0f}" for x in r['month_mean']))
