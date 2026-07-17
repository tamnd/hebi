// Command hebi compiles Go to readable Python and, in a later milestone,
// embeds Python in Go.
//
// At M0 it stands the pipeline up end to end: `hebi build` writes the emitted
// Python beside the runtime shim, `hebi run` builds and runs it under CPython,
// and `hebi version` prints the version.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tamnd/hebi/pkg/build"
)

// version is the build version. It stays 0.0.0-dev until the first tagged
// release stamps a real value in.
const version = "0.0.0-dev"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point: it takes the arguments and streams so a
// test can drive the CLI without touching the process globals.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "build":
		return runBuild(args[1:], stdout, stderr)
	case "run":
		return runRun(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "hebi: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func runBuild(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "out", "directory to write the emitted Python into")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: hebi build [-o dir] <input>")
		return 2
	}
	res, err := build.Build(fs.Arg(0), *out)
	if err != nil {
		fmt.Fprintf(stderr, "hebi: %v\n", err)
		return 1
	}
	for _, name := range res.Files {
		fmt.Fprintln(stdout, name)
	}
	return 0
}

func runRun(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: hebi run <input>")
		return 2
	}
	code, err := build.Run(ctx, fs.Arg(0), stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "hebi: %v\n", err)
		return 1
	}
	return code
}

func usage(w io.Writer) {
	fmt.Fprint(w, "usage: hebi <command> [arguments]\n\n"+
		"commands:\n"+
		"  build    compile a Go package to Python\n"+
		"  run      compile and run a Go package\n"+
		"  version  print the hebi version\n")
}
