package attr

import (
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// Type inference, §18.
//
// The full algorithm builds a bound set from the invocation's constraint
// formulas, incorporates them to a fixpoint, and resolves the remaining
// variables. What is here is the tractable subset: unify each parameter
// against its argument, then substitute into the result. That answers the
// cases real code produces — Collections.emptyList(), a Stream.map(), a
// Builder chain — and falls back to the bound (Object, usually) where it
// cannot decide.
//
// The gap is deliberate and worth naming, because it is the one place mocha's
// front end is knowingly weaker than javac's rather than merely smaller: a
// nested generic invocation whose inner type argument is only determinable
// from the outer target type will infer the bound instead. That produces a
// checkcast lower would have emitted anyway, so it is a lost diagnostic
// rather than wrong code.

// inferResult computes a method's result type at the call site, substituting
// whatever the arguments determine.
func (a *attributor) inferResult(e *env, m *sym.MethodSym, mt *types.MethodType,
	args []types.Type, want types.Type) types.Type {

	if mt == nil {
		return errType
	}
	if len(mt.TypeParams) == 0 {
		return mt.Result
	}

	subst := map[*sym.TypeParamSym]types.Type{}
	for i, p := range mt.Params {
		if i >= len(args) || args[i] == nil {
			continue
		}
		unify(p, args[i], subst)
	}

	// A variable the arguments did not determine takes the target type when
	// the result is exactly that variable — which is how the empty-collection
	// idiom works — and its bound otherwise.
	if want != nil {
		if tv, ok := mt.Result.(*types.TypeVar); ok {
			if _, known := subst[tv.Sym]; !known {
				subst[tv.Sym] = want
			}
		}
	}
	for _, tv := range mt.TypeParams {
		if _, known := subst[tv.Sym]; !known {
			subst[tv.Sym] = a.boundOf(tv)
		}
	}
	return substituteVars(mt.Result, subst)
}

func (a *attributor) boundOf(tv *types.TypeVar) types.Type {
	if tv.Bound != nil {
		return tv.Bound
	}
	return a.types.Object()
}

// unify matches a parameter type against an argument type, recording what each
// type variable must be. It is structural and one-directional: the first
// binding wins, because a second one would need a lub to combine them.
func unify(param, arg types.Type, out map[*sym.TypeParamSym]types.Type) {
	if param == nil || arg == nil {
		return
	}
	switch p := param.(type) {
	case *types.TypeVar:
		if p.Sym == nil {
			return
		}
		if _, seen := out[p.Sym]; !seen {
			// A primitive argument binds the boxed form: a type variable can
			// never be int.
			out[p.Sym] = arg
		}

	case *types.ArrayType:
		if at, ok := arg.(*types.ArrayType); ok {
			unify(p.Elem, at.Elem, out)
		}

	case *types.ClassType:
		at, ok := arg.(*types.ClassType)
		if !ok || at.Sym != p.Sym {
			return
		}
		for i := range p.Args {
			if i < len(at.Args) {
				unify(p.Args[i], at.Args[i], out)
			}
		}

	case *types.Wildcard:
		if p.Bound != nil {
			unify(p.Bound, arg, out)
		}
	}
}

// substituteVars replaces type variables throughout a type.
func substituteVars(t types.Type, subst map[*sym.TypeParamSym]types.Type) types.Type {
	if t == nil || len(subst) == 0 {
		return t
	}
	switch n := t.(type) {
	case *types.TypeVar:
		if n.Sym != nil {
			if r, ok := subst[n.Sym]; ok {
				return r
			}
		}
		return n

	case *types.ArrayType:
		e := substituteVars(n.Elem, subst)
		if e == n.Elem {
			return n
		}
		return &types.ArrayType{Elem: e}

	case *types.ClassType:
		if len(n.Args) == 0 {
			return n
		}
		args := make([]types.Type, len(n.Args))
		changed := false
		for i, a := range n.Args {
			args[i] = substituteVars(a, subst)
			if args[i] != a {
				changed = true
			}
		}
		if !changed {
			return n
		}
		return &types.ClassType{Sym: n.Sym, Args: args, Outer: n.Outer}

	case *types.Wildcard:
		if n.Bound == nil {
			return n
		}
		b := substituteVars(n.Bound, subst)
		if b == n.Bound {
			return n
		}
		return &types.Wildcard{Wild: n.Wild, Bound: b}
	}
	return t
}

// substitute reinterprets a member's type at a receiver's instantiation.
//
// A field of List<E> read through a List<String> has type String, and the
// member's own declaration says E. This is what turns the declaration into
// the use.
func (a *attributor) substitute(recv types.Type, member types.Type) types.Type {
	ct, ok := recv.(*types.ClassType)
	if !ok || len(ct.Args) == 0 || ct.Sym == nil {
		return member
	}
	params := a.types.TypeParams(ct.Sym)
	if len(params) != len(ct.Args) {
		return member
	}
	subst := make(map[*sym.TypeParamSym]types.Type, len(params))
	for i, p := range params {
		if p.Sym != nil {
			subst[p.Sym] = ct.Args[i]
		}
	}
	return substituteVars(member, subst)
}

// typeArgAt finds the argument a receiver supplies for one of a supertype's
// parameters — what Iterable<T>'s T is, given an ArrayList<String>.
func (a *attributor) typeArgAt(recv *types.ClassType, target *sym.ClassSym, i int) types.Type {
	if recv.Sym == target {
		if i < len(recv.Args) {
			return recv.Args[i]
		}
		return nil
	}
	for _, sup := range a.types.Supers(recv.Sym) {
		if sup.Sym != target {
			continue
		}
		got := a.substitute(recv, sup)
		if gc, ok := got.(*types.ClassType); ok && i < len(gc.Args) {
			return gc.Args[i]
		}
	}
	return nil
}