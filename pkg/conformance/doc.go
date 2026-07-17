// Package conformance is the three-way differential harness. It compiles a
// fixture, runs the emitted Python, runs the same source under go run, and
// requires that they agree on stdout, exit status, and the surfaced error.
// From M7 it grows a third runner for the native tier.
package conformance
