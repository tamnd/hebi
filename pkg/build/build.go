// Package build is the driver that turns a Go input into runnable Python: it
// loads and type-checks the package, lowers it to the IR, verifies the tree,
// emits the module source, and writes it beside the embedded runtime shim. Run
// builds into a temporary directory and executes the result under CPython,
// forwarding its output and exit status.
package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tamnd/hebi/pkg/emit"
	"github.com/tamnd/hebi/pkg/frontend"
	"github.com/tamnd/hebi/pkg/ir"
	"github.com/tamnd/hebi/pkg/shim"
)

// mainModule is the file name the emitted package module is written under, and
// the entry point Run hands to CPython.
const mainModule = "main.py"

// Result reports what a build wrote.
type Result struct {
	// Dir is the directory the files were written to.
	Dir string
	// Module is the base name of the emitted entry module within Dir.
	Module string
	// Files are the base names of every file written, in a stable order.
	Files []string
}

// Build compiles the Go package at input and writes the emitted Python and the
// runtime shim into outDir, creating it if needed.
func Build(input, outDir string) (*Result, error) {
	pkg, err := frontend.Load(input)
	if err != nil {
		return nil, err
	}
	module, err := lower(pkg)
	if err != nil {
		return nil, err
	}
	if err := ir.Verify(module); err != nil {
		return nil, err
	}
	src, err := emit.Module(module)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	shimFile := shim.Name + ".py"
	if err := os.WriteFile(filepath.Join(outDir, shimFile), []byte(shim.Source()), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outDir, mainModule), []byte(src), 0o644); err != nil {
		return nil, err
	}
	return &Result{Dir: outDir, Module: mainModule, Files: []string{mainModule, shimFile}}, nil
}

// Run compiles input into a temporary directory and executes it under CPython,
// streaming stdout and stderr through. It returns the program's exit code; the
// returned error is non-nil only when the build or the launch itself fails, not
// when the program exits non-zero.
func Run(ctx context.Context, input string, stdout, stderr io.Writer) (int, error) {
	dir, err := os.MkdirTemp("", "hebi-run-*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	res, err := Build(input, dir)
	if err != nil {
		return 0, err
	}
	py, err := pythonPath()
	if err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, py, res.Module)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	if err == nil {
		return 0, nil
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode(), nil
	}
	return 0, err
}

// pythonPath finds the interpreter to run emitted modules with, preferring
// python3 and falling back to python.
func pythonPath() (string, error) {
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no python interpreter found on PATH (looked for python3 and python)")
}
