package sym

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
)

// Enter is the first semantic phase. It walks a compilation unit, creates a
// symbol for every type it declares, and registers them with the table.
//
// Members are not entered here. Every type in every unit must exist before any
// member of any of them is looked at, or a forward reference between two
// top-level classes in one file would fail — so entry is eager and completion
// is lazy, and the laziness is what buys the two-pass behaviour without a
// second pass.
//
// Bodies are not walked. A local or anonymous class is declared inside a method
// body and cannot be named from outside it, so it is entered during attribution
// with the enclosing method's scope in hand. ClassSym.NextAnonymous is what
// numbers them when that happens.
func Enter(t *Table, file *ast.File) (*Unit, []token.Diagnostic) {
	e := &enterer{table: t, unit: file.Unit}
	u := &Unit{
		File:    file,
		Src:     file.Unit,
		Package: t.Unnamed,
		table:   t,
		single:  make(map[string]string),
		static:  make(map[string][]string),
	}

	if file.Package != nil {
		u.Package = t.Package(NameString(file.Package.Name, e.unit))
	}
	e.imports(u)

	if file.Module != nil {
		// A module declaration declares no types. Module resolution is not
		// modelled, so the unit is entered and its directives left alone.
		u.Module = file.Module
		return u, e.diags
	}

	for _, d := range file.Decls {
		if c := e.typeDecl(u, d, nil); c != nil {
			u.Types = append(u.Types, c)
		}
	}
	return u, e.diags
}

type enterer struct {
	table *Table
	unit  *token.File
	diags []token.Diagnostic
}

func (e *enterer) errorf(pos, end token.Pos, format string, args ...any) {
	e.diags = append(e.diags, errorAt(e.unit, pos, end, sprintf(format, args...)))
}

// typeDecl enters one type declaration and, recursively, its member types.
// outer is nil at the top level.
func (e *enterer) typeDecl(u *Unit, d ast.Decl, outer *ClassSym) *ClassSym {
	name, mods, kindFlags, members, ok := describe(d, e.unit)
	if !ok {
		return nil
	}
	if name == "" {
		return nil // a recovery node; the parser has already reported it
	}

	var binary string
	var owner Symbol
	pkg := u.Package
	if outer == nil {
		binary = TopLevelBinary(pkg.Internal, name)
		owner = pkg
	} else {
		binary = NestedBinary(outer.Binary, name)
		owner = outer
		pkg = outer.Package
	}

	c := &ClassSym{
		Sym: Sym{
			Name:  name,
			Kind:  KindClass,
			Flags: kindFlags | modifierFlags(mods) | annotationFlags(mods, e.unit),
			Owner: owner,
			Pos:   d.Pos(),
			End:   d.End(),
			Unit:  e.unit,
		},
		Binary:     binary,
		Package:    pkg,
		Outer:      outer,
		Decl:       d,
		SourceFile: e.unit.Name(),
	}
	c.Members = NewScope(c, nil)
	c.Flags |= implicitTypeFlags(c)
	c.completer = &sourceCompleter{unit: u, entered: e}

	if prev := e.table.Declare(c); prev != nil {
		e.errorf(d.Pos(), d.End(), "%s is already declared", Dotted(binary))
		return nil
	}
	if outer == nil {
		if dup := pkg.Members.Enter(c); dup != nil {
			e.errorf(d.Pos(), d.End(), "%s is already declared in package %s", name, pkg.Dotted)
		}
	} else if dup := outer.Members.Enter(c); dup != nil {
		e.errorf(d.Pos(), d.End(), "%s is already declared in %s", name, Dotted(outer.Binary))
	}

	e.typeParams(c, typeParamsOf(d))

	// Member types are entered now, for the same reason top-level types are:
	// a member class may be named before its declaration.
	for _, m := range members {
		e.typeDecl(u, m, c)
	}
	return c
}

func (e *enterer) typeParams(c *ClassSym, tp *ast.TypeParams) {
	if tp == nil {
		return
	}
	for i, p := range tp.List {
		s := &TypeParamSym{
			Sym: Sym{
				Name:  identName(p.Name, e.unit),
				Kind:  KindTypeParam,
				Owner: c,
				Pos:   p.Pos(),
				End:   p.End(),
				Unit:  e.unit,
			},
			Index:  i,
			Bounds: p.Bounds,
			Decl:   p,
		}
		c.TypeParams = append(c.TypeParams, s)
		if dup := c.Members.Enter(s); dup != nil {
			e.errorf(p.Pos(), p.End(), "type parameter %s is already declared", s.Name)
		}
	}
}

// describe pulls the four things every type declaration has out of the five
// node types that can be one.
func describe(d ast.Decl, unit *token.File) (name string, mods *ast.Modifiers, flags Flags, members []ast.Decl, ok bool) {
	switch t := d.(type) {
	case *ast.ClassDecl:
		return identName(t.Name, unit), t.Mods, 0, t.Members, true
	case *ast.InterfaceDecl:
		return identName(t.Name, unit), t.Mods, FlagInterface | FlagAbstract, t.Members, true
	case *ast.AnnotationDecl:
		return identName(t.Name, unit), t.Mods, FlagInterface | FlagAbstract | FlagAnnotation, t.Members, true
	case *ast.EnumDecl:
		return identName(t.Name, unit), t.Mods, FlagEnum | FlagFinal, t.Members, true
	case *ast.RecordDecl:
		return identName(t.Name, unit), t.Mods, FlagRecord | FlagFinal, t.Members, true
	}
	return "", nil, 0, nil, false
}

func typeParamsOf(d ast.Decl) *ast.TypeParams {
	switch t := d.(type) {
	case *ast.ClassDecl:
		return t.TypeParams
	case *ast.InterfaceDecl:
		return t.TypeParams
	case *ast.RecordDecl:
		return t.TypeParams
	}
	return nil
}

// implicitTypeFlags applies the modifiers §8 and §9 give a declaration without
// its writing them: a nested type of an interface is public static, an
// interface member is public, an enum with no constant body is final.
func implicitTypeFlags(c *ClassSym) Flags {
	var f Flags
	if c.Outer != nil && c.Outer.IsInterface() {
		f |= FlagPublic | FlagStatic
	}
	if c.IsInterface() || c.IsEnum() || c.IsRecord() {
		f |= FlagStatic // a member interface, enum or record is always static
	}
	if c.Outer == nil {
		f &^= FlagStatic
	}
	return f
}

// NextAnonymous returns the number the next anonymous class of this class's
// body takes. §13.1 numbers them from one, per innermost enclosing class.
func (c *ClassSym) NextAnonymous() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextAnon++
	return c.nextAnon
}