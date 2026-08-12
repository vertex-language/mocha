package sym

import "github.com/vertex-language/mocha/ast"

// RecordComponents returns a record's components in declaration order, or nil
// for a class that is not a record. The class is completed first: the list is
// filled in by whichever completer ran, source or binary, and neither runs
// before Complete is called.
func (c *ClassSym) RecordComponents() []*VarSym {
	c.Complete()
	return c.recordComponents
}

// ModifierFlags is modifierFlags, exported for a package that converts an
// ast.Modifiers list itself. attr does, for a local variable, an exception
// parameter, or a resource — none of which sym.Enter ever sees, since a
// method body is not walked here.
func ModifierFlags(m *ast.Modifiers) Flags { return modifierFlags(m) }