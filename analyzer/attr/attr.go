// Package attr is attribution: it resolves every name in a method body, gives
// every expression a type, and reports the errors sym and types deliberately
// left to a phase that can see both.
//
// # Side tables, not tree mutation
//
// ast nodes hold no type field, and the parser's arena invalidates every node
// on Release. Attribution therefore returns an Info of maps keyed on nodes,
// valid exactly as long as the tree is: parse, enter, attribute, lower,
// release. Nothing here writes to the tree.
//
// # Errors do not cascade
//
// Every expression gets a type. A failed resolution yields types.ErrorType,
// which types.IsSubtype treats as compatible with everything, so one
// unresolvable name produces one diagnostic rather than one per enclosing
// expression. The parser's one-per-site rule holds here too.
package attr

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// Info is everything attribution learned about one compilation unit.
//
// Every map is keyed on a node of the unit's tree, so an Info does not outlive
// ast.File.Release. Consumers that need a fact past that lifetime copy it —
// a symbol pointer and a type, usually, both of which survive.
type Info struct {
	// Types holds the type of every expression, and of every ast.Type node
	// that was resolved.
	Types map[ast.Node]types.Type

	// Uses maps a name to what it denotes: a *sym.VarSym for a variable, a
	// *sym.MethodSym for the overload that was selected, a *sym.ClassSym for
	// a type name. This is what gen reads to emit an invocation.
	Uses map[ast.Node]sym.Symbol

	// Consts holds the value of every expression that is a constant
	// expression (§15.29). A `case` label and a `static final` initialiser
	// both need this.
	Consts map[ast.Node]types.Constant

	// Local holds the classes attribution entered: local classes, anonymous
	// classes, and enum constant bodies. sym.Enter does not walk bodies, so
	// these do not exist until now.
	Local map[ast.Decl]*sym.ClassSym

	Diags []token.Diagnostic
}

func newInfo() *Info {
	return &Info{
		Types:  make(map[ast.Node]types.Type),
		Uses:   make(map[ast.Node]sym.Symbol),
		Consts: make(map[ast.Node]types.Constant),
		Local:  make(map[ast.Decl]*sym.ClassSym),
	}
}

// Type returns the type recorded for a node, or an error type. Never nil, so a
// consumer can switch on Kind without a guard.
func (in *Info) Type(n ast.Node) types.Type {
	if t, ok := in.Types[n]; ok && t != nil {
		return t
	}
	return errType
}

// Use returns the symbol a node resolved to, or nil.
func (in *Info) Use(n ast.Node) sym.Symbol { return in.Uses[n] }

// Const returns the folded value of a node, and whether it had one.
func (in *Info) Const(n ast.Node) (types.Constant, bool) {
	c, ok := in.Consts[n]
	return c, ok && c.IsValid()
}

// errType is the shared error type. Comparing against it is not meaningful —
// use types.IsError — but handing out one value avoids an allocation per
// failure in a unit that has many.
var errType types.Type = &types.ErrorType{}

// Attr attributes one compilation unit.
//
// Every top-level type is completed and its bodies walked. A unit that
// declares a module declares no types and is returned untouched.
func Attr(tt *types.Table, u *sym.Unit) *Info {
	in := newInfo()
	if u == nil || u.Module != nil {
		return in
	}
	a := &attributor{types: tt, syms: u.Table(), unit: u, info: in}

	for _, c := range u.Types {
		a.class(c)
	}
	token.SortDiagnostics(in.Diags)
	return in
}

// attributor carries what every step needs. The mutable part of a walk lives
// in env, which is copied down rather than pushed and popped: a bug that
// forgets to restore a field then costs one wrong walk, not every walk after
// it.
type attributor struct {
	types *types.Table
	syms  *sym.Table
	unit  *sym.Unit
	info  *Info

	// reported guards the one-diagnostic-per-site rule.
	reported map[token.Pos]bool
}

// class attributes one type declaration and, recursively, its member types.
func (a *attributor) class(c *sym.ClassSym) {
	if c == nil || !c.FromSource() {
		return
	}
	if err := c.Complete(); err != nil {
		if err == sym.ErrCyclicCompletion {
			a.errorf(c.Pos, c.End, "the type hierarchy of %s is circular", sym.Dotted(c.Binary))
		}
		return
	}
	a.checkOverloads(c)

	e := a.classEnv(c)
	switch d := c.Decl.(type) {
	case *ast.ClassDecl:
		a.members(e, c, d.Members)
	case *ast.InterfaceDecl:
		a.members(e, c, d.Members)
	case *ast.AnnotationDecl:
		a.members(e, c, d.Members)
	case *ast.EnumDecl:
		a.enumConstants(e, c, d.Constants)
		a.members(e, c, d.Members)
	case *ast.RecordDecl:
		a.members(e, c, d.Members)
	}
}

func (a *attributor) members(e *env, c *sym.ClassSym, members []ast.Decl) {
	for _, m := range members {
		switch d := m.(type) {
		case *ast.MethodDecl:
			a.method(e, c, d)
		case *ast.ConstructorDecl:
			a.constructor(e, c, d)
		case *ast.VarDecl:
			a.fieldDecl(e, c, d)
		case *ast.InitializerDecl:
			a.initializer(e, c, d)
		case *ast.AnnotationElemDecl:
			a.annotationElem(e, c, d)
		case *ast.ClassDecl, *ast.InterfaceDecl, *ast.AnnotationDecl,
			*ast.EnumDecl, *ast.RecordDecl:
			// A member type, already entered by sym.Enter. Attribute it as a
			// class in its own right; its scope chain starts fresh at its own
			// class env, because a member type does not see the enclosing
			// method's locals.
			if nested := c.Nested(declName(d, e.file())); nested != nil {
				a.class(nested)
			}
		}
	}
}

