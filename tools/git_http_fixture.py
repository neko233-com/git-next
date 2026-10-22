"""Serve Git repositories over HTTP for benchmarking.

The benchmark needs a remote that behaves like a real HTTP Git host: the same smart
protocol endpoints, the same pack negotiation, the same content. A local `file://`
clone would take a different code path in both clients and would measure the wrong
thing, and a public host would make the numbers depend on the network of the day.

This is a test fixture, not a product. It serves the two endpoints a clone uses and
runs the reference implementation's own `upload-pack` in stateless-RPC mode, which is
the same code path `git http-backend` invokes, so both clients are answered by the
reference protocol implementation rather than by this script.

Usage:
    python tools/git_http_fixture.py --root <dir-of-repositories> [--port 8123]
"""
from __future__ import annotations

import argparse
import os
import subprocess
import sys
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TIMING = bool(os.environ.get('GIT_FIXTURE_TIMING'))

ADVERTISE = b'application/x-git-upload-pack-advertisement'
RESULT = b'application/x-git-upload-pack-result'


def pkt_line(text: bytes) -> bytes:
    """Encode one packet of the pkt-line framing."""
    return f'{len(text) + 4:04x}'.encode() + text


class Handler(BaseHTTPRequestHandler):
    """Answer the smart-HTTP endpoints of a read-only remote."""

    protocol_version = 'HTTP/1.1'
    root = '.'

    def log_message(self, *args):
        return

    def do_GET(self) -> None:
        parsed = urllib.parse.urlsplit(self.path)
        path = urllib.parse.unquote(parsed.path)
        if not path.endswith('/info/refs') or 'service=git-upload-pack' not in parsed.query:
            self.send_error(404)
            return
        repository = self._repository(path[: -len('/info/refs')])
        if repository is None:
            return
        output = pkt_line(b'# service=git-upload-pack\n') + b'0000'
        output += self._run(['upload-pack', '--stateless-rpc', '--advertise-refs', repository], b'')
        self._send(200, ADVERTISE, output)

    def do_POST(self) -> None:
        parsed = urllib.parse.urlsplit(self.path)
        path = urllib.parse.unquote(parsed.path)
        if not path.endswith('/git-upload-pack'):
            self.send_error(404)
            return
        repository = self._repository(path[: -len('/git-upload-pack')])
        if repository is None:
            return
        length = int(self.headers.get('Content-Length') or 0)
        body = self.rfile.read(length) if length else b''
        output = self._run(['upload-pack', '--stateless-rpc', repository], body)
        self._send(200, RESULT, output)

    def _repository(self, request_path: str) -> str | None:
        """Map the request path to a repository inside the served root."""
        relative = request_path.strip('/')
        candidate = os.path.abspath(os.path.join(self.root, relative))
        # The fixture must never serve anything outside the directory it was given.
        if not candidate.startswith(os.path.abspath(self.root) + os.sep):
            self.send_error(403)
            return None
        if not os.path.isdir(candidate):
            self.send_error(404)
            return None
        return candidate

    def _run(self, arguments: list[str], body: bytes) -> bytes:
        environment = dict(os.environ)
        # The client asks for protocol v2 with a header; `git http-backend` turns it
        # into this variable, and `upload-pack` needs it to answer in kind.
        requested = self.headers.get('Git-Protocol')
        if requested:
            environment['GIT_PROTOCOL'] = requested
        started = time.perf_counter()
        result = subprocess.run(
            ['git', *arguments],
            input=body,
            capture_output=True,
            env=environment,
            check=False,
        )
        if TIMING:
            # The server's own cost has to be visible, or a benchmark cannot tell a
            # slow client from a slow fixture.
            elapsed = (time.perf_counter() - started) * 1000
            print(
                f'{self.command} {arguments[0]} {elapsed:.0f} ms '
                f'in={len(body)} out={len(result.stdout)}',
                flush=True,
            )
        if result.returncode != 0:
            self.send_error(500, result.stderr.decode('utf-8', 'replace')[:400])
            return b''
        return result.stdout

    def _send(self, status: int, content_type: bytes, payload: bytes) -> None:
        self.send_response(status)
        self.send_header('Content-Type', content_type.decode())
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)


def serve(root: str, port: int) -> ThreadingHTTPServer:
    handler = type('BoundHandler', (Handler,), {'root': os.path.abspath(root)})
    server = ThreadingHTTPServer(('127.0.0.1', port), handler)
    server.daemon_threads = True
    return server


def main() -> int:
    parser = argparse.ArgumentParser(__doc__)
    parser.add_argument('--root', required=True, help='directory containing the repositories')
    parser.add_argument('--port', type=int, default=8123)
    arguments = parser.parse_args()
    if not os.path.isdir(arguments.root):
        print(f'not a directory: {arguments.root}', file=sys.stderr)
        return 1
    server = serve(arguments.root, arguments.port)
    print(f'serving {os.path.abspath(arguments.root)} on http://127.0.0.1:{arguments.port}/', flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
