#!/usr/bin/env python3
"""Run a command with isolated HTTP origins for iOS client boundary checks."""

import json
import os
import signal
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


SERVER_IDS = {
    18081: "11111111-1111-4111-8111-111111111111",
    18082: "11111111-1111-4111-8111-111111111111",
    18083: "11111111-1111-4111-8111-111111111111",
}
HOSTILE_PATHS = {"/escape", "/stolen"}
hostile_hits = 0
redirect_hits = 0
lock = threading.Lock()


class Handler(BaseHTTPRequestHandler):
    server_version = "JastreamerIOSBoundary/1"

    def log_message(self, format_string, *args):
        print(f"boundary:{self.server.server_port}: " + format_string % args, flush=True)

    def do_GET(self):
        global hostile_hits, redirect_hits
        port = self.server.server_port
        if self.path == "/api/v1/discovery" and port == 18083:
            self.reply(302, "application/json", b"", {"Location": "http://127.0.0.1:18081/redirect-target"})
            return
        if self.path == "/redirect-count":
            with lock:
                body = str(redirect_hits).encode()
            self.reply(200, "text/plain", body)
            return
        if self.path in {"/api/v1/discovery", "/redirect-target"}:
            if self.path == "/redirect-target":
                with lock:
                    redirect_hits += 1
            body = json.dumps({
                "product": "jastreamer",
                "protocol": 1,
                "id": SERVER_IDS[port],
                "name": f"Boundary {port}",
                "version": "test",
            }).encode()
            self.reply(200, "application/json", body)
            return
        if self.path == "/hostile-count":
            with lock:
                body = str(hostile_hits).encode()
            self.reply(200, "text/plain", body)
            return
        if self.path in HOSTILE_PATHS:
            with lock:
                hostile_hits += 1
            self.reply(200, "text/plain", b"hostile origin reached")
            return
        if self.path != "/":
            self.reply(404, "text/plain", b"not found")
            return
        other = 18082 if port == 18081 else 18081
        body = f"""<!doctype html>
<html lang="en"><head><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body>
<h1>Boundary {port}</h1>
<p id="state"></p>
<p id="fetch-result"></p>
<p id="file-result"></p>
<input type="file" aria-label="Upload file" onclick="document.getElementById('file-result').textContent='File picker requested'">
<button id="store" onclick="storeBoundary()">Store boundary</button>
<button id="fetch" onclick="crossFetch()">Cross-origin fetch</button>
<button id="navigate" onclick="location.href='http://127.0.0.1:{other}/escape'">Cross-origin navigation</button>
<script>
function render() {{
  const cookie = document.cookie.split(';').map(v => v.trim()).find(v => v.startsWith('boundary='));
  document.getElementById('state').textContent = (cookie ? cookie.slice(9) : '') + '|' + (localStorage.getItem('boundary') || '');
}}
function storeBoundary() {{
  document.cookie = 'boundary=stored; Path=/; Max-Age=3600; SameSite=Strict';
  localStorage.setItem('boundary', 'stored');
  render();
}}
async function crossFetch() {{
  const result = document.getElementById('fetch-result');
  try {{
    await fetch('http://127.0.0.1:{other}/stolen');
    result.textContent = 'Cross-origin request completed';
  }} catch (_) {{
    result.textContent = 'Cross-origin request rejected';
  }}
}}
render();
</script>
</body></html>""".encode()
        self.reply(200, "text/html; charset=utf-8", body)

    def do_POST(self):
        if self.path != "/rotate-id" or self.server.server_port != 18081:
            self.reply(404, "text/plain", b"not found")
            return
        with lock:
            SERVER_IDS[18081] = "33333333-3333-4333-8333-333333333333"
        self.reply(204, "text/plain", b"")

    def reply(self, status, content_type, body, headers=None):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        for name, value in (headers or {}).items():
            self.send_header(name, value)
        self.end_headers()
        self.wfile.write(body)


class Interrupted(Exception):
    def __init__(self, signum):
        self.signum = signum


def main():
    if len(sys.argv) < 2:
        print("usage: ios-boundary-fixture.py COMMAND [ARG ...]", file=sys.stderr)
        return 64

    def interrupted(signum, _frame):
        raise Interrupted(signum)

    signal.signal(signal.SIGINT, interrupted)
    signal.signal(signal.SIGTERM, interrupted)
    servers = []
    try:
        for port in SERVER_IDS:
            servers.append(ThreadingHTTPServer(("127.0.0.1", port), Handler))
    except OSError as failure:
        for server in servers:
            server.server_close()
        print(failure, file=sys.stderr)
        return 65
    threads = [threading.Thread(target=server.serve_forever, daemon=True) for server in servers]
    for thread in threads:
        thread.start()
    print("iOS boundary origins ready at host loopback TCP " + ", ".join(map(str, SERVER_IDS)), flush=True)

    command = None
    result = 65
    try:
        command = subprocess.Popen(sys.argv[1:], start_new_session=True)
        result = command.wait()
        if result < 0:
            result = 128 - result
    except Interrupted as failure:
        result = 128 + failure.signum
    except OSError as failure:
        print(failure, file=sys.stderr)
    finally:
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        if command is not None and command.poll() is None:
            try:
                os.killpg(command.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                command.wait(timeout=10)
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(command.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                command.wait()
        for server in servers:
            server.shutdown()
            server.server_close()
        for thread in threads:
            thread.join(timeout=5)
    return result


if __name__ == "__main__":
    sys.exit(main())
