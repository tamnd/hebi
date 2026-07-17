package main

import (
	"bytes"
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
		{"build is stubbed", []string{"build", "x.go"}, 1, "", "not wired up yet"},
		{"run is stubbed", []string{"run", "x.go"}, 1, "", "not wired up yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out, errb bytes.Buffer
			code := run(tt.args, &out, &errb)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
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
