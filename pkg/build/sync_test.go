package build

import (
	"testing"
)

// TestSyncPrimitives checks the sync package surface against go run: a Mutex and a
// WaitGroup coordinate a batch of goroutines to a deterministic total, a Once runs
// its function exactly once across several calls, a TryLock reports whether it took
// a free or a held lock, and an RWMutex serves its read and write lock operations.
func TestSyncPrimitives(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"a mutex and a waitgroup coordinate goroutines to a total",
			"package main\n\nimport (\n\t\"fmt\"\n\t\"sync\"\n)\n\nfunc main() {\n\tvar mu sync.Mutex\n\tvar wg sync.WaitGroup\n\ttotal := 0\n\tfor i := 0; i < 100; i++ {\n\t\twg.Add(1)\n\t\tgo func() {\n\t\t\tmu.Lock()\n\t\t\ttotal++\n\t\t\tmu.Unlock()\n\t\t\twg.Done()\n\t\t}()\n\t}\n\twg.Wait()\n\tfmt.Println(total)\n}\n",
		},
		{
			"a once runs its function exactly once",
			"package main\n\nimport (\n\t\"fmt\"\n\t\"sync\"\n)\n\nfunc main() {\n\tvar once sync.Once\n\tn := 0\n\tfor i := 0; i < 5; i++ {\n\t\tonce.Do(func() { n++ })\n\t}\n\tfmt.Println(n)\n}\n",
		},
		{
			"a trylock reports a free then a held mutex",
			"package main\n\nimport (\n\t\"fmt\"\n\t\"sync\"\n)\n\nfunc main() {\n\tvar mu sync.Mutex\n\tfmt.Println(mu.TryLock())\n\tfmt.Println(mu.TryLock())\n\tmu.Unlock()\n\tfmt.Println(mu.TryLock())\n}\n",
		},
		{
			"an rwmutex serves read and write locks",
			"package main\n\nimport (\n\t\"fmt\"\n\t\"sync\"\n)\n\nfunc main() {\n\tvar rw sync.RWMutex\n\trw.RLock()\n\trw.RLock()\n\trw.RUnlock()\n\trw.RUnlock()\n\trw.Lock()\n\trw.Unlock()\n\tfmt.Println(rw.TryLock())\n\tfmt.Println(rw.TryRLock())\n\trw.Unlock()\n\tfmt.Println(rw.TryRLock())\n\trw.RUnlock()\n\tfmt.Println(\"ok\")\n}\n",
		},
		{
			"an rwmutex serializes writers to a total",
			"package main\n\nimport (\n\t\"fmt\"\n\t\"sync\"\n)\n\nfunc main() {\n\tvar rw sync.RWMutex\n\tvar wg sync.WaitGroup\n\tsum := 0\n\tfor i := 0; i < 40; i++ {\n\t\twg.Add(1)\n\t\tgo func() {\n\t\t\trw.Lock()\n\t\t\tsum += 3\n\t\t\trw.Unlock()\n\t\t\twg.Done()\n\t\t}()\n\t}\n\twg.Wait()\n\tfmt.Println(sum)\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestSyncFieldZeroValue checks that a sync value declared as a struct field takes a
// fresh runtime object per instance, so two counters lock independently and each
// keeps its own tally.
func TestSyncFieldZeroValue(t *testing.T) {
	t.Parallel()
	src := "package main\n\nimport (\n\t\"fmt\"\n\t\"sync\"\n)\n\ntype counter struct {\n\tmu sync.Mutex\n\tn  int\n}\n\nfunc main() {\n\tvar a counter\n\tvar b counter\n\ta.mu.Lock()\n\ta.n = 3\n\ta.mu.Unlock()\n\tb.mu.Lock()\n\tb.n = 7\n\tb.mu.Unlock()\n\tfmt.Println(a.n, b.n)\n}\n"
	assertProgramMatchesGo(t, src)
}

// TestSyncEmit pins the emitted shape: a sync value constructs through its runtime
// class and each operation lowers to the runtime free function that carries it,
// passing the value as the first argument.
func TestSyncEmit(t *testing.T) {
	t.Parallel()
	src := "package main\n\nimport \"sync\"\n\nfunc main() {\n\tvar mu sync.Mutex\n\tvar wg sync.WaitGroup\n\tvar once sync.Once\n\tmu.Lock()\n\tmu.Unlock()\n\twg.Add(1)\n\twg.Done()\n\twg.Wait()\n\tonce.Do(func() {})\n}\n"
	got := emitOf(t, src)
	for _, want := range []string{
		"_hebirt.Mutex()",
		"_hebirt.WaitGroup()",
		"_hebirt.Once()",
		"_hebirt.mutex_lock(mu)",
		"_hebirt.mutex_unlock(mu)",
		"_hebirt.waitgroup_add(wg, 1)",
		"_hebirt.waitgroup_done(wg)",
		"_hebirt.waitgroup_wait(wg)",
		"_hebirt.once_do(once,",
	} {
		if !bytesContains(got, want) {
			t.Errorf("sync emit missing %q:\n%s", want, got)
		}
	}
}

// TestWaitGroupNegativeCrashes pins that driving a WaitGroup counter below zero
// crashes the Go way, the panic escaping main and exiting two.
func TestWaitGroupNegativeCrashes(t *testing.T) {
	t.Parallel()
	assertProgramCrashesLikeGo(t, "package main\n\nimport \"sync\"\n\nfunc main() {\n\tvar wg sync.WaitGroup\n\twg.Add(-1)\n}\n")
}

// TestUnlockUnlockedMutexCrashes pins that unlocking a mutex that is not locked
// crashes the Go way with Go's message.
func TestUnlockUnlockedMutexCrashes(t *testing.T) {
	t.Parallel()
	assertProgramCrashesLikeGo(t, "package main\n\nimport \"sync\"\n\nfunc main() {\n\tvar mu sync.Mutex\n\tmu.Unlock()\n}\n")
}

// TestRUnlockUnheldRWMutexCrashes pins that read-unlocking an RWMutex that holds no
// read lock crashes the Go way.
func TestRUnlockUnheldRWMutexCrashes(t *testing.T) {
	t.Parallel()
	assertProgramCrashesLikeGo(t, "package main\n\nimport \"sync\"\n\nfunc main() {\n\tvar rw sync.RWMutex\n\trw.RUnlock()\n}\n")
}
