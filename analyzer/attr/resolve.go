package attr

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// Name resolution, §6.5.
//
// The JLS classifies a name by the syntactic context it appears in before it
// resolves anything: an ExpressionName, a TypeName, a MethodName, or an
// AmbiguousName that reclassification turns into one of the others. The
// parser deliberately keeps a dotted name as an *ast.Name for exactly this
// reason — collapsing it into selector chains would discard which parts were
// written together.
//
// Each step below is a separate lookup with a separate failure, so a "cannot
// find symbol" can name the step that ran out rather than reporting the whole
// search.

// resolveExprName resolves a simple ExpressionName (§6.5.6.1).
//
// Order: a variable in scope, then a field of this class or a supertype, then
// a field of an enclosing class, then a single-static import, then a
// static-on-demand import.
func (a *attributor) resolveExprName(e *env, name string, n ast.Node) (sym.Symbol, types.Type) {
	// 1. Local, parameter, exception parameter, resource or pattern binding.
	if v := e.lookupVar(name); v != nil {
		return v, a.varType(v)
	}

	// 2. A field of this class or anything above it. sym.ClassSym.Lookup does
	//    not search supertypes on purpose — which member is visible from where
	//    is this package's rule — so the walk is here.
	for c := e.class; c != nil; c = c.Outer {
		if v, owner := a.findField(c, name); v != nil {
			if e.static && !v.Flags.Has(sym.FlagStatic) && c == e.class {
				a.errorf(n.Pos(), n.End(),
					"non-static variable %s cannot be referenced from a static context", name)
			}
			_ = owner
			return v, a.varType(v)
		}
	}

	// 3. Static imports. sym.Unit.FindStatic returns candidate owners in
	//    import order and stops there, because which one declares the member
	//    is a lookup question.
	for _, owner := range e.unit.FindStatic(name) {
		if v, _ := a.findField(owner, name); v != nil && v.Flags.Has(sym.FlagStatic) {
			return v, a.varType(v)
		}
	}

	a.errorf(n.Pos(), n.End(), "cannot find symbol: variable %s", name)
	return nil, errType
}

// findField searches a class and its supertypes for a field, breadth first.
// The first match wins, which is what §8.3 hiding produces.
func (a *attributor) findField(c *sym.ClassSym, name string) (*sym.VarSym, *sym.ClassSym) {
	if c == nil {
		return nil, nil
	}
	if v := c.Field(name); v != nil {
		return v, c
	}
	for _, sup := range a.types.Supers(c) {
		if sup.Sym == nil {
			continue
		}
		if v := sup.Sym.Field(name); v != nil {
			return v, sup.Sym
		}
	}
	return nil, nil
}

// findMethods collects every method with a name from a class and its
// supertypes. Overload resolution needs the whole set, not the first hit:
// §15.12.2.1 considers all of them and then picks.
func (a *attributor) findMethods(c *sym.ClassSym, name string) []*sym.MethodSym {
	if c == nil {
		return nil
	}
	var out []*sym.MethodSym
	seen := map[string]bool{}

	add := func(from *sym.ClassSym) {
		for _, m := range from.Methods(name) {
			// An override hides the inherited one. Keying on the erased
			// descriptor is the same test checkOverloads uses.
			key := types.MethodDescriptor(a.types.MethodType(m))
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, m)
		}
	}
	add(c)
	for _, sup := range a.types.Supers(c) {
		if sup.Sym != nil {
			add(sup.Sym)
		}
	}
	return out
}

// resolveMethodName finds the candidate methods for an unqualified call
// (§6.5.7.1): this class and its supertypes, then enclosing classes, then
// static imports.
func (a *attributor) resolveMethodName(e *env, name string) ([]*sym.MethodSym, *sym.ClassSym) {
	for c := e.class; c != nil; c = c.Outer {
		if ms := a.findMethods(c, name); len(ms) > 0 {
			return ms, c
		}
	}
	for _, owner := range e.unit.FindStatic(name) {
		if ms := a.findMethods(owner, name); len(ms) > 0 {
			var static []*sym.MethodSym
			for _, m := range ms {
				if m.Flags.Has(sym.FlagStatic) {
					static = append(static, m)
				}
			}
			if len(static) > 0 {
				return static, owner
			}
		}
	}
	return nil, nil
}

