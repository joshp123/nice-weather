# Written by AI on 2026.10.17 by model claude opus 5.5. Research only.
# Nice day v5. The day runs 10:00 to 21:00. Daylight hours (from 10:00 until sunset, at most to 20:00) count as
# t-shirt weather when they feel at least `low` in the sun and at most `high` in the shade; past `high` an hour fades
# out over HEAT_FADE degrees. A nice day: at least 55% of its daylight hours count, and no limit is broken
# (the day's average dew point, its rain, its average wind, and the feel at 21:00).
import datetime as dt, math, zoneinfo
import numpy as np
DEFAULT = dict(low=21.0, high=30.0, humidity=20.0, rain=1.0, wind=35.0, evening=12.0)
COLD_FADE, HEAT_FADE, NICE = 0.0, 3.0, 55.0

def felt(T, dew, ws_kmh, dni):
    ws = ws_kmh / 3.6; dew = np.minimum(dew, T); e = 6.105 * np.exp(17.27 * dew / (237.7 + dew))
    shade = T + 0.33 * e - 0.70 * ws - 4
    return shade + 0.70 * (0.15 * dni) / (ws + 10), shade, dew

def sunset_local_hour(day, lat, lon, tz):
    """NOAA's approximate solar equations; good to a few minutes."""
    n = day.timetuple().tm_yday; g = 2 * math.pi / 365 * (n - 1)
    eqtime = 229.18 * (0.000075 + 0.001868 * math.cos(g) - 0.032077 * math.sin(g) - 0.014615 * math.cos(2 * g) - 0.040849 * math.sin(2 * g))
    decl = 0.006918 - 0.399912 * math.cos(g) + 0.070257 * math.sin(g) - 0.006758 * math.cos(2 * g) + 0.000907 * math.sin(2 * g) - 0.002697 * math.cos(3 * g) + 0.00148 * math.sin(3 * g)
    cos_ha = math.cos(math.radians(90.833)) / (math.cos(math.radians(lat)) * math.cos(decl)) - math.tan(math.radians(lat)) * math.tan(decl)
    ha = math.degrees(math.acos(max(-1, min(1, cos_ha))))
    utc_minutes = 720 - 4 * (lon - ha) - eqtime
    midnight = dt.datetime.combine(day, dt.time(0), dt.timezone.utc)
    local = (midnight + dt.timedelta(minutes=utc_minutes)).astimezone(zoneinfo.ZoneInfo(tz))
    return local.hour + local.minute / 60 + (24 if local.date() > day else 0)

def daylight_hours(days, lat, lon, tz):
    """Hours from 10:00 that start before sunset, at most the 11 hours 10:00 to 20:00."""
    return np.array([int(np.clip(math.ceil(sunset_local_hour(d, lat, lon, tz)) - 10, 1, 11)) for d in days])

def score_from_felt(sun, shade, daylight, day_dew, day_rain, day_wind, taste={}):
    """sun, shade: felt temperature for 10:00 to 21:00 (12 hours); the last hour is the walk home."""
    t = {**DEFAULT, **taste}
    warm = sun[:, :11] >= t['low'] if COLD_FADE == 0 else np.clip(1 - (t['low'] - sun[:, :11]) / COLD_FADE, 0, 1)
    cool = np.clip(1 - (shade[:, :11] - t['high']) / HEAT_FADE, 0, 1)
    counts = np.minimum(warm, cool) * (np.arange(11)[None, :] < daylight[:, None])
    score = counts.sum(1) / daylight * 100
    broken = np.zeros(len(score), bool)
    if t['humidity'] is not None: broken |= day_dew > t['humidity']
    if t['rain'] is not None: broken |= day_rain > t['rain']
    if t['wind'] is not None: broken |= day_wind > t['wind']
    if t['evening'] is not None: broken |= shade[:, 11] < t['evening']
    return np.where(broken, np.minimum(score, NICE - 1), score)
