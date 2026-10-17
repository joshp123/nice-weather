# Written by AI on 2026.10.17 by model claude opus 5.5.
# Writes v6/labels-1.json (the 1,000 biggest places) and v6/labels-2.json (the next 14,000) from places.tsv:
# arrays of [name, slug, latitude, longitude, population]. The slug is "<country>/<city>" in kebab case, the same rule
# as the Worker; when two places in a country share a slug, the bigger one keeps it.
# Usage: labels.py <places.tsv> <v6 folder>
import json, pathlib, re, sys, unicodedata

def kebab(text):
    text = unicodedata.normalize('NFD', text).encode('ascii', 'ignore').decode().lower()
    return re.sub(r'[^a-z0-9]+', '-', text).strip('-')

rows = [line.rstrip('\n').split('\t') for line in open(sys.argv[1], encoding='utf-8') if line.strip()]
rows.sort(key=lambda row: -int(row[5]))
seen, places = set(), []
for name, plain, region, latitude, longitude, population, *_ in rows:
    slug = f"{kebab(region.split(',')[-1])}/{kebab(plain)}"
    if slug in seen: continue
    seen.add(slug)
    places.append([name, slug, round(float(latitude), 2), round(float(longitude), 2), int(population)])
    if len(places) == 15000: break
out = pathlib.Path(sys.argv[2])
for file, part in (('labels-1.json', places[:1000]), ('labels-2.json', places[1000:])):
    (out / file).write_text(json.dumps(part, ensure_ascii=False, separators=(',', ':')))
    print(file, len(part), (out / file).stat().st_size // 1024, 'KB')
print(places[:3], [p for p in places if p[1] in ('greece/chania', 'portugal/lisbon', 'spain/malaga')])
