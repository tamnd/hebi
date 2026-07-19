package build

import "testing"

// TestGoroutine checks the go statement against go run. A go statement fixes the
// function value and its arguments where it is written and runs the call on a new
// daemon thread, so a goroutine that prints during a sleep prints before the line
// that follows the sleep, and a closure sees the variable it captured. Each case
// keeps one goroutine printing at a time so the standard output is ordered rather
// than a race between concurrent writers.
func TestGoroutine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
	}{
		{
			"a goroutine on a named function runs during the sleep",
			"package main\n\nimport (\n\t\"fmt\"\n\t\"time\"\n)\n\nfunc worker(n int) {\n\tfmt.Println(\"worker\", n)\n}\n\nfunc main() {\n\tgo worker(3)\n\ttime.Sleep(50 * time.Millisecond)\n\tfmt.Println(\"done\")\n}\n",
		},
		{
			"a goroutine closure sees the variable it captured",
			"package main\n\nimport (\n\t\"fmt\"\n\t\"time\"\n)\n\nfunc main() {\n\tmsg := \"hello\"\n\tgo func() {\n\t\tfmt.Println(msg)\n\t}()\n\ttime.Sleep(50 * time.Millisecond)\n\tfmt.Println(\"after\")\n}\n",
		},
		{
			"a goroutine on fmt.Println prints its snapshotted argument",
			"package main\n\nimport (\n\t\"fmt\"\n\t\"time\"\n)\n\nfunc main() {\n\tgo fmt.Println(\"direct\")\n\ttime.Sleep(50 * time.Millisecond)\n\tfmt.Println(\"main\")\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertProgramMatchesGo(t, tt.source)
		})
	}
}

// TestGoroutinePanicCrashes checks the fidelity guarantee that a panic which runs
// off the top of a goroutine crashes the whole program, it does not just end the
// one goroutine. The goroutine panics during the main goroutine's sleep, so the
// process exits with status 2 and the panic banner before main reaches the line
// after the sleep, which never prints.
func TestGoroutinePanicCrashes(t *testing.T) {
	t.Parallel()
	assertProgramCrashesLikeGo(t, "package main\n\nimport (\n\t\"fmt\"\n\t\"time\"\n)\n\nfunc boom() {\n\tpanic(\"goroutine down\")\n}\n\nfunc main() {\n\tgo boom()\n\ttime.Sleep(50 * time.Millisecond)\n\tfmt.Println(\"unreachable\")\n}\n")
}

// TestGoroutineEmit pins the emitted forms. A go statement lowers to a call into
// the runtime's go helper with the callable first and its snapshotted arguments
// after, a go on fmt.Println passes the shim's println reference, an immediately
// spawned function literal hoists to a def the helper then names, time.Sleep
// lowers to the sleep intrinsic, and a Duration constant folds to its exact
// nanosecond value with no package lookup.
func TestGoroutineEmit(t *testing.T) {
	t.Parallel()
	named := emitOf(t, "package main\n\nfunc worker(n int) {}\n\nfunc main() {\n\tgo worker(3)\n}\n")
	if !bytesContains(named, "_hebirt.go(worker, 3)") {
		t.Errorf("named goroutine emit missing helper call:\n%s", named)
	}
	println := emitOf(t, "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tgo fmt.Println(\"hi\")\n}\n")
	if !bytesContains(println, "_hebirt.go(_hebirt.println,") {
		t.Errorf("goroutine on fmt.Println emit missing println reference:\n%s", println)
	}
	closure := emitOf(t, "package main\n\nfunc main() {\n\tgo func() {}()\n}\n")
	if !bytesContains(closure, "_hebirt.go(_func)") {
		t.Errorf("goroutine closure emit missing hoisted def call:\n%s", closure)
	}
	sleep := emitOf(t, "package main\n\nimport \"time\"\n\nfunc main() {\n\ttime.Sleep(50 * time.Millisecond)\n}\n")
	if !bytesContains(sleep, "_hebirt._sleep((50 * 1000000))") {
		t.Errorf("time.Sleep emit missing folded Duration:\n%s", sleep)
	}
}
