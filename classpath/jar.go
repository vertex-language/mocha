package classpath

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	manifestEntry  = "META-INF/MANIFEST.MF"
	versionsPrefix = "META-INF/versions/"
	metaInfPrefix  = "META-INF/"

	// minVersionedRelease is the lower bound of the versioned search. The
	// specification ignores any N below 9.
	minVersionedRelease = 9

	// maxEntrySize caps a single decompressed entry. android.jar is fifty
	// megabytes whole; no class file within it is close to this.
	maxEntrySize = 512 << 20
)

type jar struct {
	container string
	nested    string // non-empty when this jar lives inside an aar
	kind      Kind

	closer io.Closer // nil for a jar read from memory
	index  map[string]jarEntry
	names  []string
	mr     bool
}

type jarEntry struct {
	file    *zip.File
	release int // versioned directory N, 0 for a base entry
}

func openJar(path string, release int) (*jar, error) {
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
	j := indexJar(path, "", KindJar, zr, release)
	j.closer = f
	return j, nil
}

// indexJar walks the central directory once and resolves multi-release
// selection as it goes.
func indexJar(container, nested string, kind Kind, zr *zip.Reader, release int) *jar {
	j := &jar{
		container: container,
		nested:    nested,
		kind:      kind,
		index:     make(map[string]jarEntry, len(zr.File)),
	}

	if f := find(zr, manifestEntry); f != nil {
		if b, err := readEntry(f); err == nil {
			j.mr = parseMainSection(b).multiRelease()
		}
	}

	for _, f := range zr.File {
		name, rel := f.Name, 0

		if strings.HasPrefix(name, versionsPrefix) {
			// A versioned entry in a jar that is not marked Multi-Release is
			// inert — it is an ordinary resource with a funny name.
			if !j.mr {
				continue
			}
			n, rest, ok := splitVersioned(name)
			if !ok || n < minVersionedRelease || n > release {
				continue
			}
			// Resources under META-INF cannot be versioned.
			if strings.HasPrefix(rest, metaInfPrefix) {
				continue
			}
			name, rel = rest, n
		}

		binary, ok := binaryName(name)
		if !ok {
			continue
		}
		// Higher N presides over lower, and over the base entry. At equal
		// release the first central directory record wins, so a duplicated
		// name cannot be used to shadow the copy a reader already saw.
		if prev, dup := j.index[binary]; dup && prev.release >= rel {
			continue
		}
		j.index[binary] = jarEntry{file: f, release: rel}
	}

	j.names = make([]string, 0, len(j.index))
	for n := range j.index {
		j.names = append(j.names, n)
	}
	sort.Strings(j.names)
	return j
}

// splitVersioned parses META-INF/versions/N/rest. N must match {1-9}{0-9}* —
// no leading zero, no sign, no padding — and anything else is ignored rather
// than reported.
func splitVersioned(name string) (n int, rest string, ok bool) {
	s := name[len(versionsPrefix):]
	i := strings.IndexByte(s, '/')
	if i <= 0 || i == len(s)-1 {
		return 0, "", false
	}
	num, rest := s[:i], s[i+1:]
	if len(num) > 9 || num[0] < '1' || num[0] > '9' {
		return 0, "", false
	}
	for k := 1; k < len(num); k++ {
		if num[k] < '0' || num[k] > '9' {
			return 0, "", false
		}
	}
	v, err := strconv.Atoi(num)
	if err != nil {
		return 0, "", false
	}
	return v, rest, true
}

func (j *jar) Kind() Kind        { return j.kind }
func (j *jar) Container() string { return j.container }

// MultiRelease reports whether the jar declared Multi-Release: true.
func (j *jar) MultiRelease() bool { return j.mr }

func (j *jar) has(binary string) bool {
	_, ok := j.index[binary]
	return ok
}

func (j *jar) Class(binary string) (*Class, error) {
	e, ok := j.index[binary]
	if !ok {
		return nil, &NotFoundError{Binary: binary, Container: j.container}
	}
	origin := Origin{
		Kind:      j.kind,
		Container: j.container,
		Nested:    j.nested,
		Entry:     e.file.Name,
		Release:   e.release,
	}
	data, err := readEntry(e.file)
	if err != nil {
		return nil, &Error{Origin: origin, Err: err}
	}
	return &Class{Binary: binary, Data: data, Origin: origin}, nil
}

func (j *jar) Names() ([]string, error) { return j.names, nil }

func (j *jar) Close() error {
	if j.closer == nil {
		return nil
	}
	return j.closer.Close()
}

func find(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// readEntry decompresses one entry into a fresh buffer.
//
// The trailing one-byte probe is not paranoia about size alone: archive/zip
// validates the CRC when the decompressor reaches EOF, so the probe is what
// turns a corrupt entry into an error here rather than a confusing decode
// failure two packages up.
func readEntry(f *zip.File) ([]byte, error) {
	if f.UncompressedSize64 > maxEntrySize {
		return nil, fmt.Errorf("entry declares %d bytes, over the %d byte limit",
			f.UncompressedSize64, maxEntrySize)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	buf := make([]byte, f.UncompressedSize64)
	if _, err := io.ReadFull(rc, buf); err != nil {
		return nil, err
	}
	var probe [1]byte
	switch _, err := rc.Read(probe[:]); {
	case err == io.EOF:
		return buf, nil
	case err != nil:
		return nil, err
	default:
		return nil, fmt.Errorf("entry is longer than its declared %d bytes", len(buf))
	}
}