package conformance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"
)

// cache is an optional result cache for the differential corpus. The large band
// runs go run per fixture, which drives the Go compiler and dominates the wall
// clock, so a repeat run over an unchanged tree can skip that work and finish in
// a fraction of the time. It is off by default and turns on only when
// HEBI_CACHE_DIR names a directory, so the local fast checks and the CI race run
// stay uncached and fully honest, while a heavy run on the corpus box enables it
// to go fast.
//
// Correctness rides on the key. The go oracle is keyed on the source and the Go
// toolchain version, both pinned, so its observation is stable until one of them
// changes. The compiled tier is keyed on the source and the content hash of this
// test binary, which the go tool rebuilds whenever any compiler package or the
// embedded shim changes, so a compiler change invalidates every compiled entry
// at once. The cache only skips the run, never the check: the stdout and exit
// comparison is always done fresh from the observations, so a real mismatch fails
// on every run, cached or not.
type cache struct {
	dir      string
	selfHash string
	enabled  bool
}

// maxCacheEntries caps how many entries the cache keeps. Entries are a few
// hundred bytes each, so the concern is count, not size: the compiled key turns
// over every commit, so entries from old builds pile up. The cap keeps roughly a
// score of recent builds' worth and the prune drops the least recently used past
// it, which is how the cache cleans up after itself instead of growing without
// bound.
const maxCacheEntries = 50000

// newCache builds the corpus cache from the environment. It returns a disabled
// cache when HEBI_CACHE_DIR is unset, when the binary cannot be hashed, or when
// the directory cannot be made, so caching is strictly opt in and a setup problem
// degrades to the honest uncached path rather than a hard failure.
func newCache() *cache {
	dir := os.Getenv("HEBI_CACHE_DIR")
	if dir == "" {
		return &cache{}
	}
	self, err := hashSelf()
	if err != nil {
		return &cache{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &cache{}
	}
	c := &cache{dir: dir, selfHash: self, enabled: true}
	c.prune(maxCacheEntries)
	return c
}

var (
	selfHashOnce sync.Once
	selfHashVal  string
	selfHashErr  error
)

// hashSelf hashes the running test binary. The go tool rebuilds it whenever any
// package it imports changes, the compiler included, so its content hash is the
// exact signal for invalidating the compiled tier. It is computed once and
// reused, since the binary does not change under a single run.
func hashSelf() (string, error) {
	selfHashOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			selfHashErr = err
			return
		}
		f, err := os.Open(exe)
		if err != nil {
			selfHashErr = err
			return
		}
		defer func() { _ = f.Close() }()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			selfHashErr = err
			return
		}
		selfHashVal = hex.EncodeToString(h.Sum(nil))
	})
	return selfHashVal, selfHashErr
}

// key hashes a tagged list of parts into a hex digest, with a separator between
// parts so no concatenation can collide with a different split.
func key(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *cache) goKey(source string) string {
	return key("go", runtime.Version(), source)
}

func (c *cache) compiledKey(source string) string {
	return key("compiled", c.selfHash, source)
}

// path is where an entry lives, sharded by the first byte of its key so no
// single directory holds the whole corpus.
func (c *cache) path(k string) string {
	return filepath.Join(c.dir, k[:2], k)
}

// get reads an observation from the cache, reporting a miss on any error so a
// corrupt or absent entry is simply recomputed. A hit bumps the entry's
// modification time so the prune keeps what is still in use.
func (c *cache) get(k string) (Observation, bool) {
	if !c.enabled {
		return Observation{}, false
	}
	p := c.path(k)
	data, err := os.ReadFile(p)
	if err != nil {
		return Observation{}, false
	}
	var obs Observation
	if err := json.Unmarshal(data, &obs); err != nil {
		return Observation{}, false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now)
	return obs, true
}

// put writes an observation into the cache through a temp file and a rename, so
// a reader never sees a half-written entry. Any error is swallowed: a cache that
// cannot store is still correct, only slower.
func (c *cache) put(k string, obs Observation) {
	if !c.enabled {
		return
	}
	data, err := json.Marshal(obs)
	if err != nil {
		return
	}
	shard := filepath.Join(c.dir, k[:2])
	if err := os.MkdirAll(shard, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(shard, "tmp-*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, filepath.Join(shard, k)); err != nil {
		_ = os.Remove(tmpName)
	}
}

// prune drops the least recently used entries once the cache holds more than
// max, which is how it cleans up after old builds without an external sweep. It
// is best effort: a walk or remove error just leaves that entry in place.
func (c *cache) prune(max int) {
	type entry struct {
		path string
		mod  time.Time
	}
	var entries []entry
	_ = filepath.WalkDir(c.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entries = append(entries, entry{path: p, mod: info.ModTime()})
		return nil
	})
	if len(entries) <= max {
		return
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.Before(entries[j].mod) })
	for _, e := range entries[:len(entries)-max] {
		_ = os.Remove(e.path)
	}
}

// observeGo returns the go oracle's observation, from the cache on a hit and
// from a real run on a miss, storing the result so the next run hits.
func (c *cache) observeGo(ctx context.Context, source string) (Observation, error) {
	k := c.goKey(source)
	if obs, ok := c.get(k); ok {
		return obs, nil
	}
	obs, err := RunGo(ctx, source)
	if err != nil {
		return obs, err
	}
	c.put(k, obs)
	return obs, nil
}

// observeCompiled returns the compiled tier's observation, from the cache on a
// hit keyed to this exact compiler build and from a real build-and-run on a miss.
func (c *cache) observeCompiled(ctx context.Context, source string) (Observation, error) {
	k := c.compiledKey(source)
	if obs, ok := c.get(k); ok {
		return obs, nil
	}
	obs, err := RunCompiled(ctx, source)
	if err != nil {
		return obs, err
	}
	c.put(k, obs)
	return obs, nil
}

// differentialSmoke is the cached corpus check. With the cache off it is exactly
// the free DifferentialSmoke, so the default path is unchanged. With it on, each
// tier's observation comes from the cache when its key still matches, and the
// stdout and exit check runs fresh on whatever was observed, under the same
// no-deadlock bound so a stuck program on a miss still surfaces as ErrDeadlock.
func (c *cache) differentialSmoke(ctx context.Context, source string, timeout time.Duration) error {
	if !c.enabled {
		return DifferentialSmoke(ctx, source, timeout)
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	oracle, err := c.observeGo(tctx, source)
	if err != nil {
		if errors.Is(tctx.Err(), context.DeadlineExceeded) {
			return ErrDeadlock
		}
		return fmt.Errorf("go oracle: %w", err)
	}
	compiled, err := c.observeCompiled(tctx, source)
	if err != nil {
		if errors.Is(tctx.Err(), context.DeadlineExceeded) {
			return ErrDeadlock
		}
		return fmt.Errorf("compiled tier: %w", err)
	}
	return compareObservations(oracle, compiled)
}
