package attr

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// Statement attribution, §14. Reachability and definite assignment are not
// here — they are flow's, and javac splits them the same way. What this file
// owns is that every name in a statement resolves and every expression has the
// type the statement requires.

func (a *attributor) block(e *env, b *ast.Block) {
	if b == nil {
		return
	}
	be := e.block(nil)
	for _, s := range b.Stmts {
		a.stmt(be, s)
	}
}

func (a *attributor) stmt(e *env, s ast.Stmt) {
	switch n := s.(type) {
	case *ast.Block:
		a.block(e, n)

	case *ast.EmptyStmt, *ast.BadStmt:

	case *ast.ExprStmt:
		a.expr(e, n.X, nil)

	case *ast.DeclStmt:
		a.declStmt(e, n)

	case *ast.LabeledStmt:
		le := e.child()
		name := identText(n.Label, e.file())
		if e.hasLabel(name) {
			a.errorf(n.Label.Pos(), n.Label.End(), "label %s is already in use", name)
		}
		le.labels = append(append([]string{}, e.labels...), name)
		a.stmt(le, n.Stmt)

	case *ast.IfStmt:
		a.condition(e, n.Cond)
		a.stmt(e, n.Then)
		if n.Else != nil {
			a.stmt(e, n.Else)
		}

	case *ast.WhileStmt:
		a.condition(e, n.Cond)
		le := e.child()
		le.loop = true
		a.stmt(le, n.Body)

	case *ast.DoStmt:
		le := e.child()
		le.loop = true
		a.stmt(le, n.Body)
		a.condition(e, n.Cond)

	case *ast.ForStmt:
		a.forStmt(e, n)

	case *ast.RangeStmt:
		a.rangeStmt(e, n)

	case *ast.SwitchStmt:
		a.switchStmt(e, n)

	case *ast.BreakStmt:
		a.breakStmt(e, n)

	case *ast.ContinueStmt:
		a.continueStmt(e, n)

	case *ast.ReturnStmt:
		a.returnStmt(e, n)

	case *ast.YieldStmt:
		if e.yield == nil {
			a.errorf(n.Pos(), n.End(), "yield outside of a switch expression")
			a.expr(e, n.X, nil)
			return
		}
		a.expr(e, n.X, e.yield)

	case *ast.ThrowStmt:
		t := a.expr(e, n.X, nil)
		if !a.isThrowable(t) && !types.IsError(t) {
			a.errorf(n.X.Pos(), n.X.End(), "%s is not a subclass of java.lang.Throwable", t)
		}

	case *ast.SyncStmt:
		t := a.expr(e, n.X, nil)
		if !types.IsReference(t) && !types.IsError(t) {
			a.errorf(n.X.Pos(), n.X.End(), "synchronized requires a reference, not %s", t)
		}
		a.block(e, n.Body)

	case *ast.AssertStmt:
		a.condition(e, n.X)
		if n.Msg != nil {
			a.expr(e, n.Msg, nil)
		}

	case *ast.TryStmt:
		a.tryStmt(e, n)

	case *ast.ConstructorCall:
		a.constructorCall(e, n)
	}
}

// condition attributes an expression that must be boolean, unboxing where §5.1.8
// permits it.
func (a *attributor) condition(e *env, x ast.Expr) {
	if x == nil {
		return
	}
	t := a.expr(e, x, nil)
	if a.unboxed(t).Kind() != types.KindBoolean && !types.IsError(t) {
		a.errorf(x.Pos(), x.End(), "condition must be boolean, not %s", t)
	}
}

// declStmt covers a local variable declaration and a local class declaration,
// which the tree keeps in one node because they occupy one statement position.
func (a *attributor) declStmt(e *env, n *ast.DeclStmt) {
	switch d := n.Decl.(type) {
	case *ast.VarDecl:
		a.localVars(e, d)
	case *ast.ClassDecl, *ast.InterfaceDecl, *ast.EnumDecl, *ast.RecordDecl:
		a.localClass(e, d)
	}
}

