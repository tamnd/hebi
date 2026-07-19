package conformance

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCacheOffByDefault checks the cache stays disabled when HEBI_CACHE_DIR is
// unset, so the default corpus run is uncached and every observation is fresh.
func TestCacheOffByDefault(t *testing.T) {
	t.Setenv("HEBI_CACHE_DIR", "")
	if c := newCache(); c.enabled {
		t.Fatal("cache enabled with HEBI_CACHE_DIR unset, want disabled")
	}
}

// TestCacheRoundTrip checks an enabled cache stores an observation and reads back
// exactly what was written, which is the contract the corpus relies on to skip a
// run without changing the result.
func TestCacheRoundTrip(t *testing.T) {
	t.Setenv("HEBI_CACHE_DIR", t.TempDir())
	c := newCache()
	if !c.enabled {
		t.Fatal("cache disabled with HEBI_CACHE_DIR set, want enabled")
	}
	want := Observation{Stdout: "hi\n", Exit: 3}
	k := c.goKey("package main")
	c.put(k, want)
	got, ok := c.get(k)
	if !ok {
		t.Fatal("get missed the entry just put")
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if _, ok := c.get(c.goKey("other source")); ok {
		t.Fatal("get hit on a key never stored")
	}
}

// TestCacheKeysSeparateTiersAndBuilds checks the keys keep the two tiers apart
// and fold the compiler build in, so a compiled entry can never be served for a
// go-oracle lookup and a new compiler build never reads an old compiled entry.
func TestCacheKeysSeparateTiersAndBuilds(t *testing.T) {
	t.Setenv("HEBI_CACHE_DIR", t.TempDir())
	c := newCache()
	const src = "package main"
	if c.goKey(src) == c.compiledKey(src) {
		t.Fatal("go and compiled keys collide for the same source")
	}
	other := &cache{selfHash: c.selfHash + "x"}
	if c.compiledKey(src) == other.compiledKey(src) {
		t.Fatal("compiled key did not change with the build hash")
	}
}

// TestCachePruneCapsEntries checks the prune drops the least recently used
// entries once the cache is over its cap, which is how it cleans up disk instead
// of growing without bound.
func TestCachePruneCapsEntries(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HEBI_CACHE_DIR", dir)
	c := newCache()
	for i := range 20 {
		c.put(c.goKey(string(rune('a'+i))), Observation{Exit: i})
	}
	c.prune(5)
	var count int
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, shard := range entries {
		if !shard.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, shard.Name()))
		if err != nil {
			t.Fatal(err)
		}
		count += len(files)
	}
	if count > 5 {
		t.Fatalf("prune left %d entries, want at most 5", count)
	}
}

// TestCachedDifferentialStaysCorrect checks that running a fixture through the
// cached path twice both passes and leaves a stored entry, so the cache serves a
// repeat run without changing the verdict. It runs one small program, not the
// band, so it stays a targeted check.
func TestCachedDifferentialStaysCorrect(t *testing.T) {
	requireTools(t)
	dir := t.TempDir()
	t.Setenv("HEBI_CACHE_DIR", dir)
	c := newCache()
	const src = "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(6 * 7)\n}\n"
	if err := c.differentialSmoke(t.Context(), src, SmokeTimeout); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := c.differentialSmoke(t.Context(), src, SmokeTimeout); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var stored int
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			stored++
		}
		return nil
	})
	if stored == 0 {
		t.Fatal("cached differential stored no entries")
	}
}
