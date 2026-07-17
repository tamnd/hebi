// Package shim carries the pure-Python runtime that emitted code imports: the
// integer-width helpers, the slice and map wrappers, and the other small
// pieces of Go's object model that Python does not provide directly. The
// Python sources are embedded so a build needs no separate install.
package shim
