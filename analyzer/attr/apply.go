package attr

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// Method invocation, §15.12.
//
// Resolution runs in three phases, each considering every candidate before the
// next runs (§15.12.2.2 through §15.12.2.4):
//
//  1. strict — subtyping only, no boxing, no varargs
//  2. loose  — boxing and unboxing permitted, still no varargs
//  3. varargs
//
// The phases exist so that an overload taking int is preferred over one taking
// Integer, and both over one taking int..., regardless of declaration order.
// Collapsing them into one pass with a cost function gets the common cases
// right and the interesting ones wrong.

type phase uint8

const (
	strict phase = iota
	loose
	varargs
)

// call attributes a method invocation.
func (a *attributor) call(e *env, n *ast.CallExpr, want types.Type) types.Type {
	name := identText(n.Fun, e.file())
	args := a.argTypes(e, n.Args)

	var candidates []*sym.MethodSym
	var recv types.Type

	switch {
	case n.X == nil:
		// An unqualified call: this class, enclosing classes, static imports.
		candidates, _ = a.resolveMethodName(e, name)
		if e.class != nil {
			recv = a.types.ClassOf(e.class, nil, nil)
		}

	case isSuper(n.X):
		recv = a.superType(e, n.X.(*ast.Super))
		if ct, ok := recv.(*types.ClassType); ok {
			candidates = a.findMethods(ct.Sym, name)
		}

	default:
		recv = a.expr(e, n.X, nil)
		if types.IsError(recv) {
			return errType
		}
		candidates = a.methodsOn(recv, name)
	}

	if len(candidates) == 0 {
		a.errorf(n.Fun.Pos(), n.Fun.End(), "cannot find symbol: method %s", name)
		return errType
	}

	m := a.selectMethod(e, n, candidates, args, name)
	if m == nil {
		return errType
	}
	a.info.Uses[n] = m

	if e.static && !m.Flags.Has(sym.FlagStatic) && n.X == nil {
		a.errorf(n.Pos(), n.End(),
			"non-static method %s cannot be referenced from a static context", name)
	}

	mt := a.types.MethodType(m)
	// Re-attribute the arguments against the chosen parameter types, so a
	// lambda or a diamond that had no standalone type gets one now.
	a.recheckArgs(e, n.Args, mt)

	return a.substitute(recv, a.inferResult(e, m, mt, args, want))
}

// argTypes attributes each argument without a target. A poly expression has no
// standalone type, so it yields an error type here and is re-attributed once
// the overload is known.
func (a *attributor) argTypes(e *env, args []ast.Expr) []types.Type {
	out := make([]types.Type, 0, len(args))
	for _, arg := range args {
		if isPoly(arg) {
			out = append(out, nil) // no standalone type; applicability skips it
			continue
		}
		out = append(out, a.expr(e, arg, nil))
	}
	return out
}

func (a *attributor) recheckArgs(e *env, args []ast.Expr, mt *types.MethodType) {
	if mt == nil {
		return
	}
	for i, arg := range args {
		if !isPoly(arg) {
			continue
		}
		var want types.Type
		if i < len(mt.Params) {
			want = mt.Params[i]
		} else if n := len(mt.Params); n > 0 {
			if at, ok := mt.Params[n-1].(*types.ArrayType); ok {
				want = at.Elem
			}
		}
		a.expr(e, arg, want)
	}
}

// selectMethod runs the three phases and then picks the most specific.
func (a *attributor) selectMethod(e *env, n ast.Node, candidates []*sym.MethodSym,
	args []types.Type, name string) *sym.MethodSym {

	for _, ph := range []phase{strict, loose, varargs} {
		var applicable []*sym.MethodSym
		for _, m := range candidates {
			if a.applicable(m, args, ph) {
				applicable = append(applicable, m)
			}
		}
		switch len(applicable) {
		case 0:
			continue
		case 1:
			return applicable[0]
		}
		if best := a.mostSpecific(applicable, ph); best != nil {
			return best
		}
		a.errorf(n.Pos(), n.End(), "reference to %s is ambiguous", name)
		return applicable[0]
	}

	a.errorf(n.Pos(), n.End(),
		"no suitable method found for %s(%s)", name, typeList(args))
	return nil
}

