package warn

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
)

// Deprecation warnings, §9.6.4.6.
//
// sym already carries FlagDeprecated from both sides: annotationFlags picks up
// @Deprecated in source, and binaryCompleter reads the Deprecated attribute
// from a class file. Neither phase did anything with it. This is what it was
// for.
//
// The rule that keeps this from being noise: use of a deprecated thing from
// *within* something deprecated is not reported. Deprecating a class does not
// oblige you to rewrite its own internals.

func (c *checker) deprecatedUse(n ast.Node) {
	s := c.info.Use(n)
	if s == nil {
		return
	}
	b := s.Base()
	if !b.Flags.Has(sym.FlagDeprecated) {
		return
	}
	if c.inDeprecatedContext() {
		return
	}

	switch t := s.(type) {
	case *sym.ClassSym:
		c.warnf("deprecation", n.Pos(), n.End(),
			"%s is deprecated", sym.Dotted(t.Binary))
	case *sym.MethodSym:
		owner := ""
		if t.Class != nil {
			owner = sym.Dotted(t.Class.Binary)
		}
		name := t.Name
		if t.IsConstructor() {
			c.warnf("deprecation", n.Pos(), n.End(),
				"the constructor of %s is deprecated", owner)
			return
		}
		c.warnf("deprecation", n.Pos(), n.End(),
			"%s in %s is deprecated", name, owner)
	case *sym.VarSym:
		owner := ""
		if t.Class != nil {
			owner = sym.Dotted(t.Class.Binary)
		}
		c.warnf("deprecation", n.Pos(), n.End(),
			"%s in %s is deprecated", t.Name, owner)
	}
}

// deprecatedSupertypes reports extending or implementing something deprecated.
func (c *checker) deprecatedSupertypes(cs *sym.ClassSym) {
	if cs.Flags.Has(sym.FlagDeprecated) {
		return
	}
	check := func(name string) {
		if name == "" {
			return
		}
		p := c.syms.Class(name)
		if p != nil && p.Flags.Has(sym.FlagDeprecated) {
			c.warnf("deprecation", cs.Pos, cs.End,
				"%s is deprecated", sym.Dotted(name))
		}
	}
	check(cs.Super)
	for _, i := range cs.Interfaces {
		check(i)
	}
}

// inDeprecatedContext reports whether the enclosing declaration is itself
// deprecated, which exempts everything inside it.
func (c *checker) inDeprecatedContext() bool {
	for _, layer := range c.suppressed {
		if layer != nil && layer["__deprecated__"] {
			return true
		}
	}
	return false
}