/*
Copyright 2023 Deutsche Telekom AG.

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
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// logOutput parses args with the log flags and returns what a logger built from them writes
// for one line at each level.
func logOutput(t *testing.T, args ...string) (string, error) {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o zap.Options
	bindLogFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	o.DestWriter = &buf
	logger := zap.New(zap.UseFlagOptions(&o))
	logger.V(1).Info("debug line")
	logger.Info("info line", "key", "value")
	logger.Error(errors.New("boom"), "error line")
	return buf.String(), nil
}

func TestLogFlagDefaults(t *testing.T) {
	out, err := logOutput(t)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "debug line") {
		t.Errorf("default level logs debug lines:\n%s", out)
	}
	if !strings.Contains(out, "\tINFO\tinfo line\t{\"key\": \"value\"}") || !strings.Contains(out, "error line") {
		t.Errorf("default output is not info-level text:\n%s", out)
	}
	if strings.Contains(out, "goroutine") || strings.Contains(out, "main_test.go") {
		t.Errorf("default output has a stack trace:\n%s", out)
	}
}

func TestLogLevelAndFormat(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string // messages in the output, in order
	}{
		{[]string{"--log-level=debug"}, []string{"debug line", "info line", "error line"}},
		{[]string{"--log-level=info"}, []string{"info line", "error line"}},
		{[]string{"--log-level=warn"}, []string{"error line"}},
		{[]string{"--log-level=error"}, []string{"error line"}},
		{[]string{"--zap-log-level=debug", "--log-level=error"}, []string{"error line"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, err := logOutput(t, append(tc.args, "--log-format=json")...)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				var rec struct{ Msg string }
				if err := json.Unmarshal([]byte(line), &rec); err != nil {
					t.Fatalf("line is not JSON: %q", line)
				}
				got = append(got, rec.Msg)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("messages %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLogFlagsRejectUnknownValues(t *testing.T) {
	for _, arg := range []string{"--log-level=warning", "--log-level=INFO", "--log-level=2", "--log-format=console", "--log-format=logfmt"} {
		if _, err := logOutput(t, arg); err == nil {
			t.Errorf("%s was accepted", arg)
		}
	}
}
