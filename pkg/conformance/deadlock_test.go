package conformance

import (
	"errors"
	"testing"
	"time"
)

// TestDeadlockGateCatchesStuckProgram proves the gate turns a hang into a
// labeled failure. A receive on a channel that never gets a send blocks the only
// goroutine forever. Go's runtime would spot the all-blocked state and exit, but
// the compiled tier is a Python thread with no such detector, so it hangs, and
// the gate must report ErrDeadlock rather than wait out the clock.
func TestDeadlockGateCatchesStuckProgram(t *testing.T) {
	t.Parallel()
	requireTools(t)
	const src = `package main

func main() {
	ch := make(chan int)
	<-ch
}
`
	_, err := RunCompiledSmoke(t.Context(), src, 3*time.Second)
	if !errors.Is(err, ErrDeadlock) {
		t.Fatalf("gate returned %v, want ErrDeadlock for a stuck program", err)
	}
}

// TestDeadlockGatePassesLiveProgram checks the gate does not fire on a program
// that finishes, so the bound catches stalls without flagging honest work: a
// batch of goroutines increment a guarded counter, join, and print the total,
// which must come back under the bound with no error.
func TestDeadlockGatePassesLiveProgram(t *testing.T) {
	t.Parallel()
	requireTools(t)
	const src = `package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			mu.Lock()
			total++
			mu.Unlock()
			wg.Done()
		}()
	}
	wg.Wait()
	fmt.Println(total)
}
`
	obs, err := RunCompiledSmoke(t.Context(), src, SmokeTimeout)
	if err != nil {
		t.Fatalf("live program: %v", err)
	}
	if obs.Stdout != "8\n" {
		t.Fatalf("stdout = %q, want %q", obs.Stdout, "8\n")
	}
}
