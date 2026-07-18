package conformance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/tamnd/hebi/pkg/build"
)

// goRunTokens bounds how many go run oracles compile at once. Every fixture
// spawns go run in its own parallel subtest, and go run drives the Go compiler
// and linker, which are memory heavy. Left unbounded, a wide t.Parallel fan-out
// across the whole fixture band launches one compile per core at peak, which
// spikes memory far past what the small program sizes suggest, especially under
// the race detector. A token per allowed compile caps that peak while leaving
// the cheaper compiled and python steps free to run.
var goRunTokens = make(chan struct{}, maxGoRun())

// maxGoRun is the compile ceiling: a small fraction of the cores so the oracle
// stays parallel enough to be quick without letting concurrent compiles stack
// up. It never drops below two so a single-core runner still overlaps a compile
// with a python run.
func maxGoRun() int {
	n := runtime.GOMAXPROCS(0) / 2
	if n < 2 {
		return 2
	}
	return n
}

// Observation is a program run's observable behavior: what the differential
// oracle compares across tiers. At M0 that is standard output and the exit
// code; the surfaced error joins them once panics and os.Exit are lowered.
type Observation struct {
	Stdout string
	Exit   int
}

// module is the go.mod written around a fixture so the go tool and the frontend
// can both load it as a package.
const module = "module hebi.test/fixture\n\ngo 1.26\n"

// stage writes a fixture's source into dir as a one-file module and returns the
// path to the source file.
func stage(dir, source string) (string, error) {
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0o644); err != nil {
		return "", err
	}
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		return "", err
	}
	return src, nil
}

// RunGo observes a fixture under the go tool with go run. It is the oracle every
// other tier is checked against.
func RunGo(ctx context.Context, source string) (Observation, error) {
	select {
	case goRunTokens <- struct{}{}:
		defer func() { <-goRunTokens }()
	case <-ctx.Done():
		return Observation{}, ctx.Err()
	}
	dir, err := os.MkdirTemp("", "hebi-go-*")
	if err != nil {
		return Observation{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if _, err := stage(dir, source); err != nil {
		return Observation{}, err
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	code, err := exitCode(cmd.Run())
	if err != nil {
		return Observation{}, err
	}
	return Observation{Stdout: out.String(), Exit: code}, nil
}

// RunCompiled observes a fixture through the compiled tier: build the source to
// Python and run it under CPython.
func RunCompiled(ctx context.Context, source string) (Observation, error) {
	dir, err := os.MkdirTemp("", "hebi-compiled-*")
	if err != nil {
		return Observation{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	src, err := stage(dir, source)
	if err != nil {
		return Observation{}, err
	}
	var out bytes.Buffer
	code, err := build.Run(ctx, src, &out, io.Discard)
	if err != nil {
		return Observation{}, err
	}
	return Observation{Stdout: out.String(), Exit: code}, nil
}

// Differential runs a fixture through every tier and reports the first
// disagreement with the go oracle. A nil error means the tiers agree, which is
// the invariant a fixture must hold to merge.
func Differential(ctx context.Context, source string) error {
	oracle, err := RunGo(ctx, source)
	if err != nil {
		return fmt.Errorf("go oracle: %w", err)
	}
	compiled, err := RunCompiled(ctx, source)
	if err != nil {
		return fmt.Errorf("compiled tier: %w", err)
	}
	if compiled.Stdout != oracle.Stdout {
		return fmt.Errorf("stdout mismatch:\n  go run:   %q\n  compiled: %q", oracle.Stdout, compiled.Stdout)
	}
	if compiled.Exit != oracle.Exit {
		return fmt.Errorf("exit mismatch: go run %d, compiled %d", oracle.Exit, compiled.Exit)
	}
	return nil
}

// exitCode turns the error from a command's Run into an exit code, treating a
// non-zero program exit as a value to compare rather than a harness failure.
func exitCode(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode(), nil
	}
	return 0, err
}
