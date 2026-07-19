package conformance

import "testing"

// TestShimThreadStress is the shim's own race gate. Go's race detector
// instruments Go, not the Python runtime, so a data race in the shim's locks
// would slip past the -race corpus run, which watches the harness rather than the
// emitted program. Instead this drives a heavily contended program, a hundred
// goroutines each hammering an atomic counter and a mutex-guarded counter, and
// repeats it, requiring the exact deterministic total on every run. A race in the
// shim's locking would drop an increment on some run and a total would come up
// short, which the repetition is there to catch.
func TestShimThreadStress(t *testing.T) {
	t.Parallel()
	requireTools(t)
	const src = `package main

import (
	"fmt"
	"sync"
	"sync/atomic"
)

func main() {
	var a atomic.Int64
	var mu sync.Mutex
	guarded := 0
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			a.Add(1)
			mu.Lock()
			guarded++
			mu.Unlock()
			wg.Done()
		}()
	}
	wg.Wait()
	fmt.Println(a.Load())
	fmt.Println(guarded)
}
`
	oracle, err := RunGo(t.Context(), src)
	if err != nil {
		t.Fatalf("go oracle: %v", err)
	}
	const reps = 12
	for i := range reps {
		obs, err := RunCompiledSmoke(t.Context(), src, SmokeTimeout)
		if err != nil {
			t.Fatalf("rep %d: %v", i, err)
		}
		if obs.Stdout != oracle.Stdout {
			t.Fatalf("rep %d stdout = %q, want %q: a shim lock dropped an update under contention", i, obs.Stdout, oracle.Stdout)
		}
		if obs.Exit != oracle.Exit {
			t.Fatalf("rep %d exit = %d, want %d", i, obs.Exit, oracle.Exit)
		}
	}
}