// localVars declares locals, resolving `var` from the initialiser (§14.4.1).
func (a *attributor) localVars(e *env, d *ast.VarDecl) {
	_, isVar := d.Type.(*ast.VarType)
	var base types.Type
	if !isVar {
		base = a.resolveType(e, d.Type)
		a.info.Types[d.Type] = base
	}

	for _, decl := range d.Names {
		name := identText(decl.Name, e.file())
		var t types.Type

		switch {
		case isVar:
			if decl.Init == nil {
				a.errorf(decl.Pos(), decl.End(), "cannot infer type for %s: no initializer", name)
				t = errType
				break
			}
			if _, isArr := decl.Init.(*ast.ArrayInit); isArr {
				// §14.4.1: an array initialiser has no standalone type, so it
				// cannot be what `var` infers from.
				a.errorf(decl.Pos(), decl.End(),
					"cannot infer type for %s from an array initializer", name)
				t = errType
				break
			}
			t = a.initializerValue(e, decl.Init, nil)
			// The inferred type is the *standalone* type with null and the
			// null type excluded, and no capture: `var x = null` is an error.
			if t.Kind() == types.KindNull {
				a.errorf(decl.Pos(), decl.End(), "cannot infer type for %s from null", name)
				t = errType
			}
		default:
			t = arrayOf(base, len(decl.Dims))
			if decl.Init != nil {
				a.initializerValue(e, decl.Init, t)
			}
		}

		v := &sym.VarSym{
			Sym: sym.Sym{
				Name:  name,
				Kind:  sym.KindVar,
				Flags: modifierFlags(d.Mods),
				Owner: e.method,
				Pos:   decl.Pos(),
				End:   decl.End(),
				Unit:  e.file(),
			},
			Var:      sym.VarLocal,
			Method:   e.method,
			TypeExpr: d.Type,
			Decl:     decl,
		}
		e.declare(v, t)
		a.info.Uses[decl] = v
		a.info.Types[decl] = t
	}
}

// localClass enters a class declared in a method body. sym.Enter deliberately
// does not walk bodies — a local class cannot be named from outside one — so
// this is where it comes into existence, with the enclosing method's scope in
// hand.
func (a *attributor) localClass(e *env, d ast.Decl) {
	if e.class == nil {
		return
	}
	name := declName(d, e.file())
	if name == "" {
		return
	}
	n := e.class.NextAnonymous()
	binary := sym.LocalBinary(e.class.Binary, n, name)

	c := &sym.ClassSym{
		Sym: sym.Sym{
			Name:  name,
			Kind:  sym.KindClass,
			Owner: e.class,
			Pos:   d.Pos(),
			End:   d.End(),
			Unit:  e.file(),
		},
		Binary:     binary,
		Package:    e.class.Package,
		Outer:      e.class,
		Decl:       d,
		SourceFile: e.class.SourceFile,
	}
	c.Members = sym.NewScope(c, nil)
	a.syms.Declare(c)
	a.info.Local[d] = c

	// The class is entered into the *enclosing scope* so later statements can
	// name it, but its own body is attributed with a fresh class env: a local
	// class body does not see the method's locals as its own members.
	a.class(c)
}

func (a *attributor) forStmt(e *env, n *ast.ForStmt) {
	fe := e.block(nil)
	for _, init := range n.Init {
		a.stmt(fe, init)
	}
	a.condition(fe, n.Cond)
	for _, p := range n.Post {
		a.expr(fe, p, nil)
	}
	be := fe.child()
	be.loop = true
	a.stmt(be, n.Body)
}

