package ast

import "github.com/vertex-language/mocha/token"

type (
	// PackageDecl is `{PackageModifier} package Name ;`.
	PackageDecl struct {
		Span
		Annotations []*Annotation
		PackagePos  token.Pos
		Name        *Name
	}

	// ImportDecl covers all five forms of §7.5. Static and OnDemand select
	// among the first four; Module marks a SingleModuleImportDeclaration, in
	// which case Name is a ModuleName.
	ImportDecl struct {
		Span
		ImportPos token.Pos
		Static    bool
		Module    bool
		Name      *Name
		OnDemand  bool // `. *`
		StarPos   token.Pos
	}

	// ModuleDecl is a ModuleDeclaration, §7.7.
	ModuleDecl struct {
		Span
		Annotations []*Annotation
		OpenPos     token.Pos // NoPos when not open
		ModulePos   token.Pos
		Name        *Name
		Lbrace      token.Pos
		Directives  []*ModuleDirective
		Rbrace      token.Pos
	}

	// ClassDecl is a NormalClassDeclaration.
	ClassDecl struct {
		Span
		Mods       *Modifiers
		ClassPos   token.Pos
		Name       *Ident
		TypeParams *TypeParams
		Extends    Type
		Implements []Type
		Permits    []Type
		Lbrace     token.Pos
		Members    []Decl
		Rbrace     token.Pos
	}

	// InterfaceDecl is a NormalInterfaceDeclaration.
	InterfaceDecl struct {
		Span
		Mods         *Modifiers
		InterfacePos token.Pos
		Name         *Ident
		TypeParams   *TypeParams
		Extends      []Type
		Permits      []Type
		Lbrace       token.Pos
		Members      []Decl
		Rbrace       token.Pos
	}

	// AnnotationDecl is an AnnotationInterfaceDeclaration. `sealed` and
	// `non-sealed` are syntactically admissible in Mods and rejected later.
	AnnotationDecl struct {
		Span
		Mods         *Modifiers
		AtPos        token.Pos
		InterfacePos token.Pos
		Name         *Ident
		Lbrace       token.Pos
		Members      []Decl
		Rbrace       token.Pos
	}

	// EnumDecl is an EnumDeclaration. Semi is the `;` that opens
	// EnumBodyDeclarations, or NoPos.
	EnumDecl struct {
		Span
		Mods       *Modifiers
		EnumPos    token.Pos
		Name       *Ident
		Implements []Type
		Lbrace     token.Pos
		Constants  []*EnumConstant
		Comma      token.Pos // trailing comma after the constant list, or NoPos
		Semi       token.Pos
		Members    []Decl
		Rbrace     token.Pos
	}

	// RecordDecl is a RecordDeclaration.
	RecordDecl struct {
		Span
		Mods       *Modifiers
		RecordPos  token.Pos
		Name       *Ident
		TypeParams *TypeParams
		Lparen     token.Pos
		Components []*RecordComponent
		Rparen     token.Pos
		Implements []Type
		Lbrace     token.Pos
		Members    []Decl
		Rbrace     token.Pos
	}

	// VarDecl is a FieldDeclaration, a ConstantDeclaration or a
	// LocalVariableDeclaration — the three differ in permitted modifiers and in
	// where they appear, not in shape. Semi is NoPos where the declaration is
	// not a statement (a for-init, a resource).
	VarDecl struct {
		Span
		Mods  *Modifiers
		Type  Type // may be a *VarType
		Names []*VarDeclarator
		Semi  token.Pos
	}

	// MethodDecl is a MethodDeclaration or an InterfaceMethodDeclaration.
	// Result is nil for `void`. Dims is the deprecated trailing-bracket form.
	MethodDecl struct {
		Span
		Mods        *Modifiers
		TypeParams  *TypeParams
		Annotations []*Annotation // between type parameters and the result type
		VoidPos     token.Pos
		Result      Type
		Name        *Ident
		Lparen      token.Pos
		Recv        *ReceiverParam
		Params      []*Param
		Rparen      token.Pos
		Dims        []*Dim
		Throws      []Type
		Body        *Block // nil for an abstract or native method
		Semi        token.Pos
	}

	// ConstructorDecl is a ConstructorDeclaration or, with Compact true, a
	// CompactConstructorDeclaration — which has no parameter list at all.
	ConstructorDecl struct {
		Span
		Mods       *Modifiers
		TypeParams *TypeParams
		Name       *Ident
		Compact    bool
		Lparen     token.Pos
		Recv       *ReceiverParam
		Params     []*Param
		Rparen     token.Pos
		Throws     []Type
		Body       *Block
	}

	// InitializerDecl is an InstanceInitializer or, with Static true, a
	// StaticInitializer.
	InitializerDecl struct {
		Span
		StaticPos token.Pos
		Static    bool
		Body      *Block
	}

	// AnnotationElemDecl is an AnnotationInterfaceElementDeclaration.
	AnnotationElemDecl struct {
		Span
		Mods       *Modifiers
		Type       Type
		Name       *Ident
		Lparen     token.Pos
		Rparen     token.Pos
		Dims       []*Dim
		DefaultPos token.Pos
		Default    Node // ElementValue: Expr, *ElementValueArray or *Annotation
		Semi       token.Pos
	}

	// EmptyDecl is a stray `;` among members. Keeping it costs one node and
	// saves a formatter from inventing one.
	EmptyDecl struct {
		Span
	}

	// BadDecl marks a declaration the parser could not read.
	BadDecl struct {
		Span
	}
)

