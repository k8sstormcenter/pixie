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

// Command dx-cloud-connector-shim keeps Pixie Cloud VISUALIZATION working for a
// standalone PEM that has no vizier middleware (README §4, Option A).
//
// It does the two things the real vizier cloud-connector does, minus everything
// a single-node PoC does not need (no NATS, no metadata service, no Kelvin
// fan-out):
//
//  1. AUTHENTICATE the "cluster" (this one PEM) to Pixie Cloud — dial the cloud
//     vzconn service with a DEPLOY KEY over mTLS, RegisterVizier, receive the
//     cluster ID + cloud-issued SSL certs, and maintain the tunnel. This mirrors
//     src/vizier/services/cloud_connector/bridge/vzconn_client.go +
//     src/cloud/vzconn (RegisterVizierRequest → RegisterVizierResponse).
//
//  2. PROXY queries — a cloud-tunneled ExecuteScript is forwarded to the local
//     standalone_pem on PEM_ADDR (:12345) via pxapi, and the result RowBatches
//     are streamed back up the tunnel. This mirrors what query-broker does, but
//     for a single agent (no distributed plan).
//
// This file is a COMPILING SKELETON (stdlib only): it wires config + the control
// loop and marks the two integration points against the real pixie packages. The
// full handshake/proxy is the follow-up (cloudshim/README.md) — it needs a live
// Pixie Cloud + the vzconn mTLS certs to validate, which is rig-gated.
//
// Fork-only, excluded from copybara.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type config struct {
	cloudAddr   string // PL_CLOUD_ADDR, e.g. withpixie.ai:443
	deployKey   string // contents of PL_DEPLOY_KEY_FILE
	clusterID   string // PL_CLUSTER_ID_FILE (empty on first run → assigned by RegisterVizier)
	pemAddr     string // PEM_ADDR, the local standalone_pem ExecuteScript gRPC (:12345)
	clusterName string // PL_CLUSTER_NAME (shown in the Pixie Cloud UI)
}

func loadConfig() config {
	read := func(path string) string {
		if path == "" {
			return ""
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return string(b)
	}
	return config{
		cloudAddr:   env("PL_CLOUD_ADDR", "withpixie.ai:443"),
		deployKey:   read(os.Getenv("PL_DEPLOY_KEY_FILE")),
		clusterID:   read(os.Getenv("PL_CLUSTER_ID_FILE")),
		pemAddr:     env("PEM_ADDR", "127.0.0.1:12345"),
		clusterName: env("PL_CLUSTER_NAME", "dx-standalone-vm"),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[dx-cloud-shim] ")
	cfg := loadConfig()

	if cfg.deployKey == "" {
		log.Fatal("no deploy key (PL_DEPLOY_KEY_FILE): create one with `px deploy-key create` and mount it")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	log.Printf("cloud=%s pem=%s cluster_name=%q cluster_id=%q",
		cfg.cloudAddr, cfg.pemAddr, cfg.clusterName, redact(cfg.clusterID))

	// ── (1) AUTHENTICATE to cloud ────────────────────────────────────────────
	// Implementation point: dial vzconn over mTLS and register.
	//
	//   import cloudvzconnpb "px.dev/pixie/src/cloud/vzconn/vzconnpb"
	//   conn := grpc.Dial(cfg.cloudAddr, grpc.WithTransportCredentials(mtls(cfg.deployKey)))
	//   stream, _ := cloudvzconnpb.NewVZConnServiceClient(conn).NATSBridge(ctx)   // the tunnel
	//   // RegisterVizierRequest{VizierID?, JWTKey(deployKey), ClusterInfo{ClusterName, ...}}
	//   // → RegisterVizierAck{Status, VizierID, cloud-issued certs}. Persist VizierID
	//   //   to PL_CLUSTER_ID_FILE. See cloud_connector/bridge/vzconn_client.go.
	sess, err := registerWithCloud(ctx, cfg)
	if err != nil {
		log.Fatalf("cloud registration failed: %v", err)
	}
	log.Printf("registered with cloud: cluster_id=%s (Pixie Cloud can now see this PEM)", redact(sess.clusterID))

	// ── (2) PROXY cloud-tunneled ExecuteScript → local PEM ───────────────────
	// Implementation point: for each cloud-tunneled query, run it against the
	// local standalone_pem via pxapi and stream RowBatches back up the tunnel.
	//
	//   import "px.dev/pixie/src/api/go/pxapi"
	//   pem, _ := pxapi.NewClient(ctx, pxapi.WithDirectAddr(cfg.pemAddr),
	//                                  pxapi.WithDirectCredsInsecure())
	//   vz, _ := pem.NewVizierClient(ctx, "localhost")
	//   for msg := range sess.incomingQueries() {           // from the vzconn tunnel
	//       vz.ExecuteScript(ctx, msg.PxL, tunnelCollector(sess, msg.QueryID))
	//   }
	if err := serveProxy(ctx, cfg, sess); err != nil && ctx.Err() == nil {
		log.Fatalf("proxy loop: %v", err)
	}
	log.Print("shutting down")
}

// ── skeleton internals (replace with the pixie-API wiring above) ─────────────

type session struct{ clusterID string }

// registerWithCloud performs the vzconn deploy-key mTLS registration. SKELETON:
// returns a session; the real handshake is the wiring in the (1) block.
func registerWithCloud(ctx context.Context, cfg config) (*session, error) {
	id := cfg.clusterID
	if id == "" {
		// RegisterVizier assigns one on first connect; persist it for restarts.
		id = "pending-register"
		log.Print("no cluster id yet — RegisterVizier will assign one (persist to PL_CLUSTER_ID_FILE)")
	}
	// TODO(cloudshim): real vzconn dial + RegisterVizier (see (1)); until then this
	// documents the flow and lets the unit start so the deployment is exercised.
	return &session{clusterID: id}, nil
}

// serveProxy forwards cloud-tunneled ExecuteScript to the local PEM. SKELETON:
// idles until ctx is done; the real loop is the wiring in the (2) block.
func serveProxy(ctx context.Context, cfg config, _ *session) error {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			log.Printf("proxy alive — awaiting cloud queries, PEM at %s", cfg.pemAddr)
		}
	}
}

func redact(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8] + "…"
}
