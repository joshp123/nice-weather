# Written by AI on 2026.10.17 by model claude opus 5.5. Research only.
# Nice day v4. Comfort: the share of daytime hours (10:00 to 20:00) that are t-shirt weather, felt in the sun
# when cool and in the shade when hot. Limits, each with units: dew point, rain, wind and the walk home at 9 pm.
# Breaking any limit makes the day Fair at best. Nice is a score of 55 or more.
import numpy as np
DEFAULT = dict(low=21.0, high=30.0, humidity=19.0, rain=2.0, wind=35.0, evening=14.0)
COLD_RAMP, HOT_RAMP, FAIR_AT_BEST = 5.0, 10.0, 54.0

def felt(T, dew, ws_kmh, dni):
    ws = ws_kmh / 3.6; dew = np.minimum(dew, T); e = 6.105 * np.exp(17.27 * dew / (237.7 + dew))
    shade = T + 0.33 * e - 0.70 * ws - 4
    return shade + 0.70 * (0.15 * dni) / (ws + 10), shade, dew

def score_from_felt(sun, shade, day_dew, day_rain, day_wind, taste):
    """sun and shade: felt temperatures for 10:00 to 21:00 (12 hours). The last hour is the walk home."""
    t = {**DEFAULT, **taste}
    warm_enough = np.clip(1 - (t['low'] - sun[:, :11]) / COLD_RAMP, 0, 1)
    not_too_hot = np.clip(1 - (shade[:, :11] - t['high']) / HOT_RAMP, 0, 1)
    score = np.minimum(warm_enough, not_too_hot).mean(1) * 100
    broken = np.zeros(len(score), bool)
    if t['humidity'] is not None: broken |= day_dew > t['humidity']
    if t['rain'] is not None: broken |= day_rain > t['rain']
    if t['wind'] is not None: broken |= day_wind > t['wind']
    if t['evening'] is not None: broken |= shade[:, 11] < t['evening']
    return np.where(broken, np.minimum(score, FAIR_AT_BEST), score)

def score(T, dew, ws_kmh, dni, rain_total, taste={}):
    sun, shade, dew = felt(T, dew, ws_kmh, dni)
    return score_from_felt(sun, shade, dew.mean(1), rain_total, ws_kmh.mean(1), taste)
