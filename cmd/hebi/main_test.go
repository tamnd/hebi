package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{"version prints the version", []string{"version"}, 0, version + "\n", ""},
		{"no args is a usage error", nil, 2, "", "usage: hebi"},
		{"unknown command", []string{"nope"}, 2, "", "unknown command"},
		{"build without input is a usage error", []string{"build"}, 2, "", "usage: hebi build"},
		{"build of a missing input fails", []string{"build", "does-not-exist.go"}, 1, "", "hebi:"},
		{"run without input is a usage error", []string{"run"}, 2, "", "usage: hebi run"},
		{"run of a missing input fails", []string{"run", "does-not-exist.go"}, 1, "", "hebi:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out, errb bytes.Buffer
			code := run(context.Background(), tt.args, &out, &errb)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, errb.String())
			}
			if tt.wantOut != "" && out.String() != tt.wantOut {
				t.Errorf("stdout = %q, want %q", out.String(), tt.wantOut)
			}
			if tt.wantErr != "" && !strings.Contains(errb.String(), tt.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errb.String(), tt.wantErr)
			}
		})
	}
}
