package warn

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/types"
)

// The parser's leftovers.
//
// Each of these parses cleanly because the shape is uniform, and each is
// semantic rather than grammatical. The parser listed them as deliberate
// non-decisions; this is where they get decided.

// unitShape checks the compilation unit's form (§7.3). The parser reads
// top-level members either way and sets Compact afterwards, because a unit is
// compact only if it contains a member that only a compact unit can have.
func (c *checker) unitShape() {
	f := c.unit.File
	if f == nil {
		return
	}
	if f.Compact && f.Package != nil {
		c.errorf(f.Package.Pos(), f.Package.End(),
			"a compact compilation unit cannot declare a package")
	}
	if f.Module != nil && len(f.Decls) > 0 {
		c.errorf(f.Decls[0].Pos(), f.Decls[0].End(),
			"a modular compilation unit cannot declare a type")
	}

	// §7.6: at most one top-level type may be public, and its name must match
	// the file. mocha checks the first half; the second is a filesystem
	// convention javac enforces and nothing in the class file records.
	public := 0
	for _, t := range c.unit.Types {
		if t.Flags.Has(flagPublic()) {
			public++
			if public > 1 {
				c.errorf(t.Pos, t.End,
					"only one top-level type may be public")
			}
		}
	}
}

// resources checks §14.20.3: a declaring resource declares exactly one
// variable, and it must have an initializer.
func (c *checker) resources(n *ast.TryStmt) {
	for _, r := range n.Resources {
		if r.Decl == nil {
			continue
		}
		if len(r.Decl.Names) != 1 {
			c.errorf(r.Pos(), r.End(),
				"a resource must declare exactly one variable")
			continue
		}
		if r.Decl.Names[0].Init == nil {
			c.errorf(r.Pos(), r.End(),
				"a resource declaration must have an initializer")
		}
	}
}

// lambdaParams checks §15.27.1: the concise and normal parameter forms cannot
// be mixed. The parser produces an ast.Param either way, with Type nil for the
// concise one, so the mixing is visible only as an inconsistency across the
// list.
func (c *checker) lambdaParams(n *ast.LambdaExpr) {
	if len(n.Params) < 2 {
		return
	}
	typed := n.Params[0].Type != nil
	for _, p := range n.Params[1:] {
		if (p.Type != nil) != typed {
			c.errorf(n.Pos(), n.End(),
				"cannot mix inferred and declared lambda parameter types")
			return
		}
	}
	// A concise parameter list cannot carry modifiers or annotations either.
	if !typed {
		for _, p := range n.Params {
			if p.Mods != nil && len(p.Mods.List) > 0 {
				c.errorf(p.Pos(), p.End(),
					"a lambda parameter without a type cannot have modifiers")
			}
		}
	}
}

// typeArgs checks §4.5.1: a TypeArgument is a ReferenceType, so a primitive is
// admissible only as an array element type. The parser reads the shape and
// leaves the check here.
func (c *checker) typeArgs(n *ast.NamedType) {
	if n.TypeArgs == nil {
		return
	}
	for _, arg := range n.TypeArgs.List {
		if _, ok := arg.(*ast.PrimitiveType); ok {
			c.errorf(arg.Pos(), arg.End(),
				"unexpected type: a type argument must be a reference type")
			continue
		}
		t := c.info.Type(arg)
		if t.Kind().IsPrimitive() {
			c.errorf(arg.Pos(), arg.End(),
				"unexpected type: %s is not a reference type", t)
		}
	}
}

// isElementType implements §9.6.1's restriction on annotation element types.
func (c *checker) isElementType(t types.Type) bool {
	switch n := t.(type) {
	case *types.Basic:
		return n.Kind().IsPrimitive()
	case *types.ArrayType:
		// One dimension only: an array of arrays cannot be an element type.
		if _, nested := n.Elem.(*types.ArrayType); nested {
			return false
		}
		return c.isElementType(n.Elem)
	case *types.ClassType:
		if n.Sym == nil {
			return false
		}
		switch n.Sym.Binary {
		case "java/lang/String", "java/lang/Class":
			return true
		}
		return n.Sym.IsEnum() || n.Sym.IsAnnotation()
	}
	return false
}