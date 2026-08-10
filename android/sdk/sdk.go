// Package sdk fetches and caches Android platform stubs.
//
// One job: given an API level, produce a path to an android.jar. The platform
// ZIP is downloaded, the stub is streamed out, and the rest — samples, sources,
// build tools, the emulator image — is discarded. Nothing is installed.
//
// # What this package does not do
//
// It does not resolve library coordinates, parse a POM, or touch Maven. The
// only remote resources are Google's repository index and the platform ZIPs it
// names. mocha does not download libraries.
//
// # Trust
//
// Google's repository index is served over HTTPS and is unsigned, and the
// checksums it carries are SHA-1. Verification here therefore detects a
// truncated or corrupted download, not a hostile one; the trust anchor is TLS
// to dl.google.com. Treating the SHA-1 as a security control would be a
// misreading of what it is.
package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrNotCached reports that a platform has not been fetched.
var ErrNotCached = errors.New("platform not cached")

// ErrNoSuchPlatform reports that the repository index names no stable platform
// at the requested API level.
var ErrNoSuchPlatform = errors.New("no such platform")

// Cache is a platform stub cache rooted at a directory.
//
// Layout:
//
//	$MOCHA_HOME/
//	├── platforms/android-24/android.jar
//	├── platforms/android-24/meta.json
//	└── index/repository.xml
type Cache struct {
	Root string

	// IndexURL overrides the repository index. Zero means DefaultIndexURL.
	IndexURL string

	// IndexMaxAge is how long a cached index stays fresh. Zero means
	// DefaultIndexMaxAge; negative disables caching.
	IndexMaxAge time.Duration

	// Progress, if set, is called during a download. total is -1 when the
	// server does not report a length.
	Progress func(done, total int64)
}

// DefaultIndexMaxAge is how long a cached repository index is reused. The index
// changes when Google ships a platform revision, which is not often; a day
// keeps `sdk list` off the network without going meaningfully stale.
const DefaultIndexMaxAge = 24 * time.Hour

// Open returns a cache rooted at root. An empty root resolves MOCHA_HOME, then
// falls back to ~/.mocha. The directory is created if it does not exist.
func Open(root string) (*Cache, error) {
	if root == "" {
		root = os.Getenv("MOCHA_HOME")
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("sdk: cannot locate a cache root: %w "+
				"(set MOCHA_HOME)", err)
		}
		root = filepath.Join(home, ".mocha")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Cache{Root: root}, nil
}

// Meta records what a cached stub is and where it came from. It exists so that
// `sdk list` can report an installed revision without reopening the jar, and so
// that a partial fetch is distinguishable from a complete one: meta.json is
// written last.
type Meta struct {
	API      int       `json:"api"`
	Revision string    `json:"revision"`
	URL      string    `json:"url"`
	SHA1     string    `json:"sha1"`
	ZipSize  int64     `json:"zip_size"`
	Entry    string    `json:"entry"`
	JarSize  int64     `json:"jar_size"`
	Fetched  time.Time `json:"fetched"`
}

func (c *Cache) platformDir(api int) string {
	return filepath.Join(c.Root, "platforms", "android-"+strconv.Itoa(api))
}

// JarPath is where a stub for api would live, cached or not.
func (c *Cache) JarPath(api int) string {
	return filepath.Join(c.platformDir(api), "android.jar")
}

// Path returns the cached android.jar for api, or ErrNotCached.
//
// This never fetches. A build that silently reaches the network because a flag
// named a level nobody had fetched would be a surprising thing for `mocha
// build` to do; the caller reports the miss and names the command that fixes
// it.
func (c *Cache) Path(api int) (string, error) {
	dir := c.platformDir(api)
	jar := filepath.Join(dir, "android.jar")

	// meta.json is written after the jar is in place, so its absence means the
	// fetch did not complete even if a jar is sitting there.
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		return "", fmt.Errorf("api %d: %w (run: mocha sdk fetch %d)", api, ErrNotCached, api)
	}
	if _, err := os.Stat(jar); err != nil {
		return "", fmt.Errorf("api %d: %w (run: mocha sdk fetch %d)", api, ErrNotCached, api)
	}
	return jar, nil
}

// Meta returns the record for a cached platform.
func (c *Cache) Meta(api int) (*Meta, error) {
	b, err := os.ReadFile(filepath.Join(c.platformDir(api), "meta.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("api %d: %w", api, ErrNotCached)
		}
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(c.platformDir(api), "meta.json"), err)
	}
	return &m, nil
}

// Installed returns the API levels present in the cache, ascending.
func (c *Cache) Installed() ([]int, error) {
	ents, err := os.ReadDir(filepath.Join(c.Root, "platforms"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []int
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		n, ok := strings.CutPrefix(e.Name(), "android-")
		if !ok {
			continue
		}
		api, err := strconv.Atoi(n)
		if err != nil {
			continue
		}
		if _, err := c.Path(api); err == nil {
			out = append(out, api)
		}
	}
	sort.Ints(out)
	return out, nil
}

// Remove deletes a cached platform. A missing platform is not an error.
func (c *Cache) Remove(api int) error {
	return os.RemoveAll(c.platformDir(api))
}

func (c *Cache) writeMeta(api int, m *Meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(c.platformDir(api), "meta.json"), append(b, '\n'), 0o644)
}