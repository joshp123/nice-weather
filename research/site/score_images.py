# Written by AI on 2026.10.17 by model claude opus 5.5.
# Writes v6/climate/scores-<period>.png from scores.bin, so the first view of any climate period is one small image:
# 8-bit greyscale on the 1440 x 721 grid, value = round(score), 255 = sea. A season or the year is the mean of its
# months' scores, months weighted equally, as the engine does. Usage: score_images.py <v6 folder>
import pathlib, sys
import numpy as np
from PIL import Image

MONTHS = ['january', 'february', 'march', 'april', 'may', 'june', 'july', 'august', 'september', 'october', 'november', 'december']
SEASONS = {'dec-feb': [12, 1, 2], 'mar-may': [3, 4, 5], 'jun-aug': [6, 7, 8], 'sep-nov': [9, 10, 11], 'year': list(range(1, 13))}

root = pathlib.Path(sys.argv[1])
scores = np.fromfile(root / 'climate/scores.bin', np.uint8).reshape(721, 1440, 12)
sea = scores[..., 0] == 255
def save(word, months):
    mean = scores[..., [m - 1 for m in months]].astype(np.float64).mean(-1)
    image = np.where(sea, 255, np.clip(np.round(mean), 0, 100)).astype(np.uint8)
    path = root / f'climate/scores-{word}.png'
    Image.fromarray(image, mode='L').save(path, optimize=True)
    return path.stat().st_size
sizes = {word: save(word, [index + 1]) for index, word in enumerate(MONTHS)}
sizes.update({word: save(word, months) for word, months in SEASONS.items()})
print({word: f'{size // 1024} KB' for word, size in sizes.items()})
