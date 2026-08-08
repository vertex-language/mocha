package ast

import "github.com/vertex-language/mocha/token"

type (
	// Block is `{ [BlockStatements] }`.
	Block struct {
		Span
		Lbrace token.Pos
		Stmts  []Stmt
		Rbrace token.Pos
	}

	// EmptyStmt is a lone `;`.
	EmptyStmt struct {
		Span
	}

	// ExprStmt wraps a StatementExpression. The parser has already checked that
	// X is one of the seven admissible forms.
	ExprStmt struct {
		Span
		X Expr
	}

	// DeclStmt is a local variable, class or interface declaration in statement
	// position. Decl is a *VarDecl, *ClassDecl, *InterfaceDecl, *EnumDecl or
	// *RecordDecl.
	DeclStmt struct {
		Span
		Decl Decl
	}

	// LabeledStmt is `Identifier : Statement`.
	LabeledStmt struct {
		Span
		Label *Ident
		Colon token.Pos
		Stmt  Stmt
	}

	// IfStmt covers both IfThenStatement and IfThenElseStatement; the
	// NoShortIf variants are a disambiguation device with no tree consequence.
	IfStmt struct {
		Span
		IfPos   token.Pos
		Cond    Expr
		Then    Stmt
		ElsePos token.Pos
		Else    Stmt
	}

	WhileStmt struct {
		Span
		WhilePos token.Pos
		Cond     Expr
		Body     Stmt
	}

	DoStmt struct {
		Span
		DoPos    token.Pos
		Body     Stmt
		WhilePos token.Pos
		Cond     Expr
	}

	// ForStmt is a BasicForStatement. Init is a *VarDecl or a list of
	// ExprStmts, held as Stmts either way.
	ForStmt struct {
		Span
		ForPos token.Pos
		Init   []Stmt
		Cond   Expr
		Post   []Expr
		Body   Stmt
	}

	// RangeStmt is an EnhancedForStatement: `for ( Decl : X ) Body`.
	RangeStmt struct {
		Span
		ForPos token.Pos
		Decl   *VarDecl
		Colon  token.Pos
		X      Expr
		Body   Stmt
	}

	SwitchStmt struct {
		Span
		SwitchPos token.Pos
		Tag       Expr
		Block     *SwitchBlock
	}

	BreakStmt struct {
		Span
		BreakPos token.Pos
		Label    *Ident
	}

	ContinueStmt struct {
		Span
		ContinuePos token.Pos
		Label       *Ident
	}

	ReturnStmt struct {
		Span
		ReturnPos token.Pos
		Result    Expr
	}

	// YieldStmt is `yield Expression ;`. The `yield` spelling reached here only
	// because the parser was in a production that admits the contextual
	// keyword; elsewhere it is a method name.
	YieldStmt struct {
		Span
		YieldPos token.Pos
		X        Expr
	}

	ThrowStmt struct {
		Span
		ThrowPos token.Pos
		X        Expr
	}

	SyncStmt struct {
		Span
		SyncPos token.Pos
		X       Expr
		Body    *Block
	}

	// AssertStmt is `assert X [: Msg] ;`.
	AssertStmt struct {
		Span
		AssertPos token.Pos
		X         Expr
		Colon     token.Pos
		Msg       Expr
	}

	// TryStmt covers both TryStatement and TryWithResourcesStatement;
	// Resources is nil for the former.
	TryStmt struct {
		Span
		TryPos     token.Pos
		Lparen     token.Pos
		Resources  []*Resource
		Semi       token.Pos // trailing `;` in the resource list, or NoPos
		Rparen     token.Pos
		Body       *Block
		Catches    []*CatchClause
		FinallyPos token.Pos
		Finally    *Block
	}

	// ConstructorCall is an ExplicitConstructorInvocation. It is a statement
	// here because a flexible constructor body may place ordinary statements
	// both before and after it (the prologue and the epilogue), and the tree
	// should not have to split the list.
	ConstructorCall struct {
		Span
		X        Expr       // ExpressionName or Primary qualifier, or nil
		TypeArgs *TypeArgs
		Kind     token.Kind // token.THIS or token.SUPER
		KwPos    token.Pos
		Lparen   token.Pos
		Args     []Expr
		Rparen   token.Pos
	}

	// BadStmt marks a statement the parser could not read.
	BadStmt struct {
		Span
	}
)

// Resource is one element of a ResourceSpecification: either a declaration or
// a VariableAccess. Exactly one field is non-nil.
type Resource struct {
	Span
	Decl *VarDecl
	X    Expr
}

// CatchClause is `catch ( CatchFormalParameter ) Block`. Types holds the union
// members in order; a single type is a one-element slice.
type CatchClause struct {
	Span
	CatchPos token.Pos
	Mods     *Modifiers
	Types    []Type
	Name     *Ident
	Body     *Block
}

// SwitchBlock is the body of a switch statement or expression. Exactly one of
// Rules and Groups is populated: the arrow form and the colon form cannot be
// mixed. Labels holds any trailing labels of a colon-form block that govern no
// statements.
type SwitchBlock struct {
	Span
	Lbrace token.Pos
	Rules  []*SwitchRule
	Groups []*SwitchGroup
	Labels []*SwitchLabel
	Rbrace token.Pos
}

// SwitchRule is `SwitchLabel -> Body`, where Body is an Expr, a *Block or a
// *ThrowStmt.
type SwitchRule struct {
	Span
	Label *SwitchLabel
	Arrow token.Pos
	Body  Node
}

// SwitchGroup is one or more colon-form labels and the statements they govern.
type SwitchGroup struct {
	Span
	Labels []*SwitchLabel
	Stmts  []Stmt
}

// SwitchLabel is a `case` or `default` label.
//
// Cases holds Exprs (CaseConstant), Patterns (CasePattern), or the single
// *BasicLit of `case null`. Default is true for a bare `default` — in which
// case Cases is empty — and for the `case null, default` form, where it is not.
type SwitchLabel struct {
	Span
	KwPos      token.Pos // `case` or `default`
	Cases      []Node
	Default    bool
	DefaultPos token.Pos // NoPos unless Default
	WhenPos    token.Pos
	Guard      Expr
}

func (*Block) stmtNode()           {}
func (*EmptyStmt) stmtNode()       {}
func (*ExprStmt) stmtNode()        {}
func (*DeclStmt) stmtNode()        {}
func (*LabeledStmt) stmtNode()     {}
func (*IfStmt) stmtNode()          {}
func (*WhileStmt) stmtNode()       {}
func (*DoStmt) stmtNode()          {}
func (*ForStmt) stmtNode()         {}
func (*RangeStmt) stmtNode()       {}
func (*SwitchStmt) stmtNode()      {}
func (*BreakStmt) stmtNode()       {}
func (*ContinueStmt) stmtNode()    {}
func (*ReturnStmt) stmtNode()      {}
func (*YieldStmt) stmtNode()       {}
func (*ThrowStmt) stmtNode()       {}
func (*SyncStmt) stmtNode()        {}
func (*AssertStmt) stmtNode()      {}
func (*TryStmt) stmtNode()         {}
func (*ConstructorCall) stmtNode() {}
func (*BadStmt) stmtNode()         {}