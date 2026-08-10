package classpath

import (
	"errors"
	"fmt"
	"path/filepath"
)

// Origin records where a class file was found. It exists so that a decode
// failure names something a user can act on, rather than an anonymous byte
// slice.
type Origin struct {
	Kind      Kind
	Container string // directory root, jar path, or aar path
	Nested    string // "classes.jar" or "libs/foo.jar" when inside an aar
	Entry     string // path within the container
	Release   int    // versioned directory N, or 0 for a base entry
}

// String renders the origin in the shape each container is conventionally
// written: a filesystem path for a directory, jar!/entry for an archive.
func (o Origin) String() string {
	switch o.Kind {
	case KindDir:
		return filepath.Join(o.Container, filepath.FromSlash(o.Entry))
	case KindAar:
		if o.Nested != "" {
			return o.Container + "!/" + o.Nested + "!/" + o.Entry
		}
	}
	return o.Container + "!/" + o.Entry
}

// Versioned reports whether this class came from a multi-release versioned
// directory.
func (o Origin) Versioned() bool { return o.Release > 0 }

// ErrNotFound is the sentinel every miss wraps.
var ErrNotFound = errors.New("class not found")

// NotFoundError reports that no entry defines a name.
type NotFoundError struct {
	Binary    string
	Container string // set when a single entry reports the miss
}

func (e *NotFoundError) Error() string {
	if e.Container != "" {
		return fmt.Sprintf("%s: not in %s", e.Binary, e.Container)
	}
	return fmt.Sprintf("%s: not on the class path", e.Binary)
}

func (e *NotFoundError) Unwrap() error { return ErrNotFound }

// IsNotFound reports whether err is a miss rather than a real failure.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// Error is an I/O or format failure, named by origin.
type Error struct {
	Origin Origin
	Err    error
}

func (e *Error) Error() string { return e.Origin.String() + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func join(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	}
	return errors.Join(errs...)
}