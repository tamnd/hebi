package build

import (
	"testing"
)

// TestAtomic checks the sync/atomic value types against go run: an Int64 counter
// adds across a batch of goroutines to a deterministic total, a Bool swaps and
// compare-and-swaps, and a Uint32 wraps at its width the way Go's does.
func TestAtomic(t *testing.T) {
	t.Parallel()
	src := `package main

import (
	"fmt"
	"sync"
	"sync/atomic"
)

func main() {
	var n atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			n.Add(1)
			wg.Done()
		}()
	}
	wg.Wait()
	fmt.Println(n.Load())

	var b atomic.Bool
	fmt.Println(b.CompareAndSwap(false, true))
	fmt.Println(b.CompareAndSwap(false, true))
	fmt.Println(b.Load())
	fmt.Println(b.Swap(false))

	var u atomic.Uint32
	u.Store(4294967295)
	fmt.Println(u.Add(1))

	var s atomic.Int32
	fmt.Println(s.Add(-5))
}
`
	assertProgramMatchesGo(t, src)
}

// TestSyncMap checks the sync.Map surface against go run: a store then a comma-ok
// load, a miss, LoadOrStore on a present and an absent key, LoadAndDelete, Delete,
// and a Range that sums the remaining values.
func TestSyncMap(t *testing.T) {
	t.Parallel()
	src := `package main

import (
	"fmt"
	"sync"
)

func main() {
	var m sync.Map
	m.Store("a", 1)
	m.Store("b", 2)
	v, ok := m.Load("a")
	fmt.Println("a", v, ok)
	_, ok = m.Load("z")
	fmt.Println("z", ok)
	actual, loaded := m.LoadOrStore("a", 99)
	fmt.Println("or-a", actual, loaded)
	actual, loaded = m.LoadOrStore("c", 3)
	fmt.Println("or-c", actual, loaded)
	dv, dloaded := m.LoadAndDelete("b")
	fmt.Println("del-b", dv, dloaded)
	m.Delete("a")
	total := 0
	m.Range(func(k, val any) bool {
		total += val.(int)
		return true
	})
	fmt.Println("total", total)
}
`
	assertProgramMatchesGo(t, src)
}

// TestSyncPool checks the sync.Pool surface against go run: Get on an empty pool
// runs New, a Put then a Get hands the value back, and a further Get on the empty
// pool runs New again, so the New-call count is deterministic in this serial use.
func TestSyncPool(t *testing.T) {
	t.Parallel()
	src := `package main

import (
	"fmt"
	"sync"
)

func main() {
	calls := 0
	p := sync.Pool{New: func() any {
		calls++
		return 42
	}}
	fmt.Println(p.Get())
	p.Put(7)
	fmt.Println(p.Get())
	fmt.Println(p.Get())
	fmt.Println("calls", calls)
}
`
	assertProgramMatchesGo(t, src)
}

// TestSyncCondSignal checks sync.Cond against go run: a waiter parks on the
// condition with the mutex held, the main goroutine sets the state and signals, and
// the waiter wakes to observe it, the classic wait-in-a-loop pattern.
func TestSyncCondSignal(t *testing.T) {
	t.Parallel()
	src := `package main

import (
	"fmt"
	"sync"
)

func main() {
	var mu sync.Mutex
	c := sync.NewCond(&mu)
	ready := false
	done := make(chan bool)
	go func() {
		mu.Lock()
		for !ready {
			c.Wait()
		}
		mu.Unlock()
		done <- true
	}()
	mu.Lock()
	ready = true
	mu.Unlock()
	c.Signal()
	<-done
	fmt.Println("woke", ready)
}
`
	assertProgramMatchesGo(t, src)
}

