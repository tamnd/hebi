// Package shim carries the pure-Python runtime that emitted code imports: the
// small pieces of Go's object model that Python does not provide directly. At
// M0 that is Go-style value formatting and the println path fmt.Println lowers
// to; the integer-width helpers, slice and map wrappers, and the rest arrive as
// later milestones add surface. The Python sources are embedded so a build
// needs no separate install.
package shim
