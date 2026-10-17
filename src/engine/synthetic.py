# Written by AI on 2026.10.17 by model gpt-6.1-sol. Debug fixtures only; never published as climate.
"""Use: python3 src/engine/synthetic.py REAL_V6 SYNTHETIC_OUTPUT. No third-party dependencies."""
import datetime, math, pathlib, shutil, struct, sys, zlib
source, target = map(pathlib.Path, sys.argv[1:])
(target / 'climate').mkdir(parents=True, exist_ok=True)
(target / 'forecast' / 'synthetic').mkdir(parents=True, exist_ok=True)
shutil.copyfile(source / 'cells.bin', target / 'cells.bin')
cells = list(struct.iter_unpack('<HH', (source / 'cells.bin').read_bytes()))
points = (source / 'points.bin').read_bytes()
land = bytearray(1440 * 721)
for offset in range(4, len(points), 12):
    fine_index = struct.unpack_from('<I', points, offset)[0]
    land[fine_index] = 1

def temperature(value): return max(0, min(255, round((value + 30) * 3)))
def record(row, column, month, day):
    latitude = 89.5 - row
    summer = math.cos((month - 7) * math.pi / 6) * (1 if latitude > 0 else -1)
    warm = 28 - abs(latitude) * .55 + summer * abs(latitude) * .25 + (day - 3.5) * .75
    sun = [warm - 1, warm + 3, warm + 5, warm + 4, warm + 1, warm - 3]
    shade = [warm - 2, warm, warm - 3, warm - 7]
    dew = min(warm - 2, 25 - abs(latitude) * .25)
    rain = (day * 3 + column) % 12 if day % 4 == 0 else 0
    daylight = max(1, min(11, round(7 + summer * abs(latitude) / 20)))
    return bytes([*(temperature(t) for t in sun + shade), temperature(dew), round(math.sqrt(rain) * 50), 20 + column % 30, max(0, daylight - day % 4), daylight])

def png_chunk(name, content):
    return struct.pack('>I', len(content)) + name + content + struct.pack('>I', zlib.crc32(name + content) & 0xffffffff)
for month in range(1, 13):
    (target / 'climate' / f'month-{month:02}.bin').write_bytes(b''.join(record(row, column, month, day) for row, column in cells for day in range(8)))
    pixels = bytearray([255]) * (1440 * 721)
    for index, is_land in enumerate(land):
        if is_land: pixels[index] = max(0, min(254, 128 + round(25 * math.sin(index % 1440 / 37) * math.cos(index // 1440 / 23))))
    filtered = b''.join(b'\0' + pixels[row * 1440:(row + 1) * 1440] for row in range(721))
    png = b'\x89PNG\r\n\x1a\n' + png_chunk(b'IHDR', struct.pack('>IIBBBBB', 1440, 721, 8, 0, 0, 0, 0)) + png_chunk(b'IDAT', zlib.compress(filtered)) + png_chunk(b'IEND', b'')
    (target / 'climate' / f'offsets-{month:02}.png').write_bytes(png)
first = datetime.date(2026, 10, 25)
(target / 'forecast' / 'synthetic' / 'days.bin').write_bytes(b''.join(record(row, column, (first + datetime.timedelta(days=day)).month, day % 8) for row, column in cells for day in range(15)))
(target / 'forecast' / 'current.json').write_text('{"run":"synthetic","firstDate":"2026-10-25","days":15}')
print(f'{len(cells)} cells, full-size synthetic fixture at {target}')
