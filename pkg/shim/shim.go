package shim

import _ "embed"

// Name is the module name that emitted code imports the runtime under. A build
// writes the source beside the emitted modules as Name + ".py".
const Name = "_hebirt"

//go:embed runtime/_hebirt.py
var source string

// Source returns the pure-Python runtime shim as it is embedded in the binary,
// so a build needs no separate install to produce a runnable artifact.
func Source() string { return source }
