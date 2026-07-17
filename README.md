# hebi (蛇)

Compile Go to readable Python, call Python from Go.

hebi is a Go toolchain with two directions.
`hebi build` compiles a Go package into readable Python that runs on CPython, with a native fast path behind an identical import.
The Python-from-Go direction embeds CPython and generates typed Go bindings, so a Go program can drive a Python library without hand-written glue.

The snake moves both ways.

## Status

Pre-alpha, under heavy construction.
The current build carries the start of the M0 skeleton and cannot compile a real program yet.
Follow the milestones in `notes/Spec/2076b/milestones` for what lands next.

## How it works

hebi loads a Go package with `go/packages`, type-checks it with `go/types`, and lowers the typed program onto a Python object model that mirrors Go's semantics: fixed-width integers that wrap, value-copied structs, slices over a shared backing, and Go's panic and error surface.
The emitter leans on Python idioms wherever they are faithful and reaches for a small runtime shim only where a bare idiom would diverge, so the output reads like Python a person would write.
Go is the semantic oracle: where behavior is observable, hebi matches `go run`, and every deliberate divergence is written down.

## Directions

The compiled tier emits pure Python plus a small runtime shim and ships as a `py3-none-any` wheel that also runs on PyPy.
The native tier compiles the same source to Go behind a thin abi3 extension for the hot path, chosen automatically and invisible to the caller.
Direction B embeds CPython in a Go process and funnels every call through one serializer goroutine that owns the GIL.

## Development

Every change lands as one pull request from a feature branch.
The build is Go 1.26.5, pinned in `go.mod` and recorded in `versions.env` alongside the CPython and PyPy pins.
The three-way differential harness compares `go run`, the compiled tier, and the native tier on a growing fixture corpus, and a change only merges when they agree.