// resolveTypeName resolves a simple TypeName. Type parameters shadow member
// types, which shadow whatever §6.5.5 finds through the unit.
func (a *attributor) resolveTypeName(e *env, name string, n ast.Node) types.Type {
	for _, tv := range e.tparams {
		if tv.Sym != nil && tv.Sym.Name == name {
			return tv
		}
	}
	for c := e.class; c != nil; c = c.Outer {
		if nested := c.Nested(name); nested != nil {
			a.info.Uses[n] = nested
			return a.types.ClassOf(nested, nil, nil)
		}
	}
	if c := e.unit.FindType(name); c != nil {
		a.info.Uses[n] = c
		return a.types.ClassOf(c, nil, nil)
	}
	// FindType returns nil both for "no such type" and for an on-demand
	// import ambiguity, which it documents as this package's to report. The
	// two are distinguishable by asking again.
	if a.ambiguousOnDemand(e, name) {
		a.errorf(n.Pos(), n.End(), "reference to %s is ambiguous", name)
	} else {
		a.errorf(n.Pos(), n.End(), "cannot find symbol: class %s", name)
	}
	return errType
}

// ambiguousOnDemand reports whether two on-demand imports both supply a name,
// which is the case sym.Unit.FindType folds into a nil return.
func (a *attributor) ambiguousOnDemand(e *env, name string) bool {
	var found string
	n := 0
	for _, pkg := range onDemandPackages(e.unit) {
		if c := a.syms.Class(sym.TopLevelBinary(pkg, name)); c != nil {
			if found != "" && found != c.Binary {
				n++
			}
			found = c.Binary
		}
	}
	return n > 0
}

// resolveName reclassifies a dotted name (§6.5.2) and attributes it.
//
// `a.b.c` may be a package qualifying a type, a type qualifying a static
// field, or a variable followed by two field accesses — and only a lookup can
// say which. The rule is longest-prefix-first from the left: the first
// component that resolves as a variable or a type ends the ambiguous part.
func (a *attributor) resolveName(e *env, n *ast.Name) (sym.Symbol, types.Type) {
	f := e.file()
	if n == nil || len(n.Parts) == 0 {
		return nil, errType
	}
	head := n.Parts[0].Name(f)

	// A variable in scope, or a field, ends reclassification immediately: the
	// rest are field accesses on it.
	if v := e.lookupVar(head); v != nil {
		return a.selectChain(e, v, a.varType(v), n, 1)
	}
	for c := e.class; c != nil; c = c.Outer {
		if v, _ := a.findField(c, head); v != nil {
			return a.selectChain(e, v, a.varType(v), n, 1)
		}
	}

	// Otherwise the longest prefix that names a type wins, and everything
	// after it is a member access. Trying from the left rather than the right
	// is what §6.5.2 specifies, and it differs from sym.resolveImport, which
	// tries from the right because an import names one thing.
	for i := 1; i <= len(n.Parts); i++ {
		simple := n.Parts[i-1].Name(f)
		var t types.Type
		if i == 1 {
			t = a.tryTypeName(e, simple)
		} else {
			t = a.tryQualifiedType(e, n, i, f)
		}
		if t != nil {
			ct, ok := t.(*types.ClassType)
			if !ok {
				return nil, t
			}
			a.info.Uses[n.Parts[i-1]] = ct.Sym
			if i == len(n.Parts) {
				return ct.Sym, t
			}
			return a.staticChain(e, ct, n, i)
		}
	}

	a.errorf(n.Pos(), n.End(), "cannot find symbol: %s", sym.NameString(n, f))
	return nil, errType
}

