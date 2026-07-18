package build

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestClosures checks the closure surface against go run: a read-only capture, a
// write-back through nonlocal, a closure returned and called later, a closure
// that returns a closure, an immediately invoked literal both as a value and for
// its side effect, the lambda form, the multi-statement def form, a closure
// passed as an argument, and nested closures, each matching go run exactly.
func TestClosures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{"read only capture", "n := 10\n\tadd := func(x int) int { return x + n }\n\tfmt.Println(add(5))"},
		{"nonlocal write back", "count := 0\n\tinc := func() { count++ }\n\tinc()\n\tinc()\n\tfmt.Println(count)"},
		{"closure returned then called", "adder := func(base int) func(int) int {\n\t\treturn func(x int) int { return x + base }\n\t}\n\ta := adder(100)\n\tfmt.Println(a(1), a(2))"},
		{"immediately invoked value", "r := func(a, b int) int { return a * b }(6, 7)\n\tfmt.Println(r)"},
		{"immediately invoked side effect", "x := 0\n\tfunc() { x = 9 }()\n\tfmt.Println(x)"},
		{"lambda form", "k := 3\n\tid := func() int { return k }\n\tfmt.Println(id())"},
		{"multi statement def", "acc := 0\n\tf := func(v int) int {\n\t\tacc += v\n\t\treturn acc\n\t}\n\tfmt.Println(f(1), f(2), f(3))"},
		{"closure as argument", "apply := func(f func(int) int, x int) int { return f(x) }\n\tfmt.Println(apply(func(y int) int { return y * 2 }, 21))"},
		{"nested closures", "a := 1\n\touter := func() func() int {\n\t\tb := 10\n\t\treturn func() int { return a + b }\n\t}\n\tinner := outer()\n\tfmt.Println(inner())"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesGo(t, tt.body)
		})
	}
}

// TestClosureLoopVarModern checks the Go 1.22 per-iteration loop variable: a
// closure made in each iteration of a three-clause count and of a range over an
// integer captures the value that iteration held, so three closures kept in
// separate variables return the distinct values, matching go run under the
// current language version. The closures land in named variables through a
// switch so the test avoids indexing a slice of functions, which a later slice
// covers.
func TestClosureLoopVarModern(t *testing.T) {
	t.Parallel()
	const head = "f0 := func() int { return -1 }\n\tf1 := func() int { return -1 }\n\tf2 := func() int { return -1 }\n\t"
	const tail = "\n\tswitch i {\n\tcase 0:\n\t\tf0 = func() int { return i }\n\tcase 1:\n\t\tf1 = func() int { return i }\n\tcase 2:\n\t\tf2 = func() int { return i }\n\t}\n\t}\n\tfmt.Println(f0(), f1(), f2())"
	tests := []struct {
		name string
		body string
	}{
		{"three clause count", head + "for i := 0; i < 3; i++ {" + tail},
		{"range over int", head + "for i := range 3 {" + tail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesGo(t, tt.body)
		})
	}
}

// TestClosureLoopVarOldSemantics pins the pre-1.22 shared loop variable: three
// closures made across a three-clause count all capture the one shared variable,
// so each returns the value it holds after the loop. hebi reads the module's go
// 1.21 directive and lowers the count to the while form so the shared variable
// reaches that value, matching go run.
func TestClosureLoopVarOldSemantics(t *testing.T) {
	t.Parallel()
	source := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tf0 := func() int { return -1 }\n\tf1 := func() int { return -1 }\n\tf2 := func() int { return -1 }\n\tfor i := 0; i < 3; i++ {\n\t\tswitch i {\n\t\tcase 0:\n\t\t\tf0 = func() int { return i }\n\t\tcase 1:\n\t\t\tf1 = func() int { return i }\n\t\tcase 2:\n\t\t\tf2 = func() int { return i }\n\t\t}\n\t}\n\tfmt.Println(f0(), f1(), f2())\n}\n"
	assertProgramMatchesGoVersion(t, source, "go 1.21")
}

// TestClosuresEmit pins the Python each closure shape lowers to: a captured
// lambda, a hoisted def with a nonlocal declaration, a per-iteration default
// argument snapshot inside a loop, and an immediately invoked literal hoisted to
// a def the call then names.
func TestClosuresEmit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			"lambda capture",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tk := 3\n\tid := func() int { return k }\n\tfmt.Println(id())\n}\n",
			"def main():\n    k = 3\n    id = lambda: k\n    _hebirt.println(id())\n",
		},
		{
			"nonlocal def",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tcount := 0\n\tset := func() { count = 5 }\n\tset()\n\tfmt.Println(count)\n}\n",
			"def main():\n    count = 0\n    def _func():\n        nonlocal count\n        count = 5\n    set = _func\n    set()\n    _hebirt.println(count)\n",
		},
		{
			"per iteration snapshot",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tf := func() int { return 0 }\n\tfor i := 0; i < 3; i++ {\n\t\tf = func() int { return i }\n\t}\n\tfmt.Println(f())\n}\n",
			"def main():\n    f = lambda: 0\n    for i in range(3):\n        f = lambda i=i: i\n    _hebirt.println(f())\n",
		},
		{
			"immediately invoked def",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tr := func() int { return 7 }()\n\tfmt.Println(r)\n}\n",
			"def main():\n    def _func():\n        return 7\n    r = _func()\n    _hebirt.println(r)\n",
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

// writeModuleVersion drops a single-file Go module with the given go directive
// into a fresh temp dir, so a test can pin the language version the frontend and
// go run both read, and returns the path to the source file.
func writeModuleVersion(t *testing.T, source, goLine string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/prog\n\n"+goLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "prog.go")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

// assertProgramMatchesGoVersion runs a whole program through go run and through
// hebi under a chosen go directive, and requires the two to agree. It is how the
// loop-variable semantics test fixes an older language version.
func assertProgramMatchesGoVersion(t *testing.T, source, goLine string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	file := writeModuleVersion(t, source, goLine)

	// Run the whole module so go honors the go directive that fixes the loop
	// variable semantics. Passing the file path directly would build it as a
	// standalone file under the toolchain default and ignore the directive.
	goCmd := exec.CommandContext(t.Context(), "go", "run", ".")
	goCmd.Dir = filepath.Dir(file)
	goOut, err := goCmd.Output()
	if err != nil {
		t.Fatalf("go run: %v", err)
	}

	var out, errb bytes.Buffer
	code, err := Run(context.Background(), file, &out, &errb)
	if err != nil {
		t.Fatalf("Run: %v (stderr: %s)", err, errb.String())
	}
	if code != 0 {
		t.Fatalf("hebi exit = %d (stderr: %s)", code, errb.String())
	}
	if got, want := out.String(), string(goOut); got != want {
		t.Errorf("hebi output = %q, go run output = %q", got, want)
	}
}

func bytesContains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}
