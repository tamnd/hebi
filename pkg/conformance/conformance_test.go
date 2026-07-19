package conformance

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestFixtures is the differential band: every fixture under testdata must
// produce the same stdout and exit code through go run and through the compiled
// Python tier. It covers the M0 band 0001-0050 and the M1 band 0051 onward,
// scalars and control flow, the M2 band 0301 onward, the aggregates, structs
// and arrays and slices and maps and composite literals and addressable element
// access, and the M3 band 0601 onward, the function and error world, multiple
// and named returns, closures, pointers as cells, method sets, defer, panic and
// recover, and the errors package, and the M4 band 0851 onward, the interface and
// generics world, interface values and method dispatch, embedding promotion, type
// assertions and type switches, the typed-nil trap, the empty interface, generics
// erasure, and constraint-directed division and remainder. Each fixture runs
// under the no-deadlock smoke bound, so a compiled program that never finishes
// fails as a deadlock in a minute rather than hanging the suite. It skips where
// the go tool or python3 is not on the path.
func TestFixtures(t *testing.T) {
	t.Parallel()
	requireTools(t)
	fixtures, err := filepath.Glob("testdata/fixtures/*.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures found")
	}
	for _, path := range fixtures {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := DifferentialSmoke(t.Context(), string(source), SmokeTimeout); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		})
	}
}

// TestObservationsCompose checks each runner in isolation: RunGo and
// RunCompiled must both observe the same simple program the same way, so a
// disagreement in TestFixtures points at the emitter rather than the harness.
func TestObservationsCompose(t *testing.T) {
	t.Parallel()
	requireTools(t)
	got, err := RunGo(t.Context(), "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(7)\n}\n")
	if err != nil {
		t.Fatalf("RunGo: %v", err)
	}
	if got.Stdout != "7\n" || got.Exit != 0 {
		t.Errorf("go observation = %+v, want {Stdout:\"7\\n\" Exit:0}", got)
	}
	compiled, err := RunCompiled(t.Context(), "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(7)\n}\n")
	if err != nil {
		t.Fatalf("RunCompiled: %v", err)
	}
	if compiled != got {
		t.Errorf("compiled observation %+v disagreed with go %+v", compiled, got)
	}
}

func requireTools(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
}
