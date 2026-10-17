# Written by AI on 2026.10.17 by model claude opus 5.5.
# Writes v6/climate/scores.bin: Josh's default taste's mean day score for every 0.25° land point and month,
# from the published day samples and offsets, so link previews can say "Lisbon in July: Beautiful, nice score 82".
# 12 bytes a point (January to December), row-major on the 1440 x 721 grid; 255 at sea.
# Usage: default_scores.py <v6 folder>
import pathlib, sys
import numpy as np

COLD = [(9, 0), (11, 3), (13, 10), (15, 22), (17, 40), (19, 62), (21, 85), (23, 100)]
HEAT = [(28, 100), (31, 85), (34, 60), (37, 30), (40, 10), (43, 0)]
EVENING = [(4, 0), (8, 30), (12, 65), (16, 90), (19, 100)]
RAIN = [(0, 1), (0.5, 0.95), (1, 0.88), (2, 0.74), (4, 0.5), (8, 0.22), (15, 0.05)]
WIND = [(15, 1), (20, 0.97), (25, 0.92), (30, 0.85), (40, 0.65), (50, 0.4), (65, 0.15)]
HUMIDITY = [(16, 1), (18, 0.95), (20, 0.85), (22, 0.65), (24, 0.4), (26, 0.2), (28, 0.1)]
SUN = [(0, 0.75), (0.3, 0.85), (0.6, 0.95), (0.8, 1)]
curve = lambda points, x: np.interp(x, [p[0] for p in points], [p[1] for p in points])
SUN_HOURS, SHADE_HOURS = [0, 2, 4, 6, 8, 10], [2, 5, 8, 11]

def greyscale_png(path):
    """8-bit greyscale PNG -> uint8 array. Pillow decodes the stored values exactly (no colour management)."""
    from PIL import Image
    return np.asarray(Image.open(path).convert('L'))

def scores(days, offsets):
    """days: (points, 8, 15) uint8 records; offsets: (points,) °C. Returns each point's mean day score."""
    temperature = lambda b: b.astype(np.float64) / 3 - 30
    sun_keys, shade_keys = temperature(days[..., 0:6]) + offsets[:, None, None], temperature(days[..., 6:10]) + offsets[:, None, None]
    hours = np.arange(11)
    def at(keys, positions, hour_keys):
        out = np.empty(keys.shape[:-1] + (len(positions),))
        for i, h in enumerate(positions):
            k = np.clip(np.searchsorted(hour_keys, h, side='right') - 1, 0, len(hour_keys) - 2)
            w = np.clip((h - hour_keys[k]) / (hour_keys[k + 1] - hour_keys[k]), 0, 1)
            out[..., i] = keys[..., k] * (1 - w) + keys[..., k + 1] * w
        return out
    sun_hourly = at(sun_keys, hours, SUN_HOURS); shade_hourly = at(shade_keys, hours, SHADE_HOURS)
    daylight = np.maximum(1, days[..., 14]).astype(np.float64)
    grades = np.minimum(curve(COLD, sun_hourly), curve(HEAT, shade_hourly)) * (hours[None, None, :] < daylight[..., None])
    at21 = shade_keys[..., 3]
    evening = np.minimum(curve(EVENING, at21), curve(HEAT, at21))
    warmth = (grades.sum(-1) + 2 * evening) / (daylight + 2)
    rain = (days[..., 11].astype(np.float64) / 50) ** 2
    score = warmth * curve(RAIN, rain) * curve(WIND, days[..., 12] / 2) * curve(HUMIDITY, temperature(days[..., 10])) * curve(SUN, days[..., 13] / daylight)
    return score.mean(-1)

if __name__ == '__main__':
    root = pathlib.Path(sys.argv[1])
    raw = (root / 'points.bin').read_bytes()
    points = np.frombuffer(raw[4:], dtype=[('fine', '<u4'), ('source', '<u4'), ('shift', '<i2'), ('cell', '<u2')])
    cell_count = (root / 'cells.bin').stat().st_size // 4
    out = np.full((721 * 1440, 12), 255, np.uint8)
    for month in range(1, 13):
        samples = np.fromfile(root / f'climate/month-{month:02d}.bin', np.uint8).reshape(cell_count, 8, 15)
        offset_bytes = greyscale_png(root / f'climate/offsets-{month:02d}.png').reshape(-1)[points['fine']]
        offsets = np.where(offset_bytes == 255, 0, (offset_bytes.astype(np.float64) - 128) / 10)
        out[points['fine'], month - 1] = np.clip(np.round(scores(samples[points['cell']], offsets)), 0, 254).astype(np.uint8)
        print(f'month {month}: done', flush=True)
    out.tofile(root / 'climate/scores.bin')
    for name, lat, lon in (('Lisbon', 38.72, -9.14), ('Málaga', 36.72, -4.42), ('Rotterdam', 51.92, 4.48), ('Singapore', 1.35, 103.82), ('San Diego', 32.72, -117.16)):
        fine = round((90 - lat) / 0.25) * 1440 + round((lon + 180) / 0.25) % 1440
        print(name, list(out[fine]))
