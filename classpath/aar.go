package classpath

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const (
	aarManifest = "AndroidManifest.xml"
	aarClasses  = "classes.jar"
	aarLibs     = "libs/"
	aarRes      = "res/"
)

// aar is an Android archive: a zip carrying classes.jar, a manifest fragment,
// and optionally resources.
//
// Resource compilation is out of scope, so this entry serves class bytes,
// hands the manifest fragment to the manifest package, and records whether
// res/ was populated — which is the fact a Tier 3 diagnostic needs in order to
// name the library it choked on instead of silently producing a broken APK.
type aar struct {
	container string
	closer    io.Closer

	jars     []*jar // classes.jar first, then libs/*.jar sorted
	manifest []byte // AndroidManifest.xml, plain-text XML, not binary AXML
	hasRes   bool
	names    []string
}

func openAar(path string, release int) (*aar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	zr, err := zip.NewReader(f, fi.Size())
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	a := &aar{container: path, closer: f}

	if mf := find(zr, aarManifest); mf != nil {
		if a.manifest, err = readEntry(mf); err != nil {
			f.Close()
			return nil, &Error{
				Origin: Origin{Kind: KindAar, Container: path, Entry: aarManifest},
				Err:    err,
			}
		}
	}

	// A bare res/ directory record is not a resource. Only a file under it is.
	var libs []*zip.File
	for _, e := range zr.File {
		switch {
		case strings.HasPrefix(e.Name, aarRes) && len(e.Name) > len(aarRes) &&
			!strings.HasSuffix(e.Name, "/"):
			a.hasRes = true
		case strings.HasPrefix(e.Name, aarLibs) && strings.HasSuffix(e.Name, ".jar"):
			libs = append(libs, e)
		}
	}
	sort.Slice(libs, func(i, j int) bool { return libs[i].Name < libs[j].Name })

	// classes.jar is buffered whole. A nested archive cannot be read through
	// the outer one lazily — deflate has no random access — so this is the one
	// place the package holds a container in memory.
	nested := append([]*zip.File{}, libs...)
	if cj := find(zr, aarClasses); cj != nil {
		nested = append([]*zip.File{cj}, nested...)
	}
	for _, e := range nested {
		buf, err := readEntry(e)
		if err != nil {
			f.Close()
			return nil, &Error{
				Origin: Origin{Kind: KindAar, Container: path, Entry: e.Name},
				Err:    err,
			}
		}
		inner, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
		if err != nil {
			f.Close()
			return nil, &Error{
				Origin: Origin{Kind: KindAar, Container: path, Entry: e.Name},
				Err:    err,
			}
		}
		a.jars = append(a.jars, indexJar(path, e.Name, KindAar, inner, release))
	}

	seen := map[string]bool{}
	for _, j := range a.jars {
		for _, n := range j.names {
			if !seen[n] {
				seen[n] = true
				a.names = append(a.names, n)
			}
		}
	}
	sort.Strings(a.names)
	return a, nil
}

func (a *aar) Kind() Kind        { return KindAar }
func (a *aar) Container() string { return a.container }

// Manifest returns the aar's AndroidManifest.xml fragment, or nil.
func (a *aar) Manifest() []byte { return a.manifest }

// HasResources reports whether the aar carries compiled-resource input. An aar
// for which this is true needs resource compilation, ID allocation and an R
// class — none of which mocha does — and is the Tier 3 dependency a build must
// refuse by name.
func (a *aar) HasResources() bool { return a.hasRes }

func (a *aar) has(binary string) bool {
	for _, j := range a.jars {
		if j.has(binary) {
			return true
		}
	}
	return false
}

func (a *aar) Class(binary string) (*Class, error) {
	for _, j := range a.jars {
		c, err := j.Class(binary)
		if err == nil {
			return c, nil
		}
		if !IsNotFound(err) {
			return nil, err
		}
	}
	return nil, &NotFoundError{Binary: binary, Container: a.container}
}

func (a *aar) Names() ([]string, error) { return a.names, nil }

func (a *aar) Close() error { return a.closer.Close() }