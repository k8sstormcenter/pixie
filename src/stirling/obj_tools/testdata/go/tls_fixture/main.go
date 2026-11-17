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
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
)

func serve(addr, cert, key string) error {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.URL.Path)
	})
	srv := &http.Server{Addr: addr, Handler: handler}
	return srv.ListenAndServeTLS(cert, key)
}

func get(url string) error {
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{ServerName: "localhost"},
		ForceAttemptHTTP2: true,
	}}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

func dial(addr string) error {
	var conn net.Conn
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return err
	}
	conn = tls.Client(conn, &tls.Config{ServerName: "localhost"})
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		return err
	}
	buf := make([]byte, 64)
	_, err = conn.Read(buf)
	return err
}

func main() {
	if len(os.Args) < 3 {
		return
	}
	var err error
	switch os.Args[1] {
	case "serve":
		if len(os.Args) < 5 {
			return
		}
		err = serve(os.Args[2], os.Args[3], os.Args[4])
	case "get":
		err = get(os.Args[2])
	case "dial":
		err = dial(os.Args[2])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
