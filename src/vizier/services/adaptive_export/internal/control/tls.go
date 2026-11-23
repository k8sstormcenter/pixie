// Copyright 2018- The Pixie Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package control

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

// TLSConfig builds the server-side *tls.Config for the control surface.
//
// If BOTH certFile and keyFile exist and load, the mounted keypair is used
// (the shared service-tls-certs the broker/PEM already carry). Otherwise an
// ephemeral in-memory self-signed cert is generated so TLS works with zero
// extra secrets — dx skip-verifies the in-cluster cert, so a self-signed cert
// is sufficient to stop the bearer JWT crossing the CNI in cleartext.
//
// The bool return reports whether the cert was self-generated (true) vs
// loaded from disk (false), for the caller's boot log.
func TLSConfig(certFile, keyFile string, hostnames ...string) (*tls.Config, bool, error) {
	if certFile != "" && keyFile != "" && fileExists(certFile) && fileExists(keyFile) {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, false, fmt.Errorf("load mounted keypair %s/%s: %w", certFile, keyFile, err)
		}
		return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, false, nil
	}
	cert, err := selfSignedCert(hostnames...)
	if err != nil {
		return nil, false, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, true, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// selfSignedCert mints an ephemeral in-memory self-signed certificate:
// ECDSA P-256, 1y validity, SAN covering localhost + 127.0.0.1 + ::1 and any
// extra hostnames (the pod/node name). Nothing is written to disk; the key
// lives only in the returned tls.Certificate.
func selfSignedCert(hostnames ...string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate ecdsa key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial: %w", err)
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "adaptive-export-control"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(1, 0, 0), // 1y validity
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	for _, h := range hostnames {
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        &tmpl,
	}, nil
}

// certToPEM renders a tls.Certificate (as produced by selfSignedCert, holding a
// single DER cert + an *ecdsa.PrivateKey) as PEM cert + PEM key bytes — the
// on-disk shape of a mounted /certs/server.{crt,key} keypair.
func certToPEM(cert tls.Certificate) ([]byte, []byte, error) {
	if len(cert.Certificate) == 0 {
		return nil, nil, fmt.Errorf("certToPEM: empty certificate chain")
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	ec, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("certToPEM: private key is not *ecdsa.PrivateKey")
	}
	der, err := x509.MarshalECPrivateKey(ec)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal ec private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	return certPEM, keyPEM, nil
}
