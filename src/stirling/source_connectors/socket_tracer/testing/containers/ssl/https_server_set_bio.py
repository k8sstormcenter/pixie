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

# An HTTPS server that drives libssl directly, the way servers with their own I/O layer do:
# the socket is attached with BIO_new_socket + SSL_set_bio (never SSL_set_fd) and read-ahead is
# on, so a request that arrives together with the client's Finished message is read from the
# socket during the handshake and later returned by an SSL_read that makes no syscall.

import ctypes
import logging
import os
import socket
import ssl  # noqa: F401  maps the libssl that Python itself uses
import sys

logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s %(levelname)s %(message)s',
    handlers=[logging.StreamHandler()])

CERT = b'/etc/ssl/server.crt'
KEY = b'/etc/ssl/server.key'
PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 443

SSL_FILETYPE_PEM = 1
SSL_CTRL_SET_READ_AHEAD = 41
BIO_NOCLOSE = 0


def mapped_libssl():
    with open('/proc/self/maps') as maps:
        for line in maps:
            path = line.split()[-1]
            if os.path.basename(path).startswith('libssl.so'):
                return path
    raise RuntimeError('libssl is not mapped')


lib = ctypes.CDLL(mapped_libssl())
vp, ci, cl = ctypes.c_void_p, ctypes.c_int, ctypes.c_long
for name, res, args in [
        ('TLS_server_method', vp, []),
        ('SSL_CTX_new', vp, [vp]),
        ('SSL_CTX_use_certificate_file', ci, [vp, ctypes.c_char_p, ci]),
        ('SSL_CTX_use_PrivateKey_file', ci, [vp, ctypes.c_char_p, ci]),
        ('SSL_CTX_ctrl', cl, [vp, ci, cl, vp]),
        ('SSL_new', vp, [vp]),
        ('BIO_new_socket', vp, [ci, ci]),
        ('SSL_set_bio', None, [vp, vp, vp]),
        ('SSL_accept', ci, [vp]),
        ('SSL_read', ci, [vp, vp, ci]),
        ('SSL_write', ci, [vp, vp, ci]),
        ('SSL_shutdown', ci, [vp]),
        ('SSL_free', None, [vp])]:
    fn = getattr(lib, name)
    fn.restype, fn.argtypes = res, args

with open('/usr/share/nginx/html/index.html', 'rb') as f:
    PAYLOAD = f.read()
RESPONSE = (b'HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nContent-Length: %d\r\n'
            b'Connection: close\r\n\r\n' % len(PAYLOAD)) + PAYLOAD

ctx = lib.SSL_CTX_new(lib.TLS_server_method())
if (lib.SSL_CTX_use_certificate_file(ctx, CERT, SSL_FILETYPE_PEM) != 1
        or lib.SSL_CTX_use_PrivateKey_file(ctx, KEY, SSL_FILETYPE_PEM) != 1):
    raise RuntimeError('cannot load the certificate')
lib.SSL_CTX_ctrl(ctx, SSL_CTRL_SET_READ_AHEAD, 1, None)

srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind(('0.0.0.0', PORT))
srv.listen(64)
logging.info(f'pid={os.getpid()} port={PORT}')

buf = ctypes.create_string_buffer(16384)
while True:
    conn, _ = srv.accept()
    s = lib.SSL_new(ctx)
    lib.SSL_set_bio(s, *([lib.BIO_new_socket(conn.fileno(), BIO_NOCLOSE)] * 2))
    if lib.SSL_accept(s) == 1:
        request = b''
        while b'\r\n\r\n' not in request:
            n = lib.SSL_read(s, buf, len(buf))
            if n <= 0:
                break
            request += buf.raw[:n]
        if request:
            lib.SSL_write(s, RESPONSE, len(RESPONSE))
        lib.SSL_shutdown(s)
    lib.SSL_free(s)
    conn.close()
