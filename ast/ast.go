// Package ast defines the syntax tree mocha's parser builds.
//
// Four hierarchies — Expr, Stmt, Decl, Type — plus Pattern, which §14.30 makes
// its own grammatical category. Every node embeds a Span, so Pos and End are
// stored rather than derived: a node built during error recovery still has a
// real, non-empty extent (invariant 3), which a fold over possibly-nil children
// could not guarantee.
//
// Nodes hold no text. An Ident is two positions and a token.Ctx; a literal is
// two positions and a token.Kind. Decoding "1_024", stripping a text block's
// incidental whitespace, and deciding whether a `var` spelling is a keyword all
// belong to phases above this one (invariant 1).
package ast

import "github.com/vertex-language/mocha/token"

// Node is the interface satisfied by every tree node.
type Node interface {
	Pos() token.Pos // first byte of the node
	End() token.Pos // one past the last byte
}

// Span is the stored extent of a node. Every node type embeds it.
type Span struct {
	Lo, Hi token.Pos
}

func (s Span) Pos() token.Pos { return s.Lo }
func (s Span) End() token.Pos { return s.Hi }

// At builds a Span. The parser uses it to widen a node over its children.
func At(lo, hi token.Pos) Span { return Span{Lo: lo, Hi: hi} }

// The four expression, statement, declaration and type hierarchies, plus
// patterns. The marker methods keep unrelated nodes out of each position.
type (
	Expr interface {
		Node
		exprNode()
	}
	Stmt interface {
		Node
		stmtNode()
	}
	Decl interface {
		Node
		declNode()
	}
	Type interface {
		Node
		typeNode()
	}
	Pattern interface {
		Node
		patternNode()
	}
)

// Releaser is implemented by whatever owns a tree's backing storage. The parser
// implements it with its arena; ast neither knows nor imports that.
type Releaser interface {
	Release()
}

// Ident is an Identifier, a TypeIdentifier or an UnqualifiedMethodIdentifier —
// the tree does not distinguish them, because which restriction applies is a
// property of the production the parser was in, and it has already enforced it.
//
// Ctx is non-zero when the spelling is one of the seventeen contextual
// keywords, whether or not this occurrence is one.
type Ident struct {
	Span
	Ctx token.Ctx

	// Underscore marks the `_` form of VariableDeclaratorId or
	// ConciseLambdaParameter: an unnamed variable, not an identifier.
	Underscore bool
}

// Name returns the identifier's spelling, resolved through the unit that
// produced it. The tree itself holds no strings.
func (i *Ident) Name(f *token.File) string { return f.Slice(i.Lo, i.Hi) }

// Modifier is one element of a modifier list: either an annotation or a
// keyword. Both forms are kept, in the order written, because the JLS's
// canonical order is a style rule and a formatter needs the truth.
type Modifier struct {
	Span
	Annotation *Annotation // nil for a keyword modifier
	Kind       token.Kind  // token.PUBLIC, token.NON_SEALED, ...; ILLEGAL when Annotation != nil
}

// Modifiers is a possibly-empty modifier list. A nil *Modifiers means none were
// written; callers should treat it as empty rather than dereference.
type Modifiers struct {
	Span
	List []*Modifier
}

func (m *Modifiers) Has(k token.Kind) bool {
	if m == nil {
		return false
	}
	for _, x := range m.List {
		if x.Annotation == nil && x.Kind == k {
			return true
		}
	}
	return false
}

// Annotation covers all three forms of §9.7. Pairs is nil for a marker
// annotation; a single-element annotation has one pair whose Name is nil.
type Annotation struct {
	Span
	AtPos token.Pos
	Name  *Name
	Pairs []*ElementValuePair
}

// ElementValuePair is `Identifier = ElementValue`, or the bare value of a
// single-element annotation (Name nil). Value is an Expr, an *ElementValueArray
// or a nested *Annotation.
type ElementValuePair struct {
	Span
	Name  *Ident
	Value Node
}

// ElementValueArray is `{ [ElementValueList] [,] }` in an annotation.
type ElementValueArray struct {
	Span
	Elts  []Node
	Comma token.Pos // trailing comma, or NoPos
}

// Name is a dotted name: ModuleName, PackageName, TypeName, ExpressionName,
// PackageOrTypeName or AmbiguousName. Which one is a matter of resolution, so
// the tree keeps only the parts.
type Name struct {
	Span
	Parts []*Ident
}

// File is one CompilationUnit (§7.3). Exactly one shape is populated:
//
//   - ordinary: Package optional, Decls holds top-level type declarations
//   - compact:  Compact true, Decls holds class members with no enclosing class
//   - modular:  Module non-nil
type File struct {
	Span
	Unit    *token.File // the position space every span here resolves through
	Package *PackageDecl
	Imports []*ImportDecl
	Decls   []Decl
	Module  *ModuleDecl
	Compact bool

	// Releaser is set by the parser. Release is safe to call on a tree that has
	// none, and safe to call twice.
	Releaser Releaser
}

// Release returns the tree's backing storage. Every node in the tree is invalid
// afterwards.
func (f *File) Release() {
	if f == nil || f.Releaser == nil {
		return
	}
	r := f.Releaser
	f.Releaser = nil
	r.Release()
}