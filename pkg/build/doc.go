// Package build is the driver that wires the frontend, IR, and emitter
// together and runs the result. It backs hebi build and hebi run, owns the
// content-addressed build cache, and shells out to the pinned interpreter.
package build
