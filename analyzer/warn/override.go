package warn

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// Override checking, §8.4.8.
//
// Two methods override each other exactly when the JVM would consider one to
// replace the other, so the matching key is the erased descriptor — the same
// key attr.checkOverloads uses for the clash it reports. Anything subtler
// (generic bridge methods) is lower's to emit, not this phase's to model.

// overrideChecks runs the four override rules against one method.
func (c *checker) overrideChecks(cs *sym.ClassSym, d *ast.MethodDecl, m *sym.MethodSym) {
	if m == nil || m.IsConstructor() {
		return
	}
	super := c.findOverridden(cs, m)

	if c.hasOverrideAnnotation(d) && super == nil {
		// §9.6.4.4: @Override on a method that overrides nothing is an error,
		// not a warning. It is the one annotation with compile-time meaning.
		c.errorf(d.Pos(), d.End(),
			"method does not override or implement a method from a supertype")
		return
	}
	if super == nil {
		return
	}

	// §8.4.3.3: a final method cannot be overridden.
	if super.Flags.Has(sym.FlagFinal) {
		c.errorf(d.Pos(), d.End(),
			"%s cannot override a final method in %s",
			m.Name, sym.Dotted(super.Class.Binary))
	}

	// §8.4.8.3: an override may widen access but never narrow it.
	if accessRank(m.Flags) < accessRank(super.Flags) {
		c.errorf(d.Pos(), d.End(),
			"%s cannot reduce the visibility of the method it overrides in %s",
			m.Name, sym.Dotted(super.Class.Binary))
	}

	// A static method cannot hide an instance method and vice versa.
	if m.Flags.Has(sym.FlagStatic) != super.Flags.Has(sym.FlagStatic) {
		c.errorf(d.Pos(), d.End(),
			"%s conflicts with the method it overrides in %s: static mismatch",
			m.Name, sym.Dotted(super.Class.Binary))
	}

	c.checkReturnCovariance(d, m, super)
	c.checkThrowsNarrowing(d, m, super)

	// A method overriding a deprecated one is not itself a use of it, so the
	// deprecation warning belongs here rather than in deprecate.go.
	if super.Flags.Has(sym.FlagDeprecated) && !m.Flags.Has(sym.FlagDeprecated) {
		c.warnf("deprecation", d.Pos(), d.End(),
			"%s overrides a deprecated method in %s",
			m.Name, sym.Dotted(super.Class.Binary))
	}
}

// checkReturnCovariance implements §8.4.8.3: a reference return type may
// narrow, a primitive one must match exactly.
func (c *checker) checkReturnCovariance(d *ast.MethodDecl, m, super *sym.MethodSym) {
	mt, st := c.types.MethodType(m), c.types.MethodType(super)
	if mt == nil || st == nil {
		return
	}
	if types.Identical(mt.Result, st.Result) {
		return
	}
	if types.IsError(mt.Result) || types.IsError(st.Result) {
		return
	}
	if mt.Result.Kind().IsPrimitive() || st.Result.Kind().IsPrimitive() {
		c.errorf(d.Pos(), d.End(),
			"return type %s is incompatible with %s in the overridden method",
			mt.Result, st.Result)
		return
	}
	if !c.types.IsSubtype(mt.Result, st.Result) {
		c.errorf(d.Pos(), d.End(),
			"return type %s is incompatible with %s in the overridden method",
			mt.Result, st.Result)
	}
}

// checkThrowsNarrowing implements §8.4.8.3: an override may not throw a
// checked exception the overridden method does not permit.
func (c *checker) checkThrowsNarrowing(d *ast.MethodDecl, m, super *sym.MethodSym) {
	mt, st := c.types.MethodType(m), c.types.MethodType(super)
	if mt == nil || st == nil {
		return
	}
	for _, x := range mt.Throws {
		if !c.isCheckedException(x) {
			continue
		}
		covered := false
		for _, allowed := range st.Throws {
			if c.types.IsSubtype(x, allowed) {
				covered = true
				break
			}
		}
		if !covered {
			c.errorf(d.Pos(), d.End(),
				"overridden method does not throw %s", x)
		}
	}
}

// findOverridden returns the supertype method this one overrides, or nil.
func (c *checker) findOverridden(cs *sym.ClassSym, m *sym.MethodSym) *sym.MethodSym {
	key := c.signatureKey(m)
	for _, sup := range c.types.Supers(cs) {
		if sup.Sym == nil {
			continue
		}
		for _, cand := range sup.Sym.Methods(m.Name) {
			if cand.Flags.Has(sym.FlagPrivate) {
				continue // a private method is never overridden
			}
			if c.signatureKey(cand) == key {
				return cand
			}
		}
	}
	return nil
}

// abstractCompleteness reports a concrete class that leaves an abstract
// method unimplemented (§8.1.1.1).
func (c *checker) abstractCompleteness(cs *sym.ClassSym) {
	if cs.Flags.HasAny(sym.FlagAbstract|sym.FlagInterface) {
		return
	}
	implemented := map[string]bool{}
	c.collectConcrete(cs, implemented)
	for _, sup := range c.types.Supers(cs) {
		if sup.Sym != nil {
			c.collectConcrete(sup.Sym, implemented)
		}
	}

	seen := map[string]bool{}
	for _, sup := range c.types.Supers(cs) {
		if sup.Sym == nil {
			continue
		}
		sup.Sym.Members.Each(func(s sym.Symbol) bool {
			m, ok := s.(*sym.MethodSym)
			if !ok || !m.Flags.Has(sym.FlagAbstract) {
				return true
			}
			key := m.Name + c.signatureKey(m)
			if seen[key] || implemented[key] {
				return true
			}
			seen[key] = true
			c.errorf(cs.Pos, cs.End,
				"%s is not abstract and does not override abstract method %s in %s",
				sym.Dotted(cs.Binary), m.Name, sym.Dotted(sup.Sym.Binary))
			return true
		})
	}
}

func (c *checker) collectConcrete(cs *sym.ClassSym, out map[string]bool) {
	cs.Members.Each(func(s sym.Symbol) bool {
		m, ok := s.(*sym.MethodSym)
		if !ok || m.Flags.Has(sym.FlagAbstract) {
			return true
		}
		out[m.Name+c.signatureKey(m)] = true
		return true
	})
}

// signatureKey is the erased descriptor, which is what the JVM matches on.
func (c *checker) signatureKey(m *sym.MethodSym) string {
	return types.MethodDescriptor(c.types.MethodType(m))
}

// accessRank orders the four access levels so narrowing is a comparison.
// Package access has no keyword, which is why absent sits between private
// and protected rather than at either end.
func accessRank(f sym.Flags) int {
	switch {
	case f.Has(sym.FlagPublic):
		return 3
	case f.Has(sym.FlagProtected):
		return 2
	case f.Has(sym.FlagPrivate):
		return 0
	}
	return 1 // package
}

func (c *checker) hasOverrideAnnotation(d *ast.MethodDecl) bool {
	if d.Mods == nil {
		return false
	}
	for _, x := range d.Mods.List {
		a := x.Annotation
		if a == nil || a.Name == nil || len(a.Name.Parts) == 0 {
			continue
		}
		if a.Name.Parts[len(a.Name.Parts)-1].Name(c.file()) == "Override" {
			return true
		}
	}
	return false
}