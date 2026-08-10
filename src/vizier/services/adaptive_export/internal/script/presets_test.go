// Copyright 2018- The Pixie Authors.
// SPDX-License-Identifier: Apache-2.0

package script

import (
	"strings"
	"testing"
)

func chDcSnoop(t *testing.T) string {
	t.Helper()
	for _, p := range DarkVectorPresets() {
		if p.Name == "ch-dc_snoop" {
			return p.Script
		}
	}
	t.Fatal("ch-dc_snoop preset not found")
	return ""
}

func TestDcSnoopExclusionDefault(t *testing.T) {
	s := chDcSnoop(t)
	if strings.Contains(s, "#__DC_SNOOP_EXCLUSION__") {
		t.Fatal("exclusion placeholder was not substituted")
	}
	// Child-namespace drops present for infra namespaces.
	for _, want := range []string{
		"df = df[df.namespace != 'honey']",
		"df = df[df.namespace != 'pl']",
		"df = df[df.namespace != 'clickhouse']",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("default namespace filter missing: %s", want)
		}
	}
	// The hardcoded comm blocklist was DELETED (pure-ancestry cleanup): the retention
	// script must emit NO comm-based drops.
	if strings.Contains(s, "df.comm !=") {
		t.Error("comm blocklist must be gone from dc_snoop.pxl (no df.comm != drops)")
	}
	if strings.Contains(s, "px.contains(df.comm") {
		t.Error("kernel-thread substring filter must be gone from dc_snoop.pxl")
	}
	if strings.Contains(s, "df = df[df.namespace != '']") {
		t.Error("must NOT drop blank-namespace rows")
	}
}

func TestDcSnoopParentExclusion(t *testing.T) {
	s := chDcSnoop(t)
	if strings.Contains(s, "# __DC_SNOOP_PARENT_EXCLUSION__") {
		t.Fatal("parent-exclusion placeholder was not substituted")
	}
	// The ppid-ancestry filter drops on parent_namespace, using the same namespace
	// list as the self filter.
	for _, want := range []string{
		"df = df[df.parent_namespace != 'pl']",
		"df = df[df.parent_namespace != 'honey']",
		"df = df[df.parent_namespace != 'clickhouse']",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("parent ancestry filter missing: %s", want)
		}
	}
	// Never drop rows whose parent namespace is blank (host/transient/unseen
	// parents are kept — a blank parent is not proven own-stack).
	if strings.Contains(s, "df = df[df.parent_namespace != '']") {
		t.Error("must NOT drop blank-parent-namespace rows")
	}
	// The parent join must resolve ppid -> parent's namespace.
	for _, want := range []string{
		"par.parent_namespace = par.ctx['namespace']",
		"par.ppid = px.upid_to_pid(par.upid)",
		"left_on=['ppid'], right_on=['ppid']",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("parent join step missing: %s", want)
		}
	}
}

func TestDcSnoopParentExclusionConfigurable(t *testing.T) {
	t.Setenv("DC_SNOOP_EXCLUDE_NAMESPACES", "foo, bar")
	s := chDcSnoop(t)
	if !strings.Contains(s, "df = df[df.parent_namespace != 'foo']") ||
		!strings.Contains(s, "df = df[df.parent_namespace != 'bar']") {
		t.Error("DC_SNOOP_EXCLUDE_NAMESPACES override not applied to parent filter")
	}
	if strings.Contains(s, "df = df[df.parent_namespace != 'pl']") {
		t.Error("env override should replace, not append to, the default namespace list")
	}
}

// TestDcSnoopNoCommMachinery — the DC_SNOOP_EXCLUDE_COMMS env override and its
// backing list are gone; setting the (now-defunct) var must NOT reintroduce any
// comm drop into the retention script.
func TestDcSnoopNoCommMachinery(t *testing.T) {
	t.Setenv("DC_SNOOP_EXCLUDE_COMMS", "foo,bar")
	s := chDcSnoop(t)
	if strings.Contains(s, "df.comm !=") {
		t.Error("DC_SNOOP_EXCLUDE_COMMS must be a no-op now (pure ancestry); got comm drops in script")
	}
}
