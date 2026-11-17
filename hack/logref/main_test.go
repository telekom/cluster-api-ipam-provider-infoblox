/*
Copyright 2026 Deutsche Telekom AG.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoggingReferenceUpToDate fails when docs/logging-reference.md does not
// list exactly the log messages, levels and fields of the code.
func TestLoggingReferenceUpToDate(t *testing.T) {
	if err := run(filepath.Join("..", ".."), true); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", `package main

import "log"

func main() {
	log.SetOutput(io.Discard)
	setupLog.Error(err, "start failed", "value", v)
}
`)
	writeFile(t, root, "internal/x/x.go", `package x

func F() {
	logger := log.FromContext(ctx).WithValues("instance", i)
	logger.Info("allocated", "address", a)
	logger.V(1).Info("verified")
	logger.V(2).Info("trace")
	log.FromContext(ctx).Error(err, "failed", "hostname", h)
	http.Error(w, "not a log", 500)
	err.Error()
}
`)
	writeFile(t, root, "internal/x/x_test.go", "package x\nfunc g() { logger.Info(\"test only\", \"unclassified\", 1) }\n")

	// Only the fields of this module's code must be used; drop the others from the check.
	saved := fieldClasses
	t.Cleanup(func() { fieldClasses = saved })
	fieldClasses = map[string]fieldClass{}
	for _, k := range []string{"value", "instance", "address", "hostname"} {
		fieldClasses[k] = saved[k]
	}

	res, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for dir, es := range res.pkgs {
		for _, e := range es {
			got[dir+" "+e.level+" "+e.message] = strings.Join(sortedKeys(e.fields), ",")
		}
	}
	want := map[string]string{
		"main.go error start failed": "value",
		"internal/x info allocated":  "address",
		"internal/x debug verified":  "",
		"internal/x V(2) trace":      "",
		"internal/x error failed":    "hostname",
	}
	if len(got) != len(want) {
		t.Errorf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%q: fields %q (present %t), want %q", k, g, ok, v)
		}
	}

	md := render(res)
	for _, s := range []string{
		"#### `internal/x`",
		"| info | `allocated` | `address` |",
		"| `instance` | `WithValues` | none | none |",
		"| `hostname` | log call | customer-data | tenant-user |",
	} {
		if !strings.Contains(md, s) {
			t.Errorf("rendered reference lacks %q:\n%s", s, md)
		}
	}
}

func TestScanRejects(t *testing.T) {
	for name, tc := range map[string]struct{ file, src, want string }{
		"stdlib log":                 {"pkg/x/x.go", "package x\nimport \"log\"\nfunc f() { log.Print(\"x\") }\n", "pkg/x/x.go"},
		"dynamic message":            {"pkg/x/x.go", "package x\nfunc f() { logger.Info(msg) }\n", "non-literal message msg"},
		"formatted message":          {"pkg/x/x.go", "package x\nfunc f() { logger.Info(fmt.Sprintf(\"%d\", n)) }\n", "non-literal message"},
		"dynamic V level":            {"pkg/x/x.go", "package x\nfunc f() { logger.V(n).Info(\"x\") }\n", "non-literal V level"},
		"logr error without message": {"pkg/x/x.go", "package x\nfunc f() { logger.Error(err) }\n", "too few arguments"},
		"unclassified field":         {"pkg/x/x.go", "package x\nfunc f() { logger.Info(\"x\", \"secret\", s) }\n", `field "secret" has no personal-data classification`},
		"unclassified context field": {"pkg/x/x.go", "package x\nfunc f() { l := logger.WithValues(\"secret\", s) }\n", `field "secret" has no personal-data classification`},
		"stale classification":       {"pkg/x/x.go", "package x\nfunc f() {}\n", `field "subnet" is classified`},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, tc.file, tc.src)
			if _, err := scan(root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("scan error %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestRunCheckDetectsDrift(t *testing.T) {
	root := t.TempDir()
	saved := fieldClasses
	t.Cleanup(func() { fieldClasses = saved })
	fieldClasses = map[string]fieldClass{}
	writeFile(t, root, "pkg/x/x.go", "package x\nfunc f() { logger.Info(\"hello\") }\n")
	writeFile(t, root, docPath, "# Ref\n"+beginMark+"\n"+endMark+"\n")
	if err := run(root, true); err == nil || !strings.Contains(err.Error(), "out of date") {
		t.Fatalf("check on a stale doc: %v", err)
	}
	if err := run(root, false); err != nil {
		t.Fatal(err)
	}
	if err := run(root, true); err != nil {
		t.Fatalf("check after regeneration: %v", err)
	}
	doc, err := os.ReadFile(filepath.Join(root, docPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "`hello`") {
		t.Fatalf("regenerated doc lacks the message:\n%s", doc)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
