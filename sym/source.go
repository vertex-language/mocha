package sym

import (
	"fmt"

	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
)

// sourceCompleter enters the members of a class declared in source. It runs on
// the first Complete, which is after every type in the unit has been entered —
// so a member's type may name any of them, in any order.
type sourceCompleter struct {
	unit    *Unit
	entered *enterer
}

func (sc *sourceCompleter) Complete(c *ClassSym) error {
	unit := sc.unit.Src
	switch d := c.Decl.(type) {
	case *ast.ClassDecl:
		sc.members(c, d.Members)
	case *ast.InterfaceDecl:
		sc.members(c, d.Members)
	case *ast.AnnotationDecl:
		sc.members(c, d.Members)
	case *ast.EnumDecl:
		sc.enumConstants(c, d.Constants)
		sc.members(c, d.Members)
	case *ast.RecordDecl:
		sc.recordComponents(c, d.Components)
		sc.members(c, d.Members)
	case nil:
		return nil
	default:
		return fmt.Errorf("sym: %s was entered from a %T", Dotted(c.Binary), d)
	}
	_ = unit
	return nil
}

func (sc *sourceCompleter) members(c *ClassSym, members []ast.Decl) {
	for _, m := range members {
		switch d := m.(type) {
		case *ast.VarDecl:
			sc.fields(c, d)
		case *ast.MethodDecl:
			sc.method(c, d)
		case *ast.ConstructorDecl:
			sc.constructor(c, d)
		case *ast.AnnotationElemDecl:
			sc.element(c, d)
		case *ast.InitializerDecl, *ast.EmptyDecl, *ast.BadDecl:
			// An initializer declares nothing; a stray semicolon less so.
		default:
			// A member type, already entered by Enter before any completion.
		}
	}
}

func (sc *sourceCompleter) fields(c *ClassSym, d *ast.VarDecl) {
	e := sc.entered
	flags := modifierFlags(d.Mods) | annotationFlags(d.Mods, e.unit)
	if c.IsInterface() {
		// §9.3: a field of an interface is implicitly public static final,
		// whether or not any of the three was written.
		flags |= FlagPublic | FlagStatic | FlagFinal
	}
	for _, decl := range d.Names {
		v := &VarSym{
			Sym: Sym{
				Name:  identName(decl.Name, e.unit),
				Kind:  KindVar,
				Flags: flags,
				Owner: c,
				Pos:   decl.Pos(),
				End:   decl.End(),
				Unit:  e.unit,
			},
			Var:      VarField,
			Class:    c,
			TypeExpr: d.Type,
			Decl:     decl,
		}
		if dup := c.Members.Enter(v); dup != nil {
			e.errorf(decl.Pos(), decl.End(),
				"%s is already declared in %s", v.Name, Dotted(c.Binary))
		}
	}
}

func (sc *sourceCompleter) method(c *ClassSym, d *ast.MethodDecl) {
	e := sc.entered
	flags := modifierFlags(d.Mods) | annotationFlags(d.Mods, e.unit)
	if c.IsInterface() {
		// §9.4: an interface method with no body is public abstract; one with
		// a body is public and was marked default or static explicitly.
		flags |= FlagPublic
		if d.Body == nil && !flags.Has(FlagStatic) {
			flags |= FlagAbstract
		}
	}
	m := &MethodSym{
		Sym: Sym{
			Name:  identName(d.Name, e.unit),
			Kind:  KindMethod,
			Flags: flags,
			Owner: c,
			Pos:   d.Pos(),
			End:   d.End(),
			Unit:  e.unit,
		},
		Class:      c,
		Result:     d.Result,
		ThrowsExpr: d.Throws,
		Decl:       d,
	}
	sc.methodTypeParams(m, d.TypeParams)
	sc.params(m, d.Params)
	if dup := c.Members.Enter(m); dup != nil {
		e.errorf(d.Pos(), d.End(), "%s is already declared in %s", m.Name, Dotted(c.Binary))
	}
}

func (sc *sourceCompleter) constructor(c *ClassSym, d *ast.ConstructorDecl) {
	e := sc.entered
	m := &MethodSym{
		Sym: Sym{
			Name:  InitName,
			Kind:  KindMethod,
			Flags: modifierFlags(d.Mods) | annotationFlags(d.Mods, e.unit),
			Owner: c,
			Pos:   d.Pos(),
			End:   d.End(),
			Unit:  e.unit,
		},
		Class:      c,
		ThrowsExpr: d.Throws,
		Decl:       d,
	}
	sc.methodTypeParams(m, d.TypeParams)
	if d.Compact {
		// A compact constructor has no parameter list: it takes the record's
		// components, which are not resolved yet. types fills them in.
		m.Flags |= FlagImplicit
	} else {
		sc.params(m, d.Params)
	}
	c.Members.Enter(m)
}

