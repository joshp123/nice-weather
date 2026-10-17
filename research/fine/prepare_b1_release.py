"""Seal the audited B1 data and prepare its separately uploaded activation pointer.

Written by AI on 2026.10.18 by model GPT-6.1 Sol.
"""
import argparse
import datetime as dt
import hashlib
import json
import pathlib


def read(path):
    return json.loads(path.read_text())


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def directory_size(path):
    files = [entry for entry in path.rglob('*') if entry.is_file()]
    return dict(files=len(files), bytes=sum(entry.stat().st_size for entry in files))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--data-root', type=pathlib.Path, required=True)
    parser.add_argument('--release', default='v6-fine-b1-20261018')
    parser.add_argument('--validation', type=pathlib.Path, required=True)
    parser.add_argument('--activation', type=pathlib.Path, required=True)
    args = parser.parse_args()
    root, proof = args.data_root/args.release, args.validation
    temperature, replay = read(root/'temperature/manifest.json'), read(root/'replay/manifest.json')
    generation = temperature['configuration']['generation']
    if (replay.get('status') != 'complete' or replay['generation'] != generation
            or replay['chunks'] != temperature['configuration']['chunks']
            or replay['eveningModel'] != 'uniform'):
        raise ValueError('The worldwide components are incomplete or disagree')
    tile_audit, replay_audit = read(proof/'tile-audit.json'), read(proof/'replay-audit.json')
    if (tile_audit['generation'] != generation or replay_audit['generation'] != generation
            or tile_audit['manifest_sha256'] != sha(root/'temperature/manifest.json')
            or replay_audit['manifest_sha256'] != sha(root/'replay/manifest.json')
            or not replay_audit['passes']):
        raise ValueError('Audits do not describe these delivered bytes')
    module = read(proof/'module-parity.json')
    if not module['passes'] or not module['coverage_complete'] or module['compared_places'] != 397:
        raise ValueError('The full independent hourly comparison has not passed')
    go_reports = [read(proof/'outlook-climate'/name) for name in ('go-parity.json','native-go-parity.json')]
    if not all(report['passes'] and report['queries'] == 397 for report in go_reports):
        raise ValueError('Both full Go comparisons must pass')
    transport = read(proof/'transport.json')
    if not transport['passes'] or transport['places'] != 397 or transport['maximum_absolute_difference'] != 0:
        raise ValueError('The compact browser transport has not passed the full place comparison')
    previews, activation, app = read(proof/'previews/parity.json'), read(proof/'activation.json'), read(proof/'app-complete/results.json')
    if not previews['pass'] or not activation['passes'] or not app['passes']:
        raise ValueError('Preview, activation or real-app verification is incomplete')
    for filename, digest in app['sources'].items():
        if sha(pathlib.Path(filename)) != digest:
            raise ValueError(f'The real-app check used different source: {filename}')
    attribution = pathlib.Path(__file__).with_name('ATTRIBUTION.txt').read_text()
    attribution += ('\nB1 named-place release: local 10:00–20:00 uses the ordered high/low '
        'correction; local 21:00 uses the average of the monthly high and low offsets '
        'everywhere. Native dew point is not shifted. Native weather replay uses '
        'the containing fine pixel’s local clock. The overview map and forecast '
        'retain their existing resolution.\n')
    (root/'ATTRIBUTION.txt').write_text(attribution)
    components = {name: directory_size(root/name) for name in ('temperature','replay')}
    result = dict(written_by='AI, GPT-6.1 Sol, 2026.10.18', status='ready', generation=generation,
        builtUTC=dt.datetime.now(dt.timezone.utc).isoformat(), chunks=replay['chunks'],
        firstScoredDay=min(replay['dates']), lastScoredDay=max(replay['dates']), scoredDays=len(replay['dates']),
        resolutionArcSeconds=30, eveningModel='uniform', supportedFinePixels=replay['supportedFinePixels'],
        ownNativePoints=replay['ownNativePoints'], pointClockPackets=replay['packetCount'], components=components,
        attribution='ATTRIBUTION.txt', overviewChanged=False, forecastWeatherChanged=False,
        audits={name: dict(sha256=sha(proof/filename)) for name,filename in {
            'temperature':'tile-audit.json','replay':'replay-audit.json','hourly':'module-parity.json',
            'go':'outlook-climate/go-parity.json','goBinary':'outlook-climate/native-go-parity.json',
            'transport':'transport.json','previews':'previews/parity.json',
            'activation':'activation.json','app':'app-complete/results.json'}.items()},
        knownLimits=['Rotterdam May score regression; accepted city-average gate.',
            'Headline search shortlists overview candidates; displayed numbers use exact replay.',
            'Other weather and the overview map retain ERA5 resolution.'])
    (root/'RELEASE.json').write_text(json.dumps(result, indent=2)+'\n')
    pointer = dict(nativeRoot=f'{args.release}/replay', tileRoot=f'{args.release}/temperature',
        generation=generation, eveningModel='uniform', attribution=f'{args.release}/ATTRIBUTION.txt', status='ready')
    destination = args.activation/'v6/fine/current.json'
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(json.dumps(pointer, indent=2)+'\n')
    print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
