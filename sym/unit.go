package sym

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
)

// Unit is one compilation unit's resolution environment: its package, its
// imports, and the types it declares. attr resolves a simple type name through
// FindType, which implements §6.5.5's order and nothing else — the scope rules
// inside a class body are attr's, not this package's.
type Unit struct {
	File    *ast.File
	Src     *token.File
	Package *PackageSym
	Types   []*ClassSym // top-level types, in declaration order
	Module  *ast.ModuleDecl

	table *Table

	single   map[string]string   // simple name -> binary name
	onDemand []string            // internal-form package names
	static   map[string][]string // member name -> owning binary names
	staticOn []string            // binary names imported statically on demand
}

// imports records the unit's import declarations. Nothing is resolved here
// beyond finding the type a single-type import names: an on-demand import is a
// search path, not a set of declarations, and resolving it eagerly would mean
// enumerating every package it mentions whether or not the unit uses one.
func (e *enterer) imports(u *Unit) {
	for _, imp := range u.File.Imports {
		name := NameString(imp.Name, e.unit)
		switch {
		case imp.Module:
			// §7.5.5 names a module, and module resolution is not modelled.
			e.errorf(imp.Pos(), imp.End(),
				"module import of %s is not supported", name)

		case imp.Static && imp.OnDemand:
			u.staticOn = append(u.staticOn, Internal(name))

		case imp.Static:
			// The last component is the member; everything before it is the
			// type. Which of the remaining components are packages and which
			// are enclosing types is not decidable here.
			owner, member, ok := splitLast(name)
			if !ok {
				e.errorf(imp.Pos(), imp.End(), "malformed static import")
				continue
			}
			u.static[member] = append(u.static[member], Internal(owner))

		case imp.OnDemand:
			u.onDemand = append(u.onDemand, Internal(name))

		default:
			binary := e.resolveImport(name)
			if binary == "" {
				e.errorf(imp.Pos(), imp.End(), "cannot resolve import %s", name)
				continue
			}
			simple := SimpleName(binary)
			if prev, dup := u.single[simple]; dup && prev != binary {
				e.errorf(imp.Pos(), imp.End(),
					"%s is already imported as %s", simple, Dotted(prev))
				continue
			}
			u.single[simple] = binary
		}
	}
}

// resolveImport turns a dotted name into a binary name. `a.b.C.D` may be a
// package `a.b` with a nested type `C$D` or a package `a.b.C` with a type `D`,
// and only the path can say which — so the split is tried from the right.
func (e *enterer) resolveImport(dotted string) string {
	parts := split(dotted, '.')
	for i := len(parts) - 1; i > 0; i-- {
		pkg := join(parts[:i], "/")
		binary := pkg + "/" + join(parts[i:], "$")
		if _, ok := e.table.Existing(binary); ok {
			return binary
		}
		if e.table.Class(binary) != nil {
			return binary
		}
	}
	// The unnamed package: a single-component name has no package part.
	if len(parts) == 1 && e.table.Class(parts[0]) != nil {
		return parts[0]
	}
	return ""
}

// FindType resolves a simple type name in this unit's scope, following §6.5.5:
// a single-type import, then a type of this package, then an on-demand import,
// then java.lang. It returns nil when nothing matches; the caller decides
// whether that is an error and where to report it.
//
// Types declared in an enclosing class or method are *not* searched here. Those
// shadow everything below and belong to attr's scope chain.
func (u *Unit) FindType(simple string) *ClassSym {
	if binary, ok := u.single[simple]; ok {
		return u.table.Class(binary)
	}
	if c := u.packageType(simple); c != nil {
		return c
	}

	// An on-demand import is ambiguous when two of them supply the name.
	var found *ClassSym
	for _, pkg := range u.onDemand {
		if c := u.table.Class(TopLevelBinary(pkg, simple)); c != nil {
			if found != nil && found.Binary != c.Binary {
				return nil // ambiguous; attr reports it against the use site
			}
			found = c
		}
	}
	if found != nil {
		return found
	}
	return u.table.Class(TopLevelBinary("java/lang", simple))
}

// packageType finds a type declared in this unit's own package, source first.
func (u *Unit) packageType(simple string) *ClassSym {
	if s := u.Package.Members.LookupKind(simple, KindClass); s != nil {
		return s.(*ClassSym)
	}
	return u.table.Class(TopLevelBinary(u.Package.Internal, simple))
}

// FindStatic returns the types a single-static import names as possible owners
// of a member, in import order, followed by every static-on-demand type. A
// static import names a member, not a type, so which of the candidates actually
// declares it is an overload question and therefore attr's.
func (u *Unit) FindStatic(member string) []*ClassSym {
	var out []*ClassSym
	for _, owner := range u.static[member] {
		if c := u.table.Class(owner); c != nil {
			out = append(out, c)
		}
	}
	for _, owner := range u.staticOn {
		if c := u.table.Class(owner); c != nil {
			out = append(out, c)
		}
	}
	return out
}

// Table returns the table this unit resolves against.
func (u *Unit) Table() *Table { return u.table }

// --- small string helpers, kept here so name.go stays about names ------------

func splitLast(dotted string) (head, last string, ok bool) {
	for i := len(dotted) - 1; i >= 0; i-- {
		if dotted[i] == '.' {
			return dotted[:i], dotted[i+1:], true
		}
	}
	return "", "", false
}

func split(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func join(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	n := len(sep) * (len(parts) - 1)
	for _, p := range parts {
		n += len(p)
	}
	b := make([]byte, 0, n)
	for i, p := range parts {
		if i > 0 {
			b = append(b, sep...)
		}
		b = append(b, p...)
	}
	return string(b)
}