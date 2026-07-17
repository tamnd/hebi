package frontend

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeModule writes a one-file module into a fresh temp directory and returns
// the directory. The go.mod pins the same Go line hebi builds with so go list
// runs in a real module context.
func writeModule(t *testing.T, name, mainSrc string) string {
	t.Helper()
	dir := t.TempDir()
	must := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	must("go.mod", "module "+name+"\n\ngo 1.26.5\n")
	must("main.go", mainSrc)
	return dir
}

const helloMain = `package main

import "fmt"

func main() {
	fmt.Println("hi")
}
`

func TestLoadHello(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, "hello", helloMain)

	pkg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if pkg.Name != "main" {
		t.Errorf("Name = %q, want main", pkg.Name)
	}
	if pkg.Types == nil || pkg.Info == nil || pkg.Fset == nil {
		t.Fatal("Load returned incomplete type information")
	}
	if len(pkg.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(pkg.Files))
	}
	if len(pkg.Info.Defs) == 0 {
		t.Error("Info.Defs is empty, expected the main func to be defined")
	}
	// The type of the "hi" argument literal must be known and be a string,
	// which proves the type info is wired to the syntax, not just present.
	lit := findStringLit(t, pkg.Files[0], `"hi"`)
	got := pkg.Info.TypeOf(lit)
	if got == nil || !strings.Contains(got.String(), "string") {
		t.Errorf("TypeOf(%q) = %v, want a string type", lit.Value, got)
	}
}

func TestLoadSingleFile(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, "hello", helloMain)

	pkg, err := Load(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatalf("Load file: %v", err)
	}
	if pkg.Name != "main" {
		t.Errorf("Name = %q, want main", pkg.Name)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		main    string
		wantSub string
	}{
		{
			name:    "type error",
			main:    "package main\n\nfunc main() {\n\tvar x int = \"nope\"\n\t_ = x\n}\n",
			wantSub: "frontend:",
		},
		{
			name:    "undefined identifier",
			main:    "package main\n\nfunc main() {\n\tprintln(missing)\n}\n",
			wantSub: "frontend:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := writeModule(t, "broken", tt.main)
			_, err := Load(dir)
			if err == nil {
				t.Fatal("Load succeeded, want a type error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantSub)
			}
		})
	}
}

func TestLoadBadPath(t *testing.T) {
	t.Parallel()
	if _, err := Load(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("Load of a missing path succeeded, want an error")
	}
	notGo := filepath.Join(t.TempDir(), "readme.txt")
	if err := os.WriteFile(notGo, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(notGo); err == nil {
		t.Error("Load of a non-.go file succeeded, want an error")
	}
}

// findStringLit returns the basic literal in the file whose source value is
// value, failing the test if there is none. It skips import paths, which are
// literals go/types does not record a type for.
func findStringLit(t *testing.T, file *ast.File, value string) *ast.BasicLit {
	t.Helper()
	var found *ast.BasicLit
	ast.Inspect(file, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if lit, ok := n.(*ast.BasicLit); ok && lit.Value == value {
			found = lit
			return false
		}
		return true
	})
	if found == nil {
		t.Fatalf("no basic literal %s in file", value)
	}
	return found
}
