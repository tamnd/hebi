package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceEmbedded(t *testing.T) {
	t.Parallel()
	src := Source()
	if strings.TrimSpace(src) == "" {
		t.Fatal("embedded shim source is empty")
	}
	for _, want := range []string{"def go_str", "def println", "true", "false"} {
		if !strings.Contains(src, want) {
			t.Errorf("shim source missing %q", want)
		}
	}
}

// TestShimBehavior runs the embedded shim under CPython and checks it formats
// values the Go way: booleans as true and false, operands space-joined with a
// trailing newline, matching fmt.Println. It is skipped where python3 is not
// on the path so the unit suite stays runnable without a toolchain.
func TestShimBehavior(t *testing.T) {
	t.Parallel()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Name+".py"), []byte(Source()), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), py, "-c", "import _hebirt; _hebirt.println(1, True, False, \"hi\")")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run shim: %v\n%s", err, out)
	}
	if got, want := string(out), "1 true false hi\n"; got != want {
		t.Errorf("println output = %q, want %q", got, want)
	}
}
