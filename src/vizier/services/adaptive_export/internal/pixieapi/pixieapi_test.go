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

package pixieapi

import (
	"os"
	"testing"
)

// The direct-mode constructors are the #36 broker-direct entry points (AE bypasses
// the cloud passthrough → immune to the "cluster is not in a healthy state" gate).
// These guards are what stop a misconfigured operator from crashing at first Query
// (pxapi log.Fatal's on cluster.local without PX_DISABLE_TLS), so they must hold.

func clearDirectEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"ADAPTIVE_VIZIER_DIRECT_ADDR", "PL_JWT_SIGNING_KEY", "PX_DISABLE_TLS"} {
		t.Setenv(k, "") // t.Setenv records + restores; "" then Unsetenv for a clean slate
		os.Unsetenv(k)
	}
}

func TestNewDirectFromEnv_MissingAddr(t *testing.T) {
	clearDirectEnv(t)
	if _, err := NewDirectFromEnv("cid"); err == nil {
		t.Fatal("expected error when ADAPTIVE_VIZIER_DIRECT_ADDR is unset")
	}
}

func TestNewDirectFromEnv_MissingSigningKey(t *testing.T) {
	clearDirectEnv(t)
	t.Setenv("ADAPTIVE_VIZIER_DIRECT_ADDR", "vizier-query-broker-svc.pl.svc.cluster.local:50300")
	if _, err := NewDirectFromEnv("cid"); err == nil {
		t.Fatal("expected error when PL_JWT_SIGNING_KEY is unset")
	}
}

// TestNewDirect_NoEnvGate — direct dial now uses pxapi.WithDirectTLSSkipVerify
// (PR #49 b523ce362), which doesn't read PX_DISABLE_TLS at all. NewDirect
// must therefore accept any addr regardless of env.
func TestNewDirect_NoEnvGate(t *testing.T) {
	clearDirectEnv(t)
	for _, addr := range []string{
		"vizier-query-broker-svc.pl.svc.cluster.local:50300",
		"vizier.example:50300",
		"10.42.0.5:50300",
	} {
		a, err := NewDirect("cid", DirectOptions{VizierAddr: addr, SigningKey: "k"})
		if err != nil {
			t.Fatalf("NewDirect(%q): %v", addr, err)
		}
		if a.directOpts == nil {
			t.Fatalf("direct-mode Adapter must carry directOpts (so Query takes the broker path)")
		}
		if a.client != nil {
			t.Error("direct-mode Adapter must NOT hold a cloud client (it dials per-query)")
		}
		if a.directOpts.ServiceID != "adaptive_export" {
			t.Errorf("ServiceID should default to adaptive_export, got %q", a.directOpts.ServiceID)
		}
	}
}

func TestNewDirectFromEnv_Success(t *testing.T) {
	clearDirectEnv(t)
	t.Setenv("ADAPTIVE_VIZIER_DIRECT_ADDR", "vizier-query-broker-svc.pl.svc.cluster.local:50300")
	t.Setenv("PL_JWT_SIGNING_KEY", "signing-key")
	t.Setenv("PX_DISABLE_TLS", "1")
	a, err := NewDirectFromEnv("cluster-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.directOpts == nil || a.clusterID != "cluster-123" {
		t.Fatalf("expected direct Adapter for cluster-123, got %+v", a)
	}
	if a.directOpts.VizierAddr == "" || a.directOpts.SigningKey != "signing-key" {
		t.Errorf("directOpts not populated from env: %+v", a.directOpts)
	}
}

// New (cloud) path stays cloud — sanity that the two constructors don't cross-wire.
func TestNewCloudHasNoDirectOpts(t *testing.T) {
	a := New(nil, "cid")
	if a.directOpts != nil {
		t.Error("cloud Adapter must not have directOpts")
	}
}