// rangeStmt is the enhanced for of §14.14.2: the iterable is either an array
// or an Iterable, and the loop variable's type comes from the element type.
func (a *attributor) rangeStmt(e *env, n *ast.RangeStmt) {
	fe := e.block(nil)
	src := a.expr(fe, n.X, nil)
	elem := a.elementType(n, src)

	if n.Decl != nil {
		_, isVar := n.Decl.Type.(*ast.VarType)
		declared := elem
		if !isVar {
			declared = a.resolveType(fe, n.Decl.Type)
			a.info.Types[n.Decl.Type] = declared
			if !types.IsError(elem) && !a.assignableTo(elem, declared) {
				a.errorf(n.Decl.Pos(), n.Decl.End(),
					"incompatible types: %s cannot be converted to %s", elem, declared)
			}
		}
		for _, decl := range n.Decl.Names {
			v := &sym.VarSym{
				Sym: sym.Sym{
					Name:  identText(decl.Name, fe.file()),
					Kind:  sym.KindVar,
					Flags: modifierFlags(n.Decl.Mods),
					Owner: fe.method,
					Pos:   decl.Pos(),
					End:   decl.End(),
					Unit:  fe.file(),
				},
				Var:    sym.VarLocal,
				Method: fe.method,
				Decl:   decl,
			}
			fe.declare(v, declared)
			a.info.Uses[decl] = v
		}
	}
	be := fe.child()
	be.loop = true
	a.stmt(be, n.Body)
}

// elementType is what a for-each yields per iteration.
func (a *attributor) elementType(n *ast.RangeStmt, src types.Type) types.Type {
	if at, ok := src.(*types.ArrayType); ok {
		return at.Elem
	}
	if types.IsError(src) {
		return errType
	}
	iter := a.syms.Class(sym.IterableName)
	if iter == nil {
		return a.types.Object()
	}
	ct, ok := src.(*types.ClassType)
	if !ok || !a.types.IsSubtype(src, a.types.ClassOf(iter, nil, nil)) {
		a.errorf(n.X.Pos(), n.X.End(), "for-each not applicable to %s", src)
		return errType
	}
	// The element type is Iterable's type argument at this instantiation.
	// Without one — a raw Iterable — it is Object, which is what erasure
	// gives and what gen will checkcast from.
	if arg := a.typeArgAt(ct, iter, 0); arg != nil {
		return arg
	}
	return a.types.Object()
}

func (a *attributor) breakStmt(e *env, n *ast.BreakStmt) {
	if n.Label != nil {
		if name := identText(n.Label, e.file()); !e.hasLabel(name) {
			a.errorf(n.Label.Pos(), n.Label.End(), "undefined label: %s", name)
		}
		return
	}
	if !e.loop && !e.swtch {
		a.errorf(n.Pos(), n.End(), "break outside of a loop or switch")
	}
}

func (a *attributor) continueStmt(e *env, n *ast.ContinueStmt) {
	if n.Label != nil {
		if name := identText(n.Label, e.file()); !e.hasLabel(name) {
			a.errorf(n.Label.Pos(), n.Label.End(), "undefined label: %s", name)
		}
		return
	}
	if !e.loop {
		a.errorf(n.Pos(), n.End(), "continue outside of a loop")
	}
}

func (a *attributor) returnStmt(e *env, n *ast.ReturnStmt) {
	switch {
	case e.ret == nil:
		if n.Result != nil {
			a.expr(e, n.Result, nil)
			a.errorf(n.Pos(), n.End(), "return with a value from an initializer")
		}
	case e.ret.Kind() == types.KindVoid:
		if n.Result != nil {
			a.expr(e, n.Result, nil)
			a.errorf(n.Pos(), n.End(), "incompatible types: unexpected return value")
		}
	default:
		if n.Result == nil {
			a.errorf(n.Pos(), n.End(), "missing return value")
			return
		}
		a.expr(e, n.Result, e.ret)
	}
}

