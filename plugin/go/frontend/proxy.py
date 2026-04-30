#!/usr/bin/env python3
"""
OPEC proxy server — serves frontend on :8080 and proxies RPC calls
to localhost:50002 and localhost:50003 with CORS headers added.
"""
import http.server, urllib.request, urllib.error, json, os

FRONTEND_DIR = os.path.expanduser('~/dremo-canopy/plugin/go/frontend')
RPC_PUBLIC   = 'http://localhost:50002'
RPC_ADMIN    = 'http://localhost:50003'

class Handler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **kw):
        super().__init__(*a, directory=FRONTEND_DIR, **kw)

    def add_cors(self):
        self.send_header('Access-Control-Allow-Origin',  '*')
        self.send_header('Access-Control-Allow-Methods', 'GET,POST,OPTIONS')
        self.send_header('Access-Control-Allow-Headers', 'Content-Type')

    def do_OPTIONS(self):
        self.send_response(200)
        self.add_cors()
        self.send_header('Content-Length', '0')
        self.end_headers()

    def do_POST(self):
        # Proxy /rpc/* → RPC_PUBLIC
        # Proxy /admin/* → RPC_ADMIN
        if self.path.startswith('/rpc/'):
            target = RPC_PUBLIC + self.path[4:]  # strip /rpc
        elif self.path.startswith('/admin/'):
            target = RPC_ADMIN + self.path[6:]   # strip /admin
        else:
            self.send_error(404)
            return

        length  = int(self.headers.get('Content-Length', 0))
        body    = self.rfile.read(length) if length else b'{}'

        try:
            req = urllib.request.Request(
                target, data=body,
                headers={'Content-Type': 'application/json'},
                method='POST'
            )
            with urllib.request.urlopen(req, timeout=10) as resp:
                data = resp.read()
                self.send_response(resp.status)
                self.add_cors()
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(data)))
                self.end_headers()
                self.wfile.write(data)
        except urllib.error.HTTPError as e:
            data = e.read()
            self.send_response(e.code)
            self.add_cors()
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        except Exception as e:
            err = json.dumps({'error': str(e)}).encode()
            self.send_response(502)
            self.add_cors()
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(err)))
            self.end_headers()
            self.wfile.write(err)

    def end_headers(self):
        self.add_cors()
        super().end_headers()

    def log_message(self, fmt, *args):
        print(f'[proxy] {self.path} — {fmt % args}')

if __name__ == '__main__':
    import socketserver
    PORT = 8080
    with socketserver.TCPServer(('0.0.0.0', PORT), Handler) as httpd:
        print(f'OPEC proxy running on http://0.0.0.0:{PORT}')
        print(f'  Frontend : http://localhost:{PORT}')
        print(f'  /rpc/*   → {RPC_PUBLIC}')
        print(f'  /admin/* → {RPC_ADMIN}')
        httpd.serve_forever()
