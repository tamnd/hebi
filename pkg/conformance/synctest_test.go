//go:build go1.25

package conformance

import (
	"sync"
	"testing"
	"testing/synctest"
)

// TestGoRunSemaphoreBounds locks down the compile-throttling contract with
// testing/synctest. The oracle throttle is a buffered channel used as a counting
// semaphore, real Go goroutines contending on a shared channel, which is exactly
// the concurrency synctest was built to test: the bubble runs the goroutines
// under virtual time and synctest.Wait blocks until every one is durably parked,
// so the count that got past the token is an exact fact rather than a timing
// race. It mirrors goRunTokens through the real maxGoRun ceiling without touching
// the shared pool the live tests draw from, so the two cannot interfere.
func TestGoRunSemaphoreBounds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ceiling := maxGoRun()
		sem := make(chan struct{}, ceiling)
		release := make(chan struct{})
		var mu sync.Mutex
		inside := 0
		for range ceiling + 4 {
			go func() {
				sem <- struct{}{}
				mu.Lock()
				inside++
				mu.Unlock()
				<-release
				<-sem
			}()
		}
		synctest.Wait()
		mu.Lock()
		got := inside
		mu.Unlock()
		if got != ceiling {
			t.Fatalf("semaphore let %d goroutines in, want the ceiling %d", got, ceiling)
		}
		close(release)
		synctest.Wait()
	})
}