// applicable reports whether one method accepts the argument types in a phase.
func (a *attributor) applicable(m *sym.MethodSym, args []types.Type, ph phase) bool {
	mt := a.types.MethodType(m)
	if mt == nil {
		return false
	}
	params := mt.Params

	if ph == varargs {
		if !m.Flags.Has(sym.FlagVarargs) || len(params) == 0 {
			return false
		}
		fixed := len(params) - 1
		if len(args) < fixed {
			return false
		}
		for i := 0; i < fixed; i++ {
			if !a.argFits(args[i], params[i], loose) {
				return false
			}
		}
		rest, ok := params[fixed].(*types.ArrayType)
		if !ok {
			return false
		}
		// One argument of the array type itself is also applicable: passing
		// an int[] to int... is legal and does not wrap.
		if len(args) == len(params) && a.argFits(args[fixed], params[fixed], loose) {
			return true
		}
		for i := fixed; i < len(args); i++ {
			if !a.argFits(args[i], rest.Elem, loose) {
				return false
			}
		}
		return true
	}

	if len(args) != len(params) {
		return false
	}
	// A varargs method is not considered in the first two phases in its
	// expanded form, but it is in its fixed-arity form.
	for i := range args {
		if !a.argFits(args[i], params[i], ph) {
			return false
		}
	}
	return true
}

// argFits is one argument against one parameter in a phase. A nil argument
// type is a poly expression, which is presumed to fit: its type depends on the
// parameter, so it constrains nothing here.
func (a *attributor) argFits(arg, param types.Type, ph phase) bool {
	if arg == nil || types.IsError(arg) || types.IsError(param) {
		return true
	}
	if types.Identical(arg, param) {
		return true
	}
	if ph == strict {
		// Subtyping and widening primitive conversion only (§15.12.2.2).
		if arg.Kind().IsPrimitive() && param.Kind().IsPrimitive() {
			return types.Widens(arg, param)
		}
		if types.IsReference(arg) && types.IsReference(param) {
			return a.types.IsSubtype(arg, param)
		}
		return false
	}
	return a.assignableTo(arg, param)
}

// mostSpecific implements §15.12.2.5: m1 is more specific than m2 when every
// parameter of m1 is a subtype of the matching one of m2.
func (a *attributor) mostSpecific(ms []*sym.MethodSym, ph phase) *sym.MethodSym {
	best := ms[0]
	for _, m := range ms[1:] {
		if a.moreSpecific(m, best, ph) {
			best = m
		}
	}
	// The winner has to beat everything, not just what it was compared with:
	// specificity is not a total order, and a three-way tie has no answer.
	for _, m := range ms {
		if m != best && !a.moreSpecific(best, m, ph) {
			return nil
		}
	}
	return best
}

func (a *attributor) moreSpecific(m1, m2 *sym.MethodSym, ph phase) bool {
	t1, t2 := a.types.MethodType(m1), a.types.MethodType(m2)
	if t1 == nil || t2 == nil {
		return false
	}
	p1, p2 := t1.Params, t2.Params
	if ph == varargs {
		return len(p1) >= len(p2)
	}
	if len(p1) != len(p2) {
		return false
	}
	for i := range p1 {
		if !a.convertibleFor(p1[i], p2[i]) {
			return false
		}
	}
	return true
}

func (a *attributor) convertibleFor(from, to types.Type) bool {
	if types.Identical(from, to) {
		return true
	}
	if from.Kind().IsPrimitive() && to.Kind().IsPrimitive() {
		return types.Widens(from, to)
	}
	return a.types.IsSubtype(from, to)
}