// ModuleDirective is one directive of a module declaration. Kind selects the
// form and determines which fields are populated:
//
//	requires  → Mods (transitive/static), Name
//	exports   → Name, To
//	opens     → Name, To
//	uses      → Name
//	provides  → Name, With
type ModuleDirective struct {
	Span
	Kind  token.Ctx // CtxRequires, CtxExports, CtxOpens, CtxUses, CtxProvides
	KwPos token.Pos
	Mods  *Modifiers
	Name  *Name
	To    []*Name
	With  []*Name
	Semi  token.Pos
}

// VarDeclarator is one `VariableDeclaratorId [= VariableInitializer]`. Init is
// an Expr or an *ArrayInit.
type VarDeclarator struct {
	Span
	Name   *Ident // Underscore set for the `_` form
	Dims   []*Dim
	Assign token.Pos
	Init   Node
}

// Param is a FormalParameter, a VariableArityParameter, or a lambda parameter.
// A ConciseLambdaParameter has Type nil.
type Param struct {
	Span
	Mods        *Modifiers
	Type        Type // may be a *VarType in a lambda
	Annotations []*Annotation // between the type and `...`
	Ellipsis    token.Pos     // NoPos unless variable arity
	Name        *Ident
	Dims        []*Dim
}

// ReceiverParam is `{Annotation} Type [Identifier .] this`.
type ReceiverParam struct {
	Span
	Annotations []*Annotation
	Type        Type
	Qualifier   *Ident
	ThisPos     token.Pos
}

// RecordComponent is one component of a RecordHeader.
type RecordComponent struct {
	Span
	Annotations    []*Annotation
	Type           Type
	DotAnnotations []*Annotation // between the type and `...`
	Ellipsis       token.Pos
	Name           *Ident
}

// EnumConstant is `{Annotation} Identifier [( Args )] [ClassBody]`.
type EnumConstant struct {
	Span
	Annotations []*Annotation
	Name        *Ident
	Lparen      token.Pos
	Args        []Expr
	Rparen      token.Pos
	Lbrace      token.Pos
	Members     []Decl
	Rbrace      token.Pos
}

func (*PackageDecl) declNode()        {}
func (*ImportDecl) declNode()         {}
func (*ModuleDecl) declNode()         {}
func (*ClassDecl) declNode()          {}
func (*InterfaceDecl) declNode()      {}
func (*AnnotationDecl) declNode()     {}
func (*EnumDecl) declNode()           {}
func (*RecordDecl) declNode()         {}
func (*VarDecl) declNode()            {}
func (*MethodDecl) declNode()         {}
func (*ConstructorDecl) declNode()    {}
func (*InitializerDecl) declNode()    {}
func (*AnnotationElemDecl) declNode() {}
func (*EmptyDecl) declNode()          {}
func (*BadDecl) declNode()            {}