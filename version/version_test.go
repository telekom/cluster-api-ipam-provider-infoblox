// Copyright 2024 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0.

package version

import (
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	info := Get()
	if info.GoVersion == "" {
		t.Error("expected non-empty GoVersion")
	}
	if info.Platform == "" {
		t.Error("expected non-empty Platform")
	}
	if !strings.Contains(info.Platform, "/") {
		t.Errorf("expected Platform to contain '/', got %q", info.Platform)
	}
	if info.Compiler == "" {
		t.Error("expected non-empty Compiler")
	}
}

func TestString(t *testing.T) {
	info := Info{GitVersion: "v1.2.3"}
	if info.String() != "v1.2.3" {
		t.Errorf("expected %q, got %q", "v1.2.3", info.String())
	}

	empty := Info{}
	if empty.String() != "" {
		t.Errorf("expected empty string, got %q", empty.String())
	}
}
