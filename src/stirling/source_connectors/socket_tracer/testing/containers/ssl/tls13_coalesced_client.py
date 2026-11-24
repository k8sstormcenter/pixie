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

import socket
import ssl
import sys

HOST = '127.0.0.1'
PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8443
COUNT = int(sys.argv[2]) if len(sys.argv) > 2 else 10
REQUEST = b"GET /index.html HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"


def request_with_finished():
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    ctx.minimum_version = ssl.TLSVersion.TLSv1_3
    incoming, outgoing = ssl.MemoryBIO(), ssl.MemoryBIO()
    tls = ctx.wrap_bio(incoming, outgoing, server_hostname='localhost')
    with socket.create_connection((HOST, PORT)) as sock:
        while True:
            try:
                tls.do_handshake()
                break
            except ssl.SSLWantReadError:
                pending = outgoing.read()
                if pending:
                    sock.sendall(pending)
                incoming.write(sock.recv(65536))
        tls.write(REQUEST)
        sock.sendall(outgoing.read())
        response = b''
        while True:
            data = sock.recv(65536)
            if not data:
                break
            incoming.write(data)
            while True:
                try:
                    chunk = tls.read(65536)
                except (ssl.SSLWantReadError, ssl.SSLZeroReturnError):
                    break
                if not chunk:
                    break
                response += chunk
    return response


for i in range(COUNT):
    print(request_with_finished().split(b'\r\n', 1)[0].decode(), flush=True)
