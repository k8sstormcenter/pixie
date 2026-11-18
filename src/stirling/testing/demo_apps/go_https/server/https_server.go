/*
 * Copyright 2018- The Pixie Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
)

const (
	httpPort  = 50100
	httpsPort = 50101
)

func basicHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	_, err := io.WriteString(w, `{"status":"ok"}`)
	if err != nil {
		log.Fatal(err)
	}
}

func largeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	_, err := io.WriteString(w, strings.Repeat(" ", 20000)+`{"status":"ok"}`)
	if err != nil {
		log.Fatal(err)
	}
}

type wrappedConn struct {
	net.Conn
}

type wrappedListener struct {
	net.Listener
}

func (l wrappedListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &wrappedConn{conn}, nil
}

func listenAndServeTLS(port int, certFile, keyFile string, wrapConn bool) {
	log.Printf("Starting HTTPS service on Port %d", port)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		log.Fatal(err)
	}
	if wrapConn {
		listener = wrappedListener{listener}
	}
	err = http.ServeTLS(listener, nil, certFile, keyFile)
	if err != nil {
		log.Fatal(err)
	}
}

func listenAndServe(port int) {
	log.Printf("Starting HTTP service on Port %d", port)
	err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	certPath := flag.String("cert", "", "Path to the .crt file.")
	keyPath := flag.String("key", "", "Path to the .key file.")
	wrapConn := flag.Bool("wrap_conn", false, "Wrap accepted connections in a type that embeds net.Conn.")
	flag.Parse()

	http.HandleFunc("/", basicHandler)
	http.HandleFunc("/large", largeHandler)

	go listenAndServeTLS(httpsPort, *certPath, *keyPath, *wrapConn)
	listenAndServe(httpPort)
}