// methodsOn collects the methods a receiver type offers, following a type
// variable to its bound and treating an array as Object's members plus clone.
func (a *attributor) methodsOn(recv types.Type, name string) []*sym.MethodSym {
	switch t := recv.(type) {
	case *types.ClassType:
		return a.findMethods(t.Sym, name)
	case *types.TypeVar:
		if t.Bound != nil {
			return a.methodsOn(t.Bound, name)
		}
	case *types.Intersection:
		var out []*sym.MethodSym
		for _, b := range t.Bounds {
			out = append(out, a.methodsOn(b, name)...)
		}
		return out
	case *types.ArrayType:
		if obj := a.syms.Object(); obj != nil {
			return a.findMethods(obj, name)
		}
	}
	return nil
}

// newExpr attributes a class instance creation. A body makes it an anonymous
// class, which the grammar does not call a declaration but which is one.
func (a *attributor) newExpr(e *env, n *ast.NewExpr, want types.Type) types.Type {
	if n.Outer != nil {
		a.expr(e, n.Outer, nil)
	}
	t := a.resolveType(e, n.Type)
	a.info.Types[n.Type] = t

	ct, ok := t.(*types.ClassType)
	if !ok {
		for _, arg := range n.Args {
			a.expr(e, arg, nil)
		}
		return t
	}

	// A diamond takes its arguments from the target type. Full inference
	// would solve them from the constructor's own arguments too; taking the
	// target is right whenever there is one and raw otherwise.
	if n.Type.TypeArgs != nil && n.Type.TypeArgs.Diamond {
		if wt, ok := want.(*types.ClassType); ok && wt.Sym == ct.Sym {
			ct = &types.ClassType{Sym: ct.Sym, Args: wt.Args}
			t = ct
		}
	}

	args := a.argTypes(e, n.Args)
	ctors := ct.Sym.Methods(sym.InitName)
	if len(ctors) == 0 {
		// A class with no declared constructor has the default one (§8.8.9),
		// which sym does not synthesize because it is lower's to emit.
		if len(args) > 0 {
			a.errorf(n.Pos(), n.End(), "no suitable constructor found for %s", t)
		}
	} else if m := a.selectMethod(e, n, ctors, args, sym.SimpleName(ct.Sym.Binary)); m != nil {
		a.info.Uses[n] = m
		a.recheckArgs(e, n.Args, a.types.MethodType(m))
	}

	if n.Body != nil {
		return a.anonymousClass(e, n, ct)
	}
	if ct.Sym.Flags.Has(sym.FlagAbstract) {
		a.errorf(n.Pos(), n.End(), "%s is abstract and cannot be instantiated", t)
	}
	return t
}

// anonymousClass enters the subclass an anonymous body declares. §13.1 numbers
// them from one per innermost enclosing class, which is what
// ClassSym.NextAnonymous exists for.
func (a *attributor) anonymousClass(e *env, n *ast.NewExpr, base *types.ClassType) types.Type {
	if e.class == nil {
		return base
	}
	num := e.class.NextAnonymous()
	anon := &sym.ClassSym{
		Sym: sym.Sym{
			Kind:  sym.KindClass,
			Owner: e.class,
			Pos:   n.Pos(),
			End:   n.End(),
			Unit:  e.file(),
		},
		Binary:     sym.AnonymousBinary(e.class.Binary, num),
		Package:    e.class.Package,
		Outer:      e.class,
		SourceFile: e.class.SourceFile,
	}
	// An anonymous class implements the named type when it is an interface
	// and extends it otherwise. There is no syntax that distinguishes the two.
	if base.Sym != nil && base.Sym.IsInterface() {
		anon.Interfaces = []string{base.Sym.Binary}
		anon.Super = sym.ObjectName
	} else if base.Sym != nil {
		anon.Super = base.Sym.Binary
	}
	anon.Members = sym.NewScope(anon, nil)
	a.syms.Declare(anon)

	ae := a.classEnv(anon)
	ae.static = e.static
	a.members(ae, anon, n.Body)

	return a.types.ClassOf(anon, nil, nil)
}