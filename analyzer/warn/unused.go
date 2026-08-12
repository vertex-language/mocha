package warn

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
)

// Unused declarations. All warnings, all suppressible.
//
// "Unused" here means never read. flow already knows which variables an
// expression named, so this is a lookup rather than a second walk — and it is
// the read that matters, not the write: a variable assigned and never read is
// unused, which is the case worth reporting.

// unusedLocals reports locals never read. A local named `_` is exempt: §6.1
// makes the unnamed variable explicitly a declaration you do not intend to
// use, which is the whole point of the form.
func (c *checker) unusedLocals(d *ast.VarDecl) {
	for _, decl := range d.Names {
		v, ok := c.info.Use(decl).(*sym.VarSym)
		if !ok || v == nil {
			continue
		}
		if v.Unnamed() {
			continue
		}
		if c.read[v] {
			continue
		}
		c.warnf("unused", decl.Pos(), decl.End(),
			"variable %s is never used", v.Name)
	}
}

// unusedImports reports single-type imports whose name never resolved to
// anything. An on-demand import is not reported: it is a search path, and
// deciding whether any of it was needed would mean tracking which package
// every resolved name came from.
func (c *checker) unusedImports() {
	f := c.unit.File
	if f == nil {
		return
	}
	for _, imp := range f.Imports {
		if imp.OnDemand || imp.Module || imp.Static {
			continue
		}
		if imp.Name == nil || len(imp.Name.Parts) == 0 {
			continue
		}
		simple := imp.Name.Parts[len(imp.Name.Parts)-1].Name(c.file())
		if c.used[simple] {
			continue
		}
		c.warnf("unused", imp.Pos(), imp.End(),
			"import %s is never used", simple)
	}
}

// unusedPrivates reports private members nothing in the unit references.
// Private is the only visibility this is decidable for: anything else may be
// used by a class not in this compilation.
func (c *checker) unusedPrivates(cs *sym.ClassSym) {
	cs.Members.Each(func(s sym.Symbol) bool {
		b := s.Base()
		if !b.Flags.Has(sym.FlagPrivate) || b.Flags.Has(sym.FlagImplicit) {
			return true
		}
		switch m := s.(type) {
		case *sym.VarSym:
			if !c.read[m] {
				c.warnf("unused", b.Pos, b.End,
					"private field %s is never used", b.Name)
			}
		case *sym.MethodSym:
			// A private constructor is a deliberate idiom — it is how a
			// utility class forbids instantiation — so it is never reported.
			if m.IsConstructor() {
				return true
			}
			if !c.methodReferenced(m) {
				c.warnf("unused", b.Pos, b.End,
					"private method %s is never used", b.Name)
			}
		}
		return true
	})
}

func (c *checker) methodReferenced(m *sym.MethodSym) bool {
	for _, s := range c.info.Uses {
		if s == sym.Symbol(m) {
			return true
		}
	}
	return false
}