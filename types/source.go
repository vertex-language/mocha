package types

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
)

// sourceSupers resolves a declaration's extends and implements clauses.
//
// A record and an enum name no supertype in source: §8.10 and §8.9 give them
// java.lang.Record and java.lang.Enum<E>, and the class file that comes out
// the far end has to say so.
func (t *Table) sourceSupers(c *sym.ClassSym, e *env) (Type, []Type) {
	var super Type
	var ifaces []Type

	add := func(list []ast.Type) {
		for _, x := range list {
			ifaces = append(ifaces, t.fromAST(e, x))
		}
	}

	switch d := c.Decl.(type) {
	case *ast.ClassDecl:
		if d.Extends != nil {
			super = t.fromAST(e, d.Extends)
		} else if c.Binary != sym.ObjectName {
			super = t.Object()
		}
		add(d.Implements)

	case *ast.InterfaceDecl:
		// An interface has no superclass; its extends clause lists interfaces.
		add(d.Extends)

	case *ast.AnnotationDecl:
		ifaces = append(ifaces, t.named("java/lang/annotation/Annotation"))

	case *ast.EnumDecl:
		super = t.enumSuper(c)
		add(d.Implements)

	case *ast.RecordDecl:
		super = t.named(sym.RecordName)
		add(d.Implements)
	}
	return super, ifaces
}

// enumSuper builds java.lang.Enum<E> for the enum E itself, which is what
// makes Enum's own methods resolve at the right instantiation.
func (t *Table) enumSuper(c *sym.ClassSym) Type {
	base := t.named(sym.EnumName)
	ct, ok := base.(*ClassType)
	if !ok {
		return base
	}
	if len(t.TypeParams(ct.Sym)) != 1 {
		return base
	}
	return &ClassType{Sym: ct.Sym, Args: []Type{&ClassType{Sym: c}}}
}

// fromAST resolves a type as written in source.
func (t *Table) fromAST(e *env, x ast.Type) Type {
	switch n := x.(type) {
	case nil:
		return errorType("")

	case *ast.PrimitiveType:
		return primitiveOfToken(n.Kind)

	case *ast.ArrayType:
		return arrayOf(t.fromAST(e, n.Elt), len(n.Dims))

	case *ast.NamedType:
		return t.fromNamed(e, n)

	case *ast.Wildcard:
		return t.fromWildcard(e, n)

	case *ast.VarType:
		// `var` stands in a type's position but is not a type. Inference is
		// attr's, and it substitutes the initialiser's type here.
		return errorType("var")

	case *ast.BadType:
		return errorType("")
	}
	return errorType("")
}

func (t *Table) fromWildcard(e *env, w *ast.Wildcard) Type {
	switch w.BoundKind {
	case token.EXTENDS:
		return &Wildcard{Wild: Extends, Bound: t.fromAST(e, w.Bound)}
	case token.SUPER:
		return &Wildcard{Wild: Super, Bound: t.fromAST(e, w.Bound)}
	}
	return &Wildcard{Wild: Unbounded}
}

// fromNamed resolves a ClassType, InterfaceType or TypeVariable — which of the
// three it is, is exactly what this function decides. ast deliberately does
// not distinguish them.
func (t *Table) fromNamed(e *env, n *ast.NamedType) Type {
	if e == nil || e.unit == nil {
		return errorType(nameText(n, nil))
	}
	f := e.unit.Src

	// A qualified name resolves outermost first, then one nesting step at a
	// time, so a.b.C.D works whether C is a package or an enclosing type.
	if n.Qualifier != nil {
		outer := t.fromNamed(e, n.Qualifier)
		oc, ok := outer.(*ClassType)
		if !ok {
			return outer // already an error; do not cascade
		}
		simple := identText(n.Name, f)
		nested := oc.Sym.Nested(simple)
		if nested == nil {
			// The qualifier was a package prefix, not a type.
			if c := t.syms.Class(sym.NestedBinary(oc.Sym.Binary, simple)); c != nil {
				nested = c
			}
		}
		if nested == nil {
			return errorType(oc.Sym.Binary + "$" + simple)
		}
		var enclosing *ClassType
		if !nested.Flags.Has(sym.FlagStatic) {
			enclosing = oc
		}
		return t.classOf(nested, t.typeArgs(e, n.TypeArgs, nested), enclosing)
	}

	simple := identText(n.Name, f)

	// 1. A type parameter in scope shadows everything.
	if n.TypeArgs == nil {
		if tv := e.lookupVar(simple); tv != nil {
			return tv
		}
	}

	// 2. A member type of this class or an enclosing one.
	for c := e.class; c != nil; c = c.Outer {
		if nested := c.Nested(simple); nested != nil {
			return t.classOf(nested, t.typeArgs(e, n.TypeArgs, nested), nil)
		}
	}

	// 3. §6.5.5's order, which sym.Unit already implements.
	if c := e.unit.FindType(simple); c != nil {
		return t.classOf(c, t.typeArgs(e, n.TypeArgs, c), nil)
	}
	return errorType(simple)
}

// typeArgs resolves a type argument list. A diamond supplies no arguments —
// inferring them is attr's job — so it yields a raw use here.
func (t *Table) typeArgs(e *env, ta *ast.TypeArgs, owner *sym.ClassSym) []Type {
	if ta == nil || ta.Diamond || len(ta.List) == 0 {
		return nil
	}
	out := make([]Type, 0, len(ta.List))
	for _, a := range ta.List {
		out = append(out, t.fromAST(e, a))
	}
	return out
}

func identText(id *ast.Ident, f *token.File) string {
	if id == nil || f == nil {
		return ""
	}
	if id.Underscore {
		return "_"
	}
	return id.Name(f)
}

func nameText(n *ast.NamedType, f *token.File) string {
	if n == nil {
		return ""
	}
	return identText(n.Name, f)
}

// primitiveOfToken maps a PrimitiveType's keyword. Three reserved words
// collide with literal kinds and are spelled differently in token: CHARK,
// FLOATK, INT_KW.
func primitiveOfToken(k token.Kind) Type {
	switch k {
	case token.BOOLEAN:
		return Boolean
	case token.BYTE:
		return Byte
	case token.SHORT:
		return Short
	case token.CHARK:
		return Char
	case token.INT_KW:
		return Int
	case token.LONG:
		return Long
	case token.FLOATK:
		return Float
	case token.DOUBLE:
		return Double
	}
	return errorType("")
}