# Written by AI on 2026.10.16 by model GPT-6.1 Sol.
"""Run exactly one climate tile-year in the linked Sites cloud task."""
import getpass
import hashlib
import json
import os
import pathlib
import platform
import subprocess
import tempfile
import time
import urllib.request

SITE = 'https://nice.jjpcodes.com'
ASSETS = SITE + '/data/runner/climate-measures-probe/'
PREFIX = 'forecast/climate-measures-probe'


def download(name, directory):
    started = time.monotonic()
    with urllib.request.urlopen(ASSETS + name, timeout=180) as response:
        body = response.read()
    path = directory / name
    path.write_bytes(body)
    return path, len(body), time.monotonic() - started


def main():
    credential = getpass.getpass('Temporary Site upload grant: ')
    with tempfile.TemporaryDirectory(prefix='niceweather-climate-probe-') as workspace:
        directory = pathlib.Path(workspace)
        binary, binary_bytes, binary_seconds = download('niceweather-linux-amd64', directory)
        static, static_bytes, static_seconds = download('static.bin.gz', directory)
        binary.chmod(0o700)
        started = time.monotonic()
        completed = subprocess.run([
            str(binary), 'climate-measures', '--source-index', 'earthmover',
            '--static', str(static), '--tile-row', '17', '--tile-column', '118',
            '--year', '2020', '--out', str(directory / 'output'),
            '--publish', SITE, '--publish-prefix', PREFIX, '--token-stdin',
        ], input=credential, text=True, stdout=subprocess.PIPE, check=True)
        build_and_upload_seconds = time.monotonic() - started
        report = json.loads(completed.stdout)
        measures = pathlib.Path(report['path']).read_bytes()
        report_path = pathlib.Path(report['path'].replace('.bin.gz', '.report.json'))
        report_bytes = report_path.stat().st_size
        result = {
            'writtenBy': 'AI on 2026.10.16 by model GPT-6.1 Sol',
            'execution': 'linked Sites automation; cloud Linux; standalone Go executable',
            'tile': '39 to 36.25 degrees north; 6 to 3.25 degrees west; Spain and coast',
            'year': 2020,
            'build': report,
            'buildAndUploadWallSeconds': build_and_upload_seconds,
            'bootstrapDownloadedBytes': binary_bytes + static_bytes,
            'bootstrapDownloadWallSeconds': binary_seconds + static_seconds,
            'uploadedBatchBytes': len(measures) + report_bytes,
            'measuresSHA256': hashlib.sha256(measures).hexdigest(),
            'binarySHA256': hashlib.sha256(binary.read_bytes()).hexdigest(),
            'staticSHA256': hashlib.sha256(static.read_bytes()).hexdigest(),
            'machine': {'system': platform.system(), 'architecture': platform.machine(), 'availableCPUs': os.cpu_count()},
            'measuresURL': SITE + '/data/' + PREFIX + '/r17-c118/2020.bin.gz',
        }
        body = (json.dumps(result, indent=2) + '\n').encode()
        request = urllib.request.Request(SITE + '/__publish/' + PREFIX + '/result.json', data=body,
            headers={'Authorization': 'Bearer ' + credential, 'Content-Type': 'application/json'}, method='PUT')
        with urllib.request.urlopen(request, timeout=120) as response:
            if response.status != 201:
                raise RuntimeError('Probe report upload failed')
        credential = None
        print(body.decode())


if __name__ == '__main__':
    main()
