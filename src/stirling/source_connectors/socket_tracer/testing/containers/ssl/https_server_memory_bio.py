# Copyright 2018- The Pixie Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# SPDX-License-Identifier: Apache-2.0

import logging
import os
import ssl
import threading
import time
from http.server import HTTPServer, BaseHTTPRequestHandler

logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s %(levelname)s %(message)s',
    handlers=[logging.StreamHandler()])

CERT = '/etc/ssl/server.crt'
KEY = '/etc/ssl/server.key'

with open("/usr/share/nginx/html/index.html", "r") as f:
    PAYLOAD = f.read()


class MyRequestHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-type", "text/html")
        self.send_header("Content-length", len(PAYLOAD))
        self.end_headers()
        self.wfile.write(bytes(PAYLOAD, 'utf-8'))


def pump(src, dst):
    data = src.read()
    if data:
        dst.write(data)


def memory_bio_exchange(server_ctx, client_ctx):
    c_in, c_out, s_in, s_out = (ssl.MemoryBIO() for _ in range(4))
    client = client_ctx.wrap_bio(c_in, c_out, server_side=False)
    server = server_ctx.wrap_bio(s_in, s_out, server_side=True)
    done = {client: False, server: False}
    for _ in range(20):
        for side in (client, server):
            if done[side]:
                continue
            try:
                side.do_handshake()
                done[side] = True
            except ssl.SSLWantReadError:
                pass
        pump(c_out, s_in)
        pump(s_out, c_in)
        if all(done.values()):
            break
    client.write(b"GET /memory-bio HTTP/1.1\r\nHost: localhost\r\n\r\n")
    pump(c_out, s_in)
    server.read(4096)
    server.write(b"HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
    pump(s_out, c_in)
    client.read(4096)


def memory_bio_loop():
    server_ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    server_ctx.load_cert_chain(certfile=CERT, keyfile=KEY)
    client_ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    client_ctx.check_hostname = False
    client_ctx.verify_mode = ssl.CERT_NONE
    while True:
        t = threading.Thread(target=memory_bio_exchange, args=(server_ctx, client_ctx))
        t.start()
        t.join()
        time.sleep(0.1)


logging.info(f"pid={os.getpid()}")

httpd = HTTPServer(('localhost', 443), MyRequestHandler)
ssl_context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ssl_context.load_cert_chain(certfile=CERT, keyfile=KEY)
httpd.socket = ssl_context.wrap_socket(httpd.socket, server_side=True)

threading.Thread(target=memory_bio_loop, daemon=True).start()
httpd.serve_forever()
