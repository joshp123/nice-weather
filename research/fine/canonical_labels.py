"""Keep the label catalogue's rows while using the Worker's exact city coordinates.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""
import argparse
import hashlib
import json
import pathlib
import re
import unicodedata


def kebab(text):
    plain = unicodedata.normalize('NFD', text).encode('ascii', 'ignore').decode().lower()
    return re.sub(r'[^a-z0-9]+', '-', plain).strip('-')


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def pixel(latitude, longitude):
    return min(21599, int((90-latitude)*120)), int(((((longitude+180) % 360)+360) % 360)*120)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--places', type=pathlib.Path, required=True)
    parser.add_argument('--legacy-labels', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args()
    rows = [line.rstrip('\n').split('\t') for line in args.places.read_text().splitlines() if line]
    rows.sort(key=lambda row: -int(row[5]))
    canonical = {}
    for name, plain, region, latitude, longitude, population, *_ in rows:
        slug = f"{kebab(region.split(',')[-1])}/{kebab(plain)}"
        canonical.setdefault(slug, (float(latitude), float(longitude)))
    args.output.mkdir(parents=True, exist_ok=True)
    files = {}
    for filename in ('labels-1.json', 'labels-2.json'):
        source = args.legacy_labels/filename
        old = json.loads(source.read_text())
        new = [[row[0], row[1], *canonical[row[1]], row[4]] for row in old]
        destination = args.output/filename
        destination.write_text(json.dumps(new, ensure_ascii=False, separators=(',', ':')))
        files[filename] = dict(rows=len(new), changed_coordinates=sum(a[2:4] != b[2:4] for a, b in zip(old, new)),
            changed_pixels=sum(pixel(*a[2:4]) != pixel(*b[2:4]) for a, b in zip(old, new)),
            legacy_sha256=sha(source), sha256=sha(destination), bytes=destination.stat().st_size)
    notice = ('City names, population and coordinates: GeoNames, Creative Commons Attribution 4.0.\n'
        'https://www.geonames.org/\nhttps://creativecommons.org/licenses/by/4.0/\n'
        'Source and licence: https://download.geonames.org/export/dump/readme.txt\n'
        'Export: https://download.geonames.org/export/\n'
        'Coordinates retain the existing places.tsv precision. Label rows, order, names, slugs and populations are preserved.\n')
    (args.output/'ATTRIBUTION.txt').write_text(notice)
    manifest = dict(written_by='AI, GPT-6.1 Sol, 2026.10.18', format='existing-label-array-v1',
        source='GeoNames via existing places.tsv', licence='CC-BY-4.0', places_sha256=sha(args.places), files=files)
    (args.output/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
    print(json.dumps(manifest, indent=2))


if __name__ == '__main__':
    main()
