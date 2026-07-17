// Package frontend loads a Go package with go/packages, type-checks it with
// go/types, and hands the typed AST and its type information to the IR
// builder. hebi is type-directed from the first line, so the type facts
// gathered here drive every later lowering decision.
package frontend
