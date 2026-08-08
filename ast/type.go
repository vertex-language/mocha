package ast

import "github.com/vertex-language/mocha/token"

// The JLS's Unann* nonterminals exist only to keep an annotation from being
// read as part of an enclosing construct. That is a parsing concern, so the
// tree has one type hierarchy: a NamedType with no annotations is what an
// UnannClassType produced.

type (
	// PrimitiveType is a NumericType or `boolean`, §4.2.
	PrimitiveType struct {
		Span
		Annotations []*Annotation
		Kind        token.Kind // token.INT_KW, token.BOOLEAN, ...
		KwPos       token.Pos
	}

	// NamedType is a ClassType, an InterfaceType or a TypeVariable — the
	// distinction is resolution's, not syntax's. Qualifier is the preceding
	// PackageName or ClassOrInterfaceType, or nil.
	NamedType struct {
		Span
		Qualifier   *NamedType
		Annotations []*Annotation
		Name        *Ident
		TypeArgs    *TypeArgs
	}

	// ArrayType is an element type plus one or more Dims, §10.1.
	ArrayType struct {
		Span
		Elt  Type
		Dims []*Dim
	}

	// VarType is the `var` of LocalVariableType or LambdaParameterType. It is
	// not a type in §4's sense, but it stands in a type's position.
	VarType struct {
		Span
	}

	// Wildcard is `? [extends|super ReferenceType]`, §4.5.1.
	Wildcard struct {
		Span
		Annotations []*Annotation
		QPos        token.Pos
		BoundKind   token.Kind // token.EXTENDS, token.SUPER, or ILLEGAL
		Bound       Type
	}

	// BadType marks a type the parser could not read. Its span covers the
	// tokens it gave up on, so a consumer can still report a location.
	BadType struct {
		Span
	}
)

// Dim is one `{Annotation} [ ]` pair of a Dims.
type Dim struct {
	Span
	Annotations []*Annotation
	Lbrack      token.Pos
	Rbrack      token.Pos
}

// TypeArgs is `< TypeArgumentList >`. Diamond is true for `<>`.
//
// Gt is the position of the single `>` that closed this list. Because the
// scanner never merges `>` with a following `>`, nested arguments need no
// special handling here: each list closes on its own token.
type TypeArgs struct {
	Span
	Lt      token.Pos
	Gt      token.Pos
	List    []Type // each element is a ReferenceType or a *Wildcard
	Diamond bool
}

// TypeParams is `< TypeParameterList >`.
type TypeParams struct {
	Span
	Lt   token.Pos
	Gt   token.Pos
	List []*TypeParam
}

// TypeParam is `{Annotation} TypeIdentifier [TypeBound]`. Bounds holds the
// TypeBound followed by any AdditionalBounds, flattened.
type TypeParam struct {
	Span
	Annotations []*Annotation
	Name        *Ident
	Bounds      []Type
}

func (*PrimitiveType) typeNode() {}
func (*NamedType) typeNode()     {}
func (*ArrayType) typeNode()     {}
func (*VarType) typeNode()       {}
func (*Wildcard) typeNode()      {}
func (*BadType) typeNode()       {}