func (sc *sourceCompleter) element(c *ClassSym, d *ast.AnnotationElemDecl) {
	e := sc.entered
	m := &MethodSym{
		Sym: Sym{
			Name:  identName(d.Name, e.unit),
			Kind:  KindMethod,
			Flags: modifierFlags(d.Mods) | FlagPublic | FlagAbstract,
			Owner: c,
			Pos:   d.Pos(),
			End:   d.End(),
			Unit:  e.unit,
		},
		Class:   c,
		Result:  d.Type,
		Decl:    d,
		Default: d.Default,
	}
	if dup := c.Members.Enter(m); dup != nil {
		e.errorf(d.Pos(), d.End(), "%s is already declared in %s", m.Name, Dotted(c.Binary))
	}
}

func (sc *sourceCompleter) params(m *MethodSym, params []*ast.Param) {
	e := sc.entered
	seen := NewScope(m, nil)
	for _, p := range params {
		v := &VarSym{
			Sym: Sym{
				Name:  identName(p.Name, e.unit),
				Kind:  KindVar,
				Flags: modifierFlags(p.Mods),
				Owner: m,
				Pos:   p.Pos(),
				End:   p.End(),
				Unit:  e.unit,
			},
			Var:      VarParam,
			Method:   m,
			TypeExpr: p.Type,
			Decl:     p,
		}
		if p.Ellipsis.IsValid() {
			m.Flags |= FlagVarargs
		}
		if dup := seen.Enter(v); dup != nil {
			e.errorf(p.Pos(), p.End(), "parameter %s is already declared", v.Name)
		}
		m.Params = append(m.Params, v)
	}
}

func (sc *sourceCompleter) methodTypeParams(m *MethodSym, tp *ast.TypeParams) {
	if tp == nil {
		return
	}
	e := sc.entered
	for i, p := range tp.List {
		m.TypeParams = append(m.TypeParams, &TypeParamSym{
			Sym: Sym{
				Name:  identName(p.Name, e.unit),
				Kind:  KindTypeParam,
				Owner: m,
				Pos:   p.Pos(),
				End:   p.End(),
				Unit:  e.unit,
			},
			Index:  i,
			Bounds: p.Bounds,
			Decl:   p,
		})
	}
}

func (sc *sourceCompleter) enumConstants(c *ClassSym, consts []*ast.EnumConstant) {
	e := sc.entered
	for _, k := range consts {
		v := &VarSym{
			Sym: Sym{
				Name:  identName(k.Name, e.unit),
				Kind:  KindVar,
				Flags: FlagPublic | FlagStatic | FlagFinal | FlagEnum,
				Owner: c,
				Pos:   k.Pos(),
				End:   k.End(),
				Unit:  e.unit,
			},
			Var:   VarEnumConstant,
			Class: c,
			Decl:  k,
		}
		if dup := c.Members.Enter(v); dup != nil {
			e.errorf(k.Pos(), k.End(), "enum constant %s is already declared", v.Name)
		}
		// A constant with a class body declares an anonymous subclass. It is
		// entered during attribution, where the body's scope exists.
	}
}

// recordComponents enters a record's components, plus the field and accessor
// §8.10.3 declares for each. They are implicit, not synthetic: source can name
// them, and an explicitly declared accessor replaces the implicit one — which
// is why Enter returning a conflict here is not an error.
func (sc *sourceCompleter) recordComponents(c *ClassSym, comps []*ast.RecordComponent) {
	e := sc.entered
	for _, comp := range comps {
		name := identName(comp.Name, e.unit)
		rc := &VarSym{
			Sym: Sym{
				Name:  name,
				Kind:  KindVar,
				Flags: FlagImplicit | FlagPrivate | FlagFinal,
				Owner: c,
				Pos:   comp.Pos(),
				End:   comp.End(),
				Unit:  e.unit,
			},
			Var:      VarRecordComponent,
			Class:    c,
			TypeExpr: comp.Type,
			Decl:     comp,
		}
		c.recordComponents = append(c.recordComponents, rc)

		field := *rc
		field.Var = VarField
		c.Members.Enter(&field)

		accessor := &MethodSym{
			Sym: Sym{
				Name:  name,
				Kind:  KindMethod,
				Flags: FlagImplicit | FlagPublic,
				Owner: c,
				Pos:   comp.Pos(),
				End:   comp.End(),
				Unit:  e.unit,
			},
			Class:  c,
			Result: comp.Type,
			// No Decl: the implicit accessor has no MethodDecl of its own.
			// comp is an ast.Node (via RecordComponent), not an ast.Decl, so
			// it cannot fill MethodSym.Decl — record components aren't
			// members in the declaration sense, per the comment above.
		}
		c.Members.Enter(accessor)
	}
}

// sprintf keeps fmt out of enter.go's import list for one call site.
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

var _ = token.NoPos