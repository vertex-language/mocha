package ast

import "github.com/vertex-language/mocha/token"

type (
	// BasicLit is any Literal (§3.10) except the boolean and null forms, which
	// it also covers via Kind. The spelling is undecoded.
	BasicLit struct {
		Span
		Kind token.Kind // INT, FLOAT, CHAR, STRING, TEXTBLOCK, TRUE, FALSE, NULL
	}

	// ClassLit is `Type.class`, `int[].class` or `void.class`.
	ClassLit struct {
		Span
		Type     Type      // nil for `void.class`
		VoidPos  token.Pos // NoPos unless Type is nil
		Dims     []*Dim
		ClassPos token.Pos
	}

	// This is `this` or `TypeName.this`.
	This struct {
		Span
		Qualifier *Name
		ThisPos   token.Pos
	}

	// Super stands for `super` and `TypeName.super` in the positions that admit
	// them: field access, method invocation, method reference, and explicit
	// constructor invocation.
	Super struct {
		Span
		Qualifier *Name
		SuperPos  token.Pos
	}

	// ParenExpr is `( Expression )`.
	ParenExpr struct {
		Span
		Lparen token.Pos
		X      Expr
		Rparen token.Pos
	}

	// SelectorExpr is a FieldAccess: `X . Name`. X may be a *Super.
	SelectorExpr struct {
		Span
		X   Expr
		Dot token.Pos
		Sel *Ident
	}

	// IndexExpr is an ArrayAccess: `X [ Index ]`.
	IndexExpr struct {
		Span
		X      Expr
		Lbrack token.Pos
		Index  Expr
		Rbrack token.Pos
	}

	// CallExpr is a MethodInvocation. X is nil for the bare MethodName form and
	// may be a *Super or a *Name that resolution will classify as a type or an
	// expression.
	CallExpr struct {
		Span
		X        Expr
		TypeArgs *TypeArgs
		Fun      *Ident
		Lparen   token.Pos
		Args     []Expr
		Rparen   token.Pos
	}

	// MethodRef is `X :: [TypeArguments] Name` or `X :: [TypeArguments] new`.
	// X may be an Expr or a Type; the ambiguity is real and survives here.
	MethodRef struct {
		Span
		X        Node
		ColonPos token.Pos
		TypeArgs *TypeArgs
		Name     *Ident    // nil for the `new` form
		NewPos   token.Pos // NoPos unless Name is nil
	}

	// NewExpr is a ClassInstanceCreationExpression. Outer is the qualifying
	// expression of the `Primary . new` form, or nil. Body non-nil means an
	// anonymous class, which is a class declaration the grammar does not call
	// one.
	NewExpr struct {
		Span
		Outer    Expr
		NewPos   token.Pos
		TypeArgs *TypeArgs
		Type     *NamedType
		Lparen   token.Pos
		Args     []Expr
		Rparen   token.Pos
		Body     []Decl
		Lbrace   token.Pos
		Rbrace   token.Pos
	}

	// NewArrayExpr is an ArrayCreationExpression. Exactly one of DimExprs and
	// Init is populated: `new int[n][]` versus `new int[][]{...}`.
	NewArrayExpr struct {
		Span
		NewPos   token.Pos
		Elt      Type
		DimExprs []*DimExpr
		Dims     []*Dim
		Init     *ArrayInit
	}

	// ArrayInit is `{ [VariableInitializerList] [,] }`. Elts are Exprs or
	// nested *ArrayInits.
	ArrayInit struct {
		Span
		Lbrace token.Pos
		Elts   []Node
		Comma  token.Pos // trailing comma, or NoPos
		Rbrace token.Pos
	}

	// UnaryExpr is a prefix operator: + - ~ ! ++ --.
	UnaryExpr struct {
		Span
		Op    token.Kind
		OpPos token.Pos
		X     Expr
	}

	// PostfixExpr is `X ++` or `X --`.
	PostfixExpr struct {
		Span
		X     Expr
		Op    token.Kind
		OpPos token.Pos
	}

	// BinaryExpr covers every left-associative binary level from `||` down to
	// `%`. Op may be token.SHR or token.USHR, which no scanned token ever
	// carries: the parser assembled it from adjacent `>` tokens, and OpPos is
	// the first of them.
	BinaryExpr struct {
		Span
		X     Expr
		Op    token.Kind
		OpPos token.Pos
		OpEnd token.Pos // past the last token of a joined operator
		Y     Expr
	}

	// InstanceOfExpr takes a type or a pattern on the right, never an
	// expression. Exactly one of Type and Pattern is non-nil.
	InstanceOfExpr struct {
		Span
		X       Expr
		OpPos   token.Pos
		Type    Type
		Pattern Pattern
	}

	// CondExpr is `Cond ? Then : Else`, right-associative.
	CondExpr struct {
		Span
		Cond     Expr
		Question token.Pos
		Then     Expr
		Colon    token.Pos
		Else     Expr
	}

	// AssignExpr is `LHS op RHS`. Op may be token.SHR_ASSIGN or
	// token.USHR_ASSIGN, joined by the parser like BinaryExpr's.
	AssignExpr struct {
		Span
		LHS   Expr
		Op    token.Kind
		OpPos token.Pos
		OpEnd token.Pos
		RHS   Expr
	}

	// CastExpr is `( Type {& Type} ) X`. Bounds holds any AdditionalBounds.
	CastExpr struct {
		Span
		Lparen token.Pos
		Type   Type
		Bounds []Type
		Rparen token.Pos
		X      Expr
	}

	// LambdaExpr is `LambdaParameters -> LambdaBody`. Paren records whether the
	// parameter list was parenthesized, which a formatter needs and a concise
	// single parameter does not imply. Body is an Expr or a *Block.
	LambdaExpr struct {
		Span
		Lparen token.Pos // NoPos when Paren is false
		Params []*Param
		Rparen token.Pos
		Paren  bool
		Arrow  token.Pos
		Body   Node
	}

	// SwitchExpr is `switch ( X ) SwitchBlock`, §15.28.
	SwitchExpr struct {
		Span
		SwitchPos token.Pos
		Tag       Expr
		Block     *SwitchBlock
	}

	// BadExpr marks an expression the parser could not read.
	BadExpr struct {
		Span
	}
)

// DimExpr is `{Annotation} [ Expression ]` in an array creation.
type DimExpr struct {
	Span
	Annotations []*Annotation
	Lbrack      token.Pos
	X           Expr
	Rbrack      token.Pos
}

func (*Ident) exprNode()          {}
func (*Name) exprNode()           {}
func (*BasicLit) exprNode()       {}
func (*ClassLit) exprNode()       {}
func (*This) exprNode()           {}
func (*Super) exprNode()          {}
func (*ParenExpr) exprNode()      {}
func (*SelectorExpr) exprNode()   {}
func (*IndexExpr) exprNode()      {}
func (*CallExpr) exprNode()       {}
func (*MethodRef) exprNode()      {}
func (*NewExpr) exprNode()        {}
func (*NewArrayExpr) exprNode()   {}
func (*ArrayInit) exprNode()      {}
func (*UnaryExpr) exprNode()      {}
func (*PostfixExpr) exprNode()    {}
func (*BinaryExpr) exprNode()     {}
func (*InstanceOfExpr) exprNode() {}
func (*CondExpr) exprNode()       {}
func (*AssignExpr) exprNode()     {}
func (*CastExpr) exprNode()       {}
func (*LambdaExpr) exprNode()     {}
func (*SwitchExpr) exprNode()     {}
func (*BadExpr) exprNode()        {}