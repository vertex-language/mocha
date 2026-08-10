// Package classpath maps a binary class name to the bytes of its class file.
//
// A [Path] is an ordered search path of entries — directories, jars, and aars.
// Lookup is first-wins: the earliest entry that defines a name provides it, and
// later entries are shadowed. Nothing here decodes a class file; that is
// classfile's job, and this package does not import it.
//
// # Byte ownership
//
// Every [Class] returned owns its Data outright — a fresh allocation, never a
// window into a shared buffer. classfile.Class aliases the bytes it was read
// from, so a shared buffer would make the lifetime of one class depend on every
// other class read from the same jar. The copy costs one allocation per class
// read once.
//
// # Concurrency
//
// A Path is safe for concurrent use once built. Every index is immutable after
// Add returns, and the underlying *os.File is read through ReaderAt. Add and
// Close are not safe against concurrent Load.
package classpath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultRelease is the Java platform release used to select versioned entries
// from a multi-release jar when Options.Release is zero.
//
// Eight, not the current release. Versioned entries at N >= 9 exist precisely
// because they use APIs or class file features from release N, and mocha's
// encoder is capped at class file 49.0. Selecting them by default would pull in
// code the backend cannot emit. A caller that knows better sets Release.
const DefaultRelease = 8

// Options configures a Path.
type Options struct {
	// Release is the Java platform release to resolve multi-release jars
	// against. Zero means DefaultRelease. Values below 9 disable versioned
	// selection entirely, which is what the JAR specification requires.
	Release int
}

// Role records why an entry is on the path. It does not affect lookup — every
// role is searched, in the order added — but it does decide what a caller
// enumerates.
type Role uint8

const (
	// Input: compiled, and its code ships in the artifact.
	Input Role = iota
	// Classpath: consulted for signatures, never shipped.
	Classpath
	// Lib: consulted for signatures, never shipped, and never eligible to be
	// shipped. android.jar, whose implementation ART supplies.
	Lib
)

func (r Role) String() string {
	switch r {
	case Input:
		return "input"
	case Classpath:
		return "classpath"
	case Lib:
		return "lib"
	}
	return fmt.Sprintf("Role(%d)", uint8(r))
}

// Kind is the physical form of an entry.
type Kind uint8

const (
	KindDir Kind = iota
	KindJar
	KindAar
)

// Entry is one element of a search path.
//
// Names returns every binary name the entry defines, sorted, so that
// enumeration of an input is deterministic and so is the artifact built from
// it.
type Entry interface {
	Kind() Kind
	// Container is the file or directory this entry was opened from.
	Container() string
	// Class returns the bytes of one class, or an error wrapping ErrNotFound.
	Class(binary string) (*Class, error)
	// Names returns the binary names this entry defines, sorted.
	Names() ([]string, error)
	Close() error
}

// Class is one class file, with the bytes and where they came from.
type Class struct {
	Binary string // com/example/Foo
	Data   []byte // owned by the caller
	Origin Origin
}

// Path is an ordered search path.
type Path struct {
	opts    Options
	entries []Entry
	roles   []Role
}

// New returns an empty path.
func New(opts Options) *Path {
	if opts.Release == 0 {
		opts.Release = DefaultRelease
	}
	return &Path{opts: opts}
}

// Add opens name and appends it to the path. A directory becomes a directory
// entry; .jar and .zip become jars; .aar becomes an aar.
//
// A bare .class file is deliberately not accepted. Its binary name is not
// derivable from its path — only from this_class in the file itself — and
// reading it here would mean importing classfile and inverting the dependency
// this package is on the clean side of. A driver that wants to accept loose
// class files reads them itself and calls AddEntry with a [Static].
func (p *Path) Add(role Role, name string) error {
	fi, err := os.Stat(name)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		p.AddEntry(role, openDir(name))
		return nil
	}

	var e Entry
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jar", ".zip":
		e, err = openJar(name, p.opts.Release)
	case ".aar":
		e, err = openAar(name, p.opts.Release)
	case ".class":
		return fmt.Errorf("classpath: %s: a bare class file is not a path entry; "+
			"pass its directory, or register it with AddEntry", name)
	default:
		return fmt.Errorf("classpath: %s: unrecognised entry (want a directory, .jar, .zip or .aar)", name)
	}
	if err != nil {
		return err
	}
	p.AddEntry(role, e)
	return nil
}

// AddEntry appends an already-open entry.
func (p *Path) AddEntry(role Role, e Entry) {
	p.entries = append(p.entries, e)
	p.roles = append(p.roles, role)
}

// Load returns the first definition of binary on the path.
func (p *Path) Load(binary string) (*Class, error) {
	if !ValidBinaryName(binary) {
		return nil, fmt.Errorf("classpath: %q is not a binary name", binary)
	}
	for _, e := range p.entries {
		c, err := e.Class(binary)
		if err == nil {
			return c, nil
		}
		if !IsNotFound(err) {
			return nil, err
		}
	}
	return nil, &NotFoundError{Binary: binary}
}

// Has reports whether the path defines binary, without reading its bytes.
func (p *Path) Has(binary string) bool {
	for _, e := range p.entries {
		if h, ok := e.(interface{ has(string) bool }); ok {
			if h.has(binary) {
				return true
			}
			continue
		}
		if _, err := e.Class(binary); err == nil {
			return true
		}
	}
	return false
}

// Entries returns the entries added with the given role, in order.
func (p *Path) Entries(role Role) []Entry {
	var out []Entry
	for i, e := range p.entries {
		if p.roles[i] == role {
			out = append(out, e)
		}
	}
	return out
}

// Close releases every open file. Errors from individual entries are joined.
func (p *Path) Close() error {
	var errs []error
	for _, e := range p.entries {
		if err := e.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return join(errs)
}