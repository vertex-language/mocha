package sym

import (
	"sync"

	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/classpath"
	"github.com/vertex-language/mocha/token"
)

// readMode is what a symbol table needs from a class file and no more. Method
// bodies and debug tables carry nothing resolution can use, and skipping them
// is most of the decode time on a stub jar like android.jar.
const readMode = classfile.SkipCode | classfile.SkipDebug

// Table is the symbol table for one compilation. It owns every ClassSym and
// PackageSym in play, whether entered from source or completed from the path.
//
// A Table is safe for concurrent use. classpath.Path is immutable once built,
// which is what lets completers run in parallel.
type Table struct {
	path *classpath.Path

	mu       sync.RWMutex
	classes  map[string]*ClassSym    // by binary name
	packages map[string]*PackageSym  // by dotted name

	// Unnamed is the unnamed package (§7.4.2), which every unit without a
	// package declaration belongs to.
	Unnamed *PackageSym
}

// NewTable returns an empty table resolving against p, which may be nil for a
// table with no class path at all.
func NewTable(p *classpath.Path) *Table {
	t := &Table{
		path:     p,
		classes:  make(map[string]*ClassSym),
		packages: make(map[string]*PackageSym),
	}
	t.Unnamed = t.Package("")
	return t
}

// Package returns the package with the given dotted name, creating it if this
// is the first mention. A package always exists: §7.4.3 makes them observable
// rather than declared, and nothing here can prove one empty.
func (t *Table) Package(dotted string) *PackageSym {
	t.mu.RLock()
	p := t.packages[dotted]
	t.mu.RUnlock()
	if p != nil {
		return p
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if p = t.packages[dotted]; p != nil {
		return p
	}
	p = &PackageSym{
		Sym:      Sym{Name: lastComponent(dotted), Kind: KindPackage},
		Dotted:   dotted,
		Internal: Internal(dotted),
		table:    t,
	}
	p.Members = NewScope(p, nil)
	t.packages[dotted] = p
	return p
}

func lastComponent(dotted string) string {
	for i := len(dotted) - 1; i >= 0; i-- {
		if dotted[i] == '.' {
			return dotted[i+1:]
		}
	}
	return dotted
}

// Existing returns the class symbol for a binary name if the table already has
// one, without consulting the class path.
func (t *Table) Existing(binary string) (*ClassSym, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.classes[binary]
	return c, ok
}

// Class returns the class symbol for a binary name, reading it from the class
// path if the table has not seen it. It returns nil when nothing on the path
// defines the name.
//
// The returned symbol is a stub: its members arrive on the first Complete.
func (t *Table) Class(binary string) *ClassSym {
	if c, ok := t.Existing(binary); ok {
		return c
	}
	if t.path == nil || !t.path.Has(binary) {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if c, ok := t.classes[binary]; ok {
		return c
	}
	c := t.stub(binary)
	c.completer = &binaryCompleter{table: t, binary: binary}
	t.classes[binary] = c
	return c
}

// stub builds an unpopulated ClassSym. The caller holds t.mu.
func (t *Table) stub(binary string) *ClassSym {
	pkg := t.packageLocked(Dotted(PackageOf(binary)))
	c := &ClassSym{
		Sym:     Sym{Name: SimpleName(binary), Kind: KindClass, Owner: pkg},
		Binary:  binary,
		Package: pkg,
	}
	c.Members = NewScope(c, nil)
	return c
}

// packageLocked is Package for a caller that already holds t.mu.
func (t *Table) packageLocked(dotted string) *PackageSym {
	if p := t.packages[dotted]; p != nil {
		return p
	}
	p := &PackageSym{
		Sym:      Sym{Name: lastComponent(dotted), Kind: KindPackage},
		Dotted:   dotted,
		Internal: Internal(dotted),
		table:    t,
	}
	p.Members = NewScope(p, nil)
	t.packages[dotted] = p
	return p
}

// Declare registers a class entered from source. It returns the class already
// declared under that binary name, or nil on success — a duplicate is a real
// error, since two source files cannot define the same type.
//
// A source declaration wins over a class file of the same name: you are
// compiling it, so the version on the path is stale by definition.
func (t *Table) Declare(c *ClassSym) *ClassSym {
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.classes[c.Binary]; ok && prev.FromSource() {
		return prev
	}
	t.classes[c.Binary] = c
	return nil
}

// Members lists the types the path defines directly in a package, which is
// what an on-demand import needs. It does not recurse into subpackages.
func (t *Table) Members(pkgInternal string) []string {
	if t.path == nil {
		return nil
	}
	prefix := pkgInternal + "/"
	var out []string
	for _, role := range []classpath.Role{classpath.Input, classpath.Classpath, classpath.Lib} {
		for _, e := range t.path.Entries(role) {
			names, err := e.Names()
			if err != nil {
				continue
			}
			for _, n := range names {
				if pkgInternal == "" {
					if !hasByte(n, '/') {
						out = append(out, n)
					}
					continue
				}
				if len(n) > len(prefix) && n[:len(prefix)] == prefix &&
					!hasByte(n[len(prefix):], '/') {
					out = append(out, n)
				}
			}
		}
	}
	return out
}

func hasByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}

// load reads and decodes one class file from the path.
func (t *Table) load(binary string) (*classfile.Class, error) {
	if t.path == nil {
		return nil, &classpath.NotFoundError{Binary: binary}
	}
	raw, err := t.path.Load(binary)
	if err != nil {
		return nil, err
	}
	return classfile.Read(raw.Data, readMode)
}

// --- well-known types -------------------------------------------------------

// The internal names attribution needs by name rather than by resolution: a
// string concatenation produces one, an enhanced for consumes one, a
// try-with-resources requires one. Nothing here fails if they are absent; a
// unit that never touches them compiles against a path that lacks them.
const (
	ObjectName        = "java/lang/Object"
	StringName        = "java/lang/String"
	ClassName         = "java/lang/Class"
	ThrowableName     = "java/lang/Throwable"
	EnumName          = "java/lang/Enum"
	RecordName        = "java/lang/Record"
	IterableName      = "java/lang/Iterable"
	AutoCloseableName = "java/lang/AutoCloseable"
	SerializableName  = "java/io/Serializable"
)

// Object returns the symbol for java.lang.Object, or nil if the path has none.
func (t *Table) Object() *ClassSym { return t.Class(ObjectName) }

// JavaLang returns the java.lang package, whose types every unit imports on
// demand (§7.3).
func (t *Table) JavaLang() *PackageSym { return t.Package("java.lang") }

// Diagnostic builds an error at a span in a unit.
func errorAt(unit *token.File, pos, end token.Pos, msg string) token.Diagnostic {
	if end <= pos {
		end = pos + 1
	}
	return token.Diagnostic{Pos: pos, End: end, Severity: token.SevError, Msg: msg}
}