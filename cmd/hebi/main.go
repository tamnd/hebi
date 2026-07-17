// Command hebi compiles Go to readable Python and, in a later milestone,
// embeds Python in Go.
//
// This is the M0 skeleton. Only `hebi version` does real work today. The
// `build` and `run` commands arrive with the build driver later in M0.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is the build version. It stays 0.0.0-dev until the first tagged
// release stamps a real value in.
const version = "0.0.0-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable entry point: it takes the arguments and streams so a
// test can drive the CLI without touching the process globals.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "build", "run":
		fmt.Fprintf(stderr, "hebi: %s is not wired up yet\n", args[0])
		return 1
	default:
		fmt.Fprintf(stderr, "hebi: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, "usage: hebi <command> [arguments]\n\n"+
		"commands:\n"+
		"  build    compile a Go package to Python\n"+
		"  run      compile and run a Go package\n"+
		"  version  print the hebi version\n")
}
