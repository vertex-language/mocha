package sdk

import (
	"archive/zip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const userAgent = "mocha-sdk/1"

func httpClient() *http.Client {
	return &http.Client{Timeout: 0} // bounded by the caller's context
}

// Fetch downloads the platform for api and caches its android.jar, returning
// the path. A platform already cached is returned untouched unless force.
func (c *Cache) Fetch(ctx context.Context, api int, force bool) (string, error) {
	if !force {
		if p, err := c.Path(api); err == nil {
			return p, nil
		}
	}

	plat, err := c.Lookup(ctx, api)
	if err != nil {
		return "", err
	}

	tmpDir := filepath.Join(c.Root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", err
	}
	zipPath, sum, err := c.download(ctx, plat, tmpDir)
	if err != nil {
		return "", err
	}
	// The ZIP is scratch. It is removed whether extraction succeeds or not.
	defer os.Remove(zipPath)

	if plat.SHA1 != "" && !strings.EqualFold(sum, plat.SHA1) {
		return "", fmt.Errorf("sdk: %s: checksum mismatch (index says %s, got %s)",
			plat.URL, plat.SHA1, sum)
	}

	dir := c.platformDir(api)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	jar := filepath.Join(dir, "android.jar")

	entry, n, err := extractStub(zipPath, jar)
	if err != nil {
		return "", err
	}

	// meta.json last: its presence is what marks the platform complete.
	if err := c.writeMeta(api, &Meta{
		API:      api,
		Revision: plat.Revision.String(),
		URL:      plat.URL,
		SHA1:     plat.SHA1,
		ZipSize:  plat.Size,
		Entry:    entry,
		JarSize:  n,
		Fetched:  time.Now().UTC(),
	}); err != nil {
		return "", err
	}
	return jar, nil
}

// download streams the archive to a temp file, hashing as it goes, and returns
// the path and the hex SHA-1.
func (c *Cache) download(ctx context.Context, p Platform, tmpDir string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient().Do(req)
	if err != nil {
		return "", "", fmt.Errorf("sdk: fetching %s: %w", p.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("sdk: fetching %s: %s", p.URL, resp.Status)
	}

	f, err := os.CreateTemp(tmpDir, "platform-*.zip")
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	total := resp.ContentLength
	if total < 0 && p.Size > 0 {
		total = p.Size
	}

	h := sha1.New()
	var w io.Writer = io.MultiWriter(f, h)
	if c.Progress != nil {
		w = io.MultiWriter(f, h, &progressWriter{fn: c.Progress, total: total})
	}

	written, err := io.Copy(w, resp.Body)
	if err != nil {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("sdk: fetching %s: %w", p.URL, err)
	}
	if p.Size > 0 && written != p.Size {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("sdk: %s: got %d bytes, index says %d", p.URL, written, p.Size)
	}
	return f.Name(), hex.EncodeToString(h.Sum(nil)), nil
}

// extractStub copies the single android.jar out of a platform ZIP.
//
// The archive's top-level directory is named for the platform *version*, not
// its API level — android-6.0 for API 23, android-10 for API 29 — and the
// naming has changed more than once. Matching on the base name at depth two
// avoids depending on it, and refusing an ambiguous match avoids silently
// picking the wrong jar if the layout changes again.
func extractStub(zipPath, dest string) (entry string, n int64, err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", 0, fmt.Errorf("sdk: %s: %w", zipPath, err)
	}
	defer zr.Close()

	var found *zip.File
	for _, f := range zr.File {
		if path.Base(f.Name) != "android.jar" {
			continue
		}
		if strings.Count(strings.Trim(f.Name, "/"), "/") != 1 {
			continue // not <platform>/android.jar
		}
		if found != nil {
			return "", 0, fmt.Errorf("sdk: %s: more than one android.jar (%s and %s)",
				zipPath, found.Name, f.Name)
		}
		found = f
	}
	if found == nil {
		return "", 0, fmt.Errorf("sdk: %s: no android.jar", zipPath)
	}

	rc, err := found.Open()
	if err != nil {
		return "", 0, fmt.Errorf("sdk: %s!/%s: %w", zipPath, found.Name, err)
	}
	defer rc.Close()

	tmp := dest + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return "", 0, err
	}
	n, err = io.Copy(out, rc)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", 0, fmt.Errorf("sdk: %s!/%s: %w", zipPath, found.Name, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return "", 0, err
	}
	return found.Name, n, nil
}

type progressWriter struct {
	fn    func(done, total int64)
	done  int64
	total int64
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.done += int64(len(p))
	w.fn(w.done, w.total)
	return len(p), nil
}

// writeFileAtomic writes via a temp file and a rename, so an interrupted write
// cannot leave a truncated file that a later run treats as valid.
func writeFileAtomic(dest string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dest), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, perm)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}