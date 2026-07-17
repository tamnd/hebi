// Package emit turns the IR into readable Python, one module per Go package,
// with a deterministic node order and ruff-clean output. It uses Python idioms
// wherever they are faithful to Go and reaches for the runtime shim only where
// a bare idiom would diverge.
package emit