// tryTypeName is resolveTypeName without the diagnostic, for reclassification,
// which must be free to fail and try a longer prefix.
func (a *attributor) tryTypeName(e *env, name string) types.Type {
	for _, tv := range e.tparams {
		if tv.Sym != nil && tv.Sym.Name == name {
			return tv
		}
	}
	for c := e.class; c != nil; c = c.Outer {
		if nested := c.Nested(name); nested != nil {
			return a.types.ClassOf(nested, nil, nil)
		}
	}
	if c := e.unit.FindType(name); c != nil {
		return a.types.ClassOf(c, nil, nil)
	}
	return nil
}

// tryQualifiedType asks whether the first i components name a type: either a
// package plus a top-level type, or a chain ending in a nested one.
func (a *attributor) tryQualifiedType(e *env, n *ast.Name, i int, f fileLike) types.Type {
	pkg := joinParts(n, 0, i-1, "/", f)
	simple := n.Parts[i-1].Name(f)
	if c := a.syms.Class(sym.TopLevelBinary(pkg, simple)); c != nil {
		return a.types.ClassOf(c, nil, nil)
	}
	// The prefix may itself be a nested type: a/b/C$D.
	if c := a.syms.Class(joinParts(n, 0, i, "$", f)); c != nil {
		return a.types.ClassOf(c, nil, nil)
	}
	return nil
}

// selectChain walks the remaining components as instance field accesses.
func (a *attributor) selectChain(e *env, s sym.Symbol, t types.Type, n *ast.Name, from int) (sym.Symbol, types.Type) {
	f := e.file()
	a.info.Uses[n.Parts[from-1]] = s
	for i := from; i < len(n.Parts); i++ {
		name := n.Parts[i].Name(f)
		v, _ := a.fieldOf(t, name)
		if v == nil {
			a.errorf(n.Parts[i].Pos(), n.Parts[i].End(),
				"cannot find symbol: variable %s", name)
			return nil, errType
		}
		a.info.Uses[n.Parts[i]] = v
		s, t = v, a.varType(v)
	}
	return s, t
}

// staticChain walks the components after a type prefix: the first is a static
// member, the rest are instance fields on it.
func (a *attributor) staticChain(e *env, ct *types.ClassType, n *ast.Name, from int) (sym.Symbol, types.Type) {
	f := e.file()
	name := n.Parts[from].Name(f)

	if nested := ct.Sym.Nested(name); nested != nil {
		t := a.types.ClassOf(nested, nil, nil)
		a.info.Uses[n.Parts[from]] = nested
		if from+1 == len(n.Parts)-0 && from+1 >= len(n.Parts) {
			return nested, t
		}
		if nt, ok := t.(*types.ClassType); ok && from+1 < len(n.Parts) {
			return a.staticChain(e, nt, n, from+1)
		}
		return nested, t
	}

	v, _ := a.findField(ct.Sym, name)
	if v == nil {
		a.errorf(n.Parts[from].Pos(), n.Parts[from].End(),
			"cannot find symbol: variable %s in %s", name, sym.Dotted(ct.Sym.Binary))
		return nil, errType
	}
	if !v.Flags.Has(sym.FlagStatic) {
		a.errorf(n.Parts[from].Pos(), n.Parts[from].End(),
			"non-static variable %s cannot be referenced from a static context", name)
	}
	return a.selectChain(e, v, a.varType(v), n, from+1)
}

// fieldOf finds a field on a receiver type, following the type variable's
// bound and treating an array's length specially (§10.7).
func (a *attributor) fieldOf(recv types.Type, name string) (*sym.VarSym, *sym.ClassSym) {
	switch t := recv.(type) {
	case *types.ClassType:
		return a.findField(t.Sym, name)
	case *types.TypeVar:
		if t.Bound != nil {
			return a.fieldOf(t.Bound, name)
		}
	case *types.Intersection:
		for _, b := range t.Bounds {
			if v, c := a.fieldOf(b, name); v != nil {
				return v, c
			}
		}
	}
	return nil, nil
}

// varType is the declared type of a variable, resolved through types.
func (a *attributor) varType(v *sym.VarSym) types.Type {
	if v == nil {
		return errType
	}
	if t, ok := a.info.Types[declNode(v)]; ok && t != nil {
		return t
	}
	return a.types.FieldType(v)
}