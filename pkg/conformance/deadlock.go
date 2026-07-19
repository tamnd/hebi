package conformance

import (
	"context"
	"errors"
	"time"
)

// ErrDeadlock marks a fixture whose compiled program did not finish inside the
// smoke gate's wall-clock bound. Go's runtime exits a fully blocked program with
// its deadlock detector, but the compiled tier is Python threads, which have no
// such detector, so a stuck channel or an unbalanced WaitGroup would hang the
// whole suite until the outer test timeout. The bound turns that hang into this
// error instead, so a deadlock is a labeled failure rather than a lost half hour.
var ErrDeadlock = errors.New("compiled program did not finish: possible deadlock")

// SmokeTimeout is the wall-clock bound the no-deadlock gate gives a single
// fixture. A correct concurrency fixture finishes in well under a second, so the
// bound sits far above that to leave room for a cold go run compile on a loaded
// runner, while still failing a real stall in a minute rather than the suite's
// outer timeout.
const SmokeTimeout = 60 * time.Second

// RunCompiledSmoke runs a fixture through the compiled tier under a wall-clock
// bound and reports ErrDeadlock when it does not finish in time, so a stuck
// goroutine is a labeled failure rather than a hung test. It checks the context
// rather than the run error because a killed subprocess surfaces as an ordinary
// non-zero exit, not an error, so the deadline is the only reliable signal.
func RunCompiledSmoke(ctx context.Context, source string, timeout time.Duration) (Observation, error) {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	obs, err := RunCompiled(tctx, source)
	if errors.Is(tctx.Err(), context.DeadlineExceeded) {
		return Observation{}, ErrDeadlock
	}
	return obs, err
}

// DifferentialSmoke runs Differential under a wall-clock bound so a compiled
// program that never finishes surfaces as ErrDeadlock instead of hanging the
// suite. The go oracle exits a blocked program on its own, so the bound only ever
// trips on the compiled tier, which is the tier without a deadlock detector.
func DifferentialSmoke(ctx context.Context, source string, timeout time.Duration) error {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := Differential(tctx, source)
	if errors.Is(tctx.Err(), context.DeadlineExceeded) {
		return ErrDeadlock
	}
	return err
}
