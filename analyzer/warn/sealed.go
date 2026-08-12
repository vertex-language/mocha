package warn

import (
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// Sealed hierarchies, §8.1.1.2 and §9.1.1.4.
//
// types resolves ClassSym.Permits and stops there. The conformance is mutual
// and neither half is checkable without the supertype graph: every permitted
// subclass must actually extend the sealed type, and every direct subclass
// must appear in the permits clause.

func (c *checker) sealedClass(cs *sym.ClassSym) {
	if cs.Flags.Has(sym.FlagSealed) {
		c.checkPermits(cs)
	}
	c.checkSealedParent(cs)
}

// checkPermits verifies each named subclass extends this type and lives where
// a sealed hierarchy requires.
func (c *checker) checkPermits(cs *sym.ClassSym) {
	if len(cs.Permits) == 0 {
		// A sealed type with no permits clause is legal only when its
		// subclasses are declared in the same file — the implicit form.
		if !c.hasLocalSubclass(cs) {
			c.errorf(cs.Pos, cs.End,
				"sealed %s has no permitted subclasses",
				sym.Dotted(cs.Binary))
		}
		return
	}

	self := c.types.ClassOf(cs, nil, nil)
	for _, name := range cs.Permits {
		sub := c.syms.Class(name)
		if sub == nil {
			c.errorf(cs.Pos, cs.End,
				"cannot find permitted subclass %s", sym.Dotted(name))
			continue
		}
		subType := c.types.ClassOf(sub, nil, nil)
		if !c.extendsDirectly(sub, cs) {
			c.errorf(cs.Pos, cs.End,
				"%s is not a direct subclass of %s",
				sym.Dotted(name), sym.Dotted(cs.Binary))
			continue
		}
		// §8.1.1.2: a permitted subclass must itself be final, sealed or
		// non-sealed. Leaving it open would defeat the seal.
		if !sub.Flags.HasAny(sym.FlagFinal | sym.FlagSealed | sym.FlagNonSealed) {
			c.errorf(sub.Pos, sub.End,
				"%s must be final, sealed or non-sealed to extend sealed %s",
				sym.Dotted(name), sym.Dotted(cs.Binary))
		}
		// A permitted subclass must be in the same package (or module, which
		// mocha does not model).
		if sym.PackageOf(name) != sym.PackageOf(cs.Binary) {
			c.errorf(cs.Pos, cs.End,
				"%s must be in the same package as sealed %s",
				sym.Dotted(name), sym.Dotted(cs.Binary))
		}
		_ = subType
		_ = self
	}
}

// checkSealedParent verifies this class may extend what it extends.
func (c *checker) checkSealedParent(cs *sym.ClassSym) {
	supers := []string{}
	if cs.Super != "" {
		supers = append(supers, cs.Super)
	}
	supers = append(supers, cs.Interfaces...)

	for _, name := range supers {
		parent := c.syms.Class(name)
		if parent == nil || !parent.Flags.Has(sym.FlagSealed) {
			continue
		}
		if len(parent.Permits) == 0 {
			// Implicit permits: same file. sym does not record which file a
			// binary class came from, so this only checks source siblings.
			if cs.SourceFile != parent.SourceFile {
				c.errorf(cs.Pos, cs.End,
					"%s is not permitted to extend sealed %s",
					sym.Dotted(cs.Binary), sym.Dotted(name))
			}
			continue
		}
		if !contains(parent.Permits, cs.Binary) {
			c.errorf(cs.Pos, cs.End,
				"%s is not listed in the permits clause of %s",
				sym.Dotted(cs.Binary), sym.Dotted(name))
		}
		// A subclass of a sealed type must declare its own disposition.
		if !cs.Flags.HasAny(sym.FlagFinal | sym.FlagSealed | sym.FlagNonSealed) {
			c.errorf(cs.Pos, cs.End,
				"%s must be final, sealed or non-sealed", sym.Dotted(cs.Binary))
		}
	}
}

func (c *checker) extendsDirectly(sub, parent *sym.ClassSym) bool {
	if sub.Super == parent.Binary {
		return true
	}
	return contains(sub.Interfaces, parent.Binary)
}

func (c *checker) hasSealedSupertype(cs *sym.ClassSym) bool {
	if cs.Super != "" {
		if p := c.syms.Class(cs.Super); p != nil && p.Flags.Has(sym.FlagSealed) {
			return true
		}
	}
	for _, i := range cs.Interfaces {
		if p := c.syms.Class(i); p != nil && p.Flags.Has(sym.FlagSealed) {
			return true
		}
	}
	return false
}

// hasLocalSubclass reports whether any type in this unit extends cs, which is
// what an implicit permits clause requires.
func (c *checker) hasLocalSubclass(cs *sym.ClassSym) bool {
	for _, t := range c.unit.Types {
		if c.extendsDirectly(t, cs) {
			return true
		}
		found := false
		t.Members.Each(func(s sym.Symbol) bool {
			if nested, ok := s.(*sym.ClassSym); ok && c.extendsDirectly(nested, cs) {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

// permittedSubtypes returns the sealed hierarchy's leaves, for exhaustiveness.
func (c *checker) permittedSubtypes(t types.Type) []*sym.ClassSym {
	ct, ok := t.(*types.ClassType)
	if !ok || ct.Sym == nil || !ct.Sym.Flags.Has(sym.FlagSealed) {
		return nil
	}
	var out []*sym.ClassSym
	for _, name := range ct.Sym.Permits {
		if sub := c.syms.Class(name); sub != nil {
			out = append(out, sub)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}