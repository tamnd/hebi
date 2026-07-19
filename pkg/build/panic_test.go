package build

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/tamnd/hebi/pkg/shim"
)

// assertProgramCrashesLikeGo runs a program that ends in an unrecovered panic
// through go run and through hebi and checks that both print the same standard
// output, that hebi exits with status 2 as a panicking Go program does, and that
// hebi's first standard-error line, the panic banner, matches go run's. Only the
// first line is compared, since the goroutine stack that follows carries addresses
// that never match across two runtimes.
func assertProgramCrashesLikeGo(t *testing.T, source string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	file := writeModule(t, source)

	acquireGoRun(t)
	goCmd := exec.CommandContext(t.Context(), "go", "run", file)
	var goOut, goErr bytes.Buffer
	goCmd.Stdout = &goOut
	goCmd.Stderr = &goErr
	if err := goCmd.Run(); err == nil {
		t.Fatalf("go run did not fail, want a panic")
	}

	var out, errb bytes.Buffer
	code, err := Run(context.Background(), file, &out, &errb)
	if err != nil {
		t.Fatalf("Run: %v (stderr: %s)", err, errb.String())
	}
	if code != 2 {
		t.Errorf("hebi exit = %d, want 2 (stderr: %s)", code, errb.String())
	}
	if got, want := out.String(), goOut.String(); got != want {
		t.Errorf("hebi stdout = %q, go run stdout = %q", got, want)
	}
	if got, want := firstLine(errb.String()), firstLine(goErr.String()); got != want {
		t.Errorf("hebi panic line = %q, go run panic line = %q", got, want)
	}
}

// firstLine returns the text up to the first newline, or the whole string when it
// has none.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// TestRecover checks recover end to end against go run. A deferred recover
// swallows an explicit panic so the function returns normally and its caller keeps
// running, a deferred recover swallows a runtime panic and leaves a named result
// at the fallback the deferred call set, a deferred call changes a named result on
// a plain return with no panic at all, and several defers unwind in reverse with
// the recover among them. The idiomatic conditional recover, if r := recover(); r
// != nil, waits on interface values and their comparison to nil, so these cases
// recover unconditionally, which is enough to exercise the unwind.
func TestRecover(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"recover swallows an explicit panic",
			"package main\n\nimport \"fmt\"\n\nfunc safe() {\n\tdefer func() {\n\t\trecover()\n\t}()\n\tpanic(\"boom\")\n}\n\nfunc main() {\n\tsafe()\n\tfmt.Println(\"after\")\n}\n",
		},
		{
			"recover swallows a runtime panic and returns a fallback",
			"package main\n\nimport \"fmt\"\n\nfunc lookup() (v int) {\n\tdefer func() {\n\t\trecover()\n\t\tv = -1\n\t}()\n\ts := []int{10, 20, 30}\n\tv = s[9]\n\treturn v\n}\n\nfunc main() {\n\tfmt.Println(lookup())\n}\n",
		},
		{
			"a deferred call changes a named result with no panic",
			"package main\n\nimport \"fmt\"\n\nfunc inc() (n int) {\n\tdefer func() {\n\t\tn = n + 1\n\t}()\n\treturn 5\n}\n\nfunc main() {\n\tfmt.Println(inc())\n}\n",
		},
		{
			"several defers unwind in reverse with a recover among them",
			"package main\n\nimport \"fmt\"\n\nfunc f() {\n\tdefer fmt.Println(\"first\")\n\tdefer func() {\n\t\trecover()\n\t\tfmt.Println(\"second\")\n\t}()\n\tpanic(\"x\")\n}\n\nfunc main() {\n\tf()\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestPanicCrash checks that an unrecovered panic prints the Go banner and exits
// with status 2, both for a bare panic and for one that first runs a deferred call
// on the way out, so the deferred output lands on standard output before the
// program crashes.
func TestPanicCrash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"a bare panic crashes",
			"package main\n\nfunc main() {\n\tpanic(\"boom\")\n}\n",
		},
		{
			"a deferred call runs before the crash",
			"package main\n\nimport \"fmt\"\n\nfunc f() {\n\tdefer fmt.Println(\"cleanup\")\n\tpanic(\"boom\")\n}\n\nfunc main() {\n\tf()\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramCrashesLikeGo(t, tt.source)
		})
	}
}

// TestPanicRecoverEmit pins the shape of the panic and recover surface: panic
// raises a GoPanic carrying the value, panic with a nil argument raises the
// runtime's PanicNilError, recover lowers to the runtime helper, a reshaped
// deferring function runs under a try whose except drains the defers and re-raises
// only an unrecovered panic, and a module that can panic guards its entry point so
// an escaping panic prints the banner rather than a Python traceback.
func TestPanicRecoverEmit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			"panic raises a GoPanic",
			"package main\n\nfunc main() {\n\tpanic(\"boom\")\n}\n",
			"raise " + shim.Name + ".GoPanic(b\"boom\")\n",
		},
		{
			"panic nil raises PanicNilError",
			"package main\n\nfunc main() {\n\tpanic(nil)\n}\n",
			"raise " + shim.Name + ".GoPanic(" + shim.Name + ".PanicNilError())\n",
		},
		{
			"recover lowers to the runtime helper",
			"package main\n\nfunc f() {\n\tdefer func() {\n\t\trecover()\n\t}()\n}\n\nfunc main() {\n\tf()\n}\n",
			shim.Name + "._recover()",
		},
		{
			"a reshaped body re-raises only an unrecovered panic",
			"package main\n\nfunc inc() (n int) {\n\tdefer func() {\n\t\tn = n + 1\n\t}()\n\treturn 5\n}\n\nfunc main() {\n\t_ = inc()\n}\n",
			"except " + shim.Name + ".GoPanic as _p:\n",
		},
		{
			"a panicking module guards its entry point",
			"package main\n\nfunc main() {\n\tpanic(\"boom\")\n}\n",
			shim.Name + "._crash(_p)\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := emitOf(t, tt.source)
			if !bytesContains(got, tt.want) {
				t.Errorf("emitted main.py = \n%s\nwant it to contain\n%s", got, tt.want)
			}
		})
	}
}
