# Written by AI on 2026.10.18 by model claude opus 5.5.
# Serves a local build of the app on 127.0.0.1:5198 and forwards data and API requests to the live site,
# so the interface can be checked locally against real data. usage: live_data_proxy.py <dist-dir>
import sys, os, urllib.request, urllib.error
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler

DIST = sys.argv[1]
LIVE = 'https://nice.jjpcodes.com'
TYPES = {'.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png', '.svg': 'image/svg+xml', '.woff2': 'font/woff2', '.woff': 'font/woff', '.json': 'application/json'}


class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *args):
        pass

    def local_file(self, path):
        if path.startswith('/assets/'):
            return os.path.join(DIST, path.lstrip('/'))
        if path == '/method':
            return os.path.join(DIST, 'method.html')
        app_path = (path.startswith('/@') or '.' not in path.rsplit('/', 1)[-1]) and not path.startswith(('/api/', '/data/', '/preview/', '/outlook/'))
        return os.path.join(DIST, 'index.html') if app_path else None

    def send_local(self, file):
        body = open(file, 'rb').read()
        self.send_response(200)
        self.send_header('Content-Type', TYPES.get(os.path.splitext(file)[1], 'application/octet-stream'))
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def forward(self, method):
        length = int(self.headers.get('Content-Length') or 0)
        body = self.rfile.read(length) if length else None
        headers = {name: value for name, value in self.headers.items() if name.lower() in ('range', 'content-type', 'accept', 'user-agent')}
        headers.setdefault('User-Agent', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15')
        request = urllib.request.Request(LIVE + self.path, data=body, method=method, headers=headers)
        try:
            response = urllib.request.urlopen(request, timeout=60)
        except urllib.error.HTTPError as error:
            response = error
        data = response.read()
        self.send_response(response.status if hasattr(response, 'status') else response.code)
        for name, value in response.headers.items():
            if name.lower() in ('content-type', 'content-range', 'cache-control', 'etag', 'accept-ranges'):
                self.send_header(name, value)
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        file = self.local_file(self.path.split('?')[0])
        if file and os.path.isfile(file):
            return self.send_local(file)
        return self.forward('GET')

    def do_POST(self):
        return self.forward('POST')

    def do_HEAD(self):
        return self.forward('HEAD')


ThreadingHTTPServer(('127.0.0.1', 5198), Handler).serve_forever()
