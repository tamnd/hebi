package conformance

import (
	"os"
	"testing"
)

// corpusCache is the result cache the fixture band runs through. It is built once
// in TestMain so the binary is hashed and the prune runs a single time rather
// than per fixture. With HEBI_CACHE_DIR unset it is a disabled cache, so the
// default run is uncached and unchanged.
var corpusCache *cache

func TestMain(m *testing.M) {
	corpusCache = newCache()
	os.Exit(m.Run())
}
