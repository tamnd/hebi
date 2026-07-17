package main

import (
	"context"
	"os"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"hebi": func() {
			os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
		},
	})
}

// TestScripts drives the CLI through testscript golden files. The build and run
// scripts shell out to the go tool and CPython, so they inherit the host's Go
// and cache environment and skip cleanly where those tools are absent.
func TestScripts(t *testing.T) {
	t.Parallel()
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(e *testscript.Env) error {
			for _, key := range []string{"HOME", "PATH", "GOPATH", "GOMODCACHE", "GOCACHE", "GOROOT", "GONOSUMDB", "GONOSUMCHECK"} {
				if v := os.Getenv(key); v != "" {
					e.Setenv(key, v)
				}
			}
			return nil
		},
	})
}