// tryStmt attributes resources, the body, each catch clause and the finally.
func (a *attributor) tryStmt(e *env, n *ast.TryStmt) {
	te := e.block(nil)
	for _, r := range n.Resources {
		a.resource(te, r)
	}
	a.block(te, n.Body)

	for _, c := range n.Catches {
		ce := e.block(nil)
		var caught []types.Type
		for _, xt := range c.Types {
			t := a.resolveType(ce, xt)
			a.info.Types[xt] = t
			if !a.isThrowable(t) && !types.IsError(t) {
				a.errorf(xt.Pos(), xt.End(), "%s is not a subclass of java.lang.Throwable", t)
			}
			caught = append(caught, t)
		}
		// A multi-catch parameter's type is the union's lub, which mocha does
		// not compute; the declared type is the first alternative for a single
		// catch and Throwable for a union, which is what erasure gives anyway.
		pt := errType
		switch {
		case len(caught) == 1:
			pt = caught[0]
		case len(caught) > 1:
			pt = a.types.Named(sym.ThrowableName)
		}
		if c.Name != nil {
			v := &sym.VarSym{
				Sym: sym.Sym{
					Name:  identText(c.Name, ce.file()),
					Kind:  sym.KindVar,
					Flags: sym.FlagFinal,
					Owner: ce.method,
					Pos:   c.Name.Pos(),
					End:   c.Name.End(),
					Unit:  ce.file(),
				},
				Var:    sym.VarExceptionParam,
				Method: ce.method,
			}
			ce.declare(v, pt)
		}
		a.block(ce, c.Body)
	}
	if n.Finally != nil {
		a.block(e, n.Finally)
	}
}

// resource declares a try-with-resources resource and checks that it closes.
func (a *attributor) resource(e *env, r *ast.Resource) {
	if r.X != nil {
		t := a.expr(e, r.X, nil)
		a.checkCloseable(r.X, t)
		return
	}
	if r.Decl == nil {
		return
	}
	a.localVars(e, r.Decl)
	for _, decl := range r.Decl.Names {
		if v, ok := a.info.Use(decl).(*sym.VarSym); ok {
			v.Var = sym.VarResource
			v.Flags |= sym.FlagFinal
			a.checkCloseable(decl, a.info.Type(decl))
		}
	}
}

func (a *attributor) checkCloseable(n ast.Node, t types.Type) {
	if types.IsError(t) {
		return
	}
	ac := a.syms.Class(sym.AutoCloseableName)
	if ac == nil {
		return // nothing on the path declares it; not this package's failure
	}
	if !a.types.IsSubtype(t, a.types.ClassOf(ac, nil, nil)) {
		a.errorf(n.Pos(), n.End(),
			"try-with-resources requires java.lang.AutoCloseable, not %s", t)
	}
}

// constructorCall is this(...) or super(...), §8.8.7.1.
func (a *attributor) constructorCall(e *env, n *ast.ConstructorCall) {
	if !e.ctor {
		a.errorf(n.Pos(), n.End(), "constructor call is not allowed here")
	}
	var target *sym.ClassSym
	switch n.Kind {
	case token.THIS:
		target = e.class
	case token.SUPER:
		if s, ok := a.types.Supertype(e.class).(*types.ClassType); ok {
			target = s.Sym
		}
	}
	if target == nil {
		for _, arg := range n.Args {
			a.expr(e, arg, nil)
		}
		return
	}
	args := a.argTypes(e, n.Args)
	ctors := target.Methods(sym.InitName)
	m := a.selectMethod(e, n, ctors, args, sym.InitName)
	if m != nil {
		a.info.Uses[n] = m
	}
}

func (a *attributor) enumConstants(e *env, c *sym.ClassSym, consts []*ast.EnumConstant) {
	for _, k := range consts {
		for _, arg := range k.Args {
			a.expr(e, arg, nil)
		}
		if len(k.Members) == 0 {
			continue
		}
		// A constant with a body declares an anonymous subclass of the enum
		// (§8.9.3). It is entered here for the same reason a local class is:
		// sym.Enter does not walk bodies.
		n := c.NextAnonymous()
		anon := &sym.ClassSym{
			Sym: sym.Sym{
				Name:  "",
				Kind:  sym.KindClass,
				Owner: c,
				Pos:   k.Pos(),
				End:   k.End(),
				Unit:  e.file(),
			},
			Binary:     sym.AnonymousBinary(c.Binary, n),
			Package:    c.Package,
			Outer:      c,
			Super:      c.Binary,
			SourceFile: c.SourceFile,
		}
		anon.Members = sym.NewScope(anon, nil)
		a.syms.Declare(anon)
		ae := a.classEnv(anon)
		a.members(ae, anon, k.Members)
	}
}