func (a *attributor) method(e *env, c *sym.ClassSym, d *ast.MethodDecl) {
	m := a.methodSym(c, d)
	if m == nil {
		return
	}
	mt := a.types.MethodType(m)
	a.recordThrows(e, m)

	if d.Body == nil {
		// Abstract, native, or an interface method without a body. §8.4.3.2
		// and §8.4.3.4 make a body mandatory otherwise, but that is a
		// modifier check and belongs with the other ones in warn.
		return
	}
	me := a.methodEnv(e, m, mt)
	a.block(me, d.Body)
}

func (a *attributor) constructor(e *env, c *sym.ClassSym, d *ast.ConstructorDecl) {
	m := a.constructorSym(c, d)
	if m == nil {
		return
	}
	if d.Compact {
		// §8.10.4.2: a compact constructor's parameters are the record's
		// components. sym marked it implicit and left the list empty because
		// the components were not resolved yet. They are now.
		a.fillCompact(c, m)
	}
	mt := a.types.MethodType(m)
	me := a.methodEnv(e, m, mt)
	me.ctor = true
	if d.Body != nil {
		a.block(me, d.Body)
	}
}

// fillCompact gives a compact constructor the record's components as its
// parameters, in declaration order.
func (a *attributor) fillCompact(c *sym.ClassSym, m *sym.MethodSym) {
	if len(m.Params) > 0 {
		return
	}
	for _, comp := range recordComponents(c) {
		m.Params = append(m.Params, comp)
	}
}

func (a *attributor) initializer(e *env, c *sym.ClassSym, d *ast.InitializerDecl) {
	ie := e.child()
	ie.static = d.Static
	ie.scope = sym.NewScope(c, e.scope)
	ie.ret = nil
	if d.Body != nil {
		a.block(ie, d.Body)
	}
}

func (a *attributor) annotationElem(e *env, c *sym.ClassSym, d *ast.AnnotationElemDecl) {
	t := a.resolveType(e, d.Type)
	a.info.Types[d.Type] = t
	if d.Default != nil {
		if x, ok := d.Default.(ast.Expr); ok {
			a.expr(e, x, t)
		}
	}
}

// fieldDecl attributes a field's initialisers. A field initialiser runs in the
// static context of its own declaration, not of the class.
func (a *attributor) fieldDecl(e *env, c *sym.ClassSym, d *ast.VarDecl) {
	fe := e.child()
	fe.static = isStaticField(c, d)
	ft := a.resolveType(fe, d.Type)
	a.info.Types[d.Type] = ft

	for _, decl := range d.Names {
		v := a.fieldSym(c, decl, fe.file())
		declared := ft
		if len(decl.Dims) > 0 {
			declared = arrayOf(ft, len(decl.Dims))
		}
		if v != nil {
			a.info.Uses[decl] = v
			a.info.Types[decl] = declared
		}
		if decl.Init == nil {
			continue
		}
		val := a.initializerValue(fe, decl.Init, declared)

		// §4.12.4: a constant variable is a final field of primitive or String
		// type initialised by a constant expression. gen needs the folded
		// value for a ConstantValue attribute, and a case label needs it too.
		if v != nil && v.Flags.Has(sym.FlagFinal) && isConstantVarType(declared) {
			if k, ok := a.info.Const(decl.Init); ok {
				a.info.Consts[decl] = k
				_ = val
			}
		}
	}
}

// initializerValue attributes either an expression or an array initialiser,
// which is not an expression and has no type of its own.
func (a *attributor) initializerValue(e *env, init ast.Node, want types.Type) types.Type {
	switch n := init.(type) {
	case ast.Expr:
		return a.expr(e, n, want)
	case *ast.ArrayInit:
		return a.arrayInit(e, n, want)
	}
	return errType
}

// arrayInit checks each element against the array's component type. An array
// initialiser is not an expression (§10.6): it takes its type entirely from
// context, which is why `want` is not optional here.
func (a *attributor) arrayInit(e *env, n *ast.ArrayInit, want types.Type) types.Type {
	at, ok := want.(*types.ArrayType)
	if !ok {
		if !types.IsError(want) {
			a.errorf(n.Pos(), n.End(), "array initializer is not allowed here")
		}
		a.info.Types[n] = errType
		return errType
	}
	for _, elt := range n.Elts {
		a.initializerValue(e, elt, at.Elem)
	}
	a.info.Types[n] = at
	return at
}

// checkOverloads reports two methods that share a name and an erased
// signature. sym.Scope deliberately admits them — detecting the clash needs
// erasure, which needs types — so this is the first phase that can.
func (a *attributor) checkOverloads(c *sym.ClassSym) {
	seen := map[string]*sym.MethodSym{}
	c.Members.Each(func(s sym.Symbol) bool {
		m, ok := s.(*sym.MethodSym)
		if !ok || m.Flags.Has(sym.FlagImplicit) {
			return true
		}
		key := m.Name + types.MethodDescriptor(a.types.MethodType(m))
		if prev, dup := seen[key]; dup {
			a.errorf(m.Pos, m.End,
				"%s%s is already declared in %s",
				m.Name, paramList(a.types.MethodType(m)), sym.Dotted(c.Binary))
			_ = prev
			return true
		}
		seen[key] = m
		return true
	})
}