// TestSyncCondBroadcast checks Cond.Broadcast and the c.L.Lock idiom against go
// run: several waiters park through the Locker reached as c.L, one Broadcast wakes
// them all, and each records its wake, so the count is exact.
func TestSyncCondBroadcast(t *testing.T) {
	t.Parallel()
	src := `package main

import (
	"fmt"
	"sync"
)

func main() {
	var mu sync.Mutex
	c := sync.NewCond(&mu)
	ready := false
	var wg sync.WaitGroup
	var wmu sync.Mutex
	woke := 0
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			c.L.Lock()
			for !ready {
				c.Wait()
			}
			c.L.Unlock()
			wmu.Lock()
			woke++
			wmu.Unlock()
			wg.Done()
		}()
	}
	mu.Lock()
	ready = true
	mu.Unlock()
	c.Broadcast()
	wg.Wait()
	fmt.Println("woke", woke)
}
`
	assertProgramMatchesGo(t, src)
}

// TestSyncFieldAtomic checks that an atomic value declared as a struct field takes
// a fresh runtime cell per instance, so two counters add independently.
func TestSyncFieldAtomic(t *testing.T) {
	t.Parallel()
	src := `package main

import (
	"fmt"
	"sync"
	"sync/atomic"
)

type counter struct {
	n  atomic.Int64
	mu sync.Mutex
}

func main() {
	var a counter
	var b counter
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			a.n.Add(2)
			wg.Done()
		}()
	}
	wg.Wait()
	b.n.Add(7)
	fmt.Println(a.n.Load(), b.n.Load())
}
`
	assertProgramMatchesGo(t, src)
}

// TestConcurrencyExtEmit pins the emitted shape of the slice-six primitives: each
// constructs through its runtime class and each operation lowers to the runtime
// free function that carries it, passing the value as the first argument.
func TestConcurrencyExtEmit(t *testing.T) {
	t.Parallel()
	src := `package main

import (
	"sync"
	"sync/atomic"
)

func main() {
	var n atomic.Int64
	n.Add(1)
	n.Load()

	var m sync.Map
	m.Store("k", 1)

	p := sync.Pool{New: func() any { return 0 }}
	p.Get()
	p.Put(1)

	var mu sync.Mutex
	c := sync.NewCond(&mu)
	c.Signal()
}
`
	got := emitOf(t, src)
	for _, want := range []string{
		"_hebirt.AtomicInt64()",
		"_hebirt.atomic_add(n, 1)",
		"_hebirt.atomic_load(n)",
		"_hebirt.SyncMap()",
		"_hebirt.syncmap_store(m,",
		"_hebirt.Pool(",
		"_hebirt.pool_get(p)",
		"_hebirt.pool_put(p, 1)",
		"_hebirt.NewCond(mu)",
		"_hebirt.cond_signal(c)",
	} {
		if !bytesContains(got, want) {
			t.Errorf("concurrency emit missing %q:\n%s", want, got)
		}
	}
}

// TestDeferInGoroutine checks that a defer inside a goroutine closure works, the
// case a plain function's defer already covers but a goroutine's did not: the
// closure body must open its own defer frame so the deferred call has a stack to
// push onto. The canonical shutdown idiom, defer wg.Done, joins a batch of
// goroutines to a deterministic total, and a deferred Unlock releases the lock the
// goroutine took, so a missing frame would crash the goroutine with an undefined
// defer stack rather than reach the count.
func TestDeferInGoroutine(t *testing.T) {
	t.Parallel()
	src := `package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 1; i <= 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			mu.Lock()
			defer mu.Unlock()
			total += n
		}(i)
	}
	wg.Wait()
	fmt.Println(total)
}
`
	assertProgramMatchesGo(t, src)
}

// TestDeferredRecoverInGoroutine checks that a deferred recover inside a goroutine
// reshapes the closure's body the same way it reshapes a named function's, so a
// panic raised in the goroutine is caught by its own deferred recover and the
// recovered value is handed back out a channel rather than crashing the process.
func TestDeferredRecoverInGoroutine(t *testing.T) {
	t.Parallel()
	src := `package main

import "fmt"

func main() {
	done := make(chan string)
	go func() {
		defer func() {
			r := recover()
			if r != nil {
				done <- "recovered " + r.(string)
			}
		}()
		panic("boom")
	}()
	fmt.Println(<-done)
}
`
	assertProgramMatchesGo(t, src)
}
