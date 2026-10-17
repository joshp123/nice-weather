"""Freeze the interface's existing typical-day preview assets for an ordered upload.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""
import argparse
import hashlib
import json
import pathlib
import shutil


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--artifacts', type=pathlib.Path, required=True)
    parser.add_argument('--output', type=pathlib.Path, required=True)
    args = parser.parse_args()
    forecast = json.loads((args.artifacts/'v6/forecast/current.json').read_text())
    climate_files = sorted((args.artifacts/'v6/climate').glob('scores-typical-day-*.png'))
    forecast_files = sorted((args.artifacts/'v6/forecast'/forecast['run']).glob('scores-typical-day-*.png'))
    if len(climate_files) != 133 or len(forecast_files) != 15:
        raise ValueError('Expected 133 climate and 15 current-forecast typical-day preview images')
    files = []
    for source in climate_files + forecast_files:
        content = source.read_bytes()
        if content[:8] != b'\x89PNG\r\n\x1a\n':
            raise ValueError(f'Invalid preview PNG: {source}')
        key = source.relative_to(args.artifacts)
        destination = args.output/key
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)
        files.append(dict(key=key.as_posix(), bytes=len(content), sha256=hashlib.sha256(content).hexdigest()))
    manifest = dict(written_by='AI, GPT-6.1 Sol, 2026.10.18',
        purpose='Matching interface preview assets; publish these new keys before the app code.',
        forecast_run=forecast['run'], files=files, count=len(files), bytes=sum(row['bytes'] for row in files),
        unchanged='No cells, points, weather records, offsets, facts, scores.bin or forecast pointer included.')
    args.output.mkdir(parents=True, exist_ok=True)
    (args.output/'MANIFEST.json').write_text(json.dumps(manifest, indent=2)+'\n')
    print(json.dumps({key: value for key, value in manifest.items() if key != 'files'}))


if __name__ == '__main__':
    main()
