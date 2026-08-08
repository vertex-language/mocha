package parser

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
)

// parseModifiers reads `{ClassModifier}` and its siblings. All modifier lists
// are read with one function: which keywords a given declaration admits is a
// semantic rule, and rejecting `transient` on a method here would only produce
// a worse diagnostic than the phase that knows what a method is.
func (p *parser) parseModifiers() *ast.Modifiers {
	lo := p.pos()
	var list []*ast.Modifier
	for {
		switch {
		case p.at(token.AT) && p.peek(1).Kind != token.INTERFACE:
			a := p.parseAnnotation()
			m := alloc[ast.Modifier](p.arena)
			m.Span = a.Span
			m.Annotation = a
			list = append(list, m)
			continue
		case isModifierKeyword(p.kind()):
			t := p.next()
			m := alloc[ast.Modifier](p.arena)
			m.Span = ast.At(t.Pos, t.End)
			m.Kind = t.Kind
			list = append(list, m)
			continue
		case p.at(token.NON_SEALED), p.atCtx(token.CtxSealed):
			// `sealed` is contextual and `non-sealed` is its own token; both
			// are modifiers only here, which is what makes `sealed` usable as a
			// variable name elsewhere.
			t := p.next()
			m := alloc[ast.Modifier](p.arena)
			m.Span = ast.At(t.Pos, t.End)
			if t.Kind == token.NON_SEALED {
				m.Kind = token.NON_SEALED
			} else {
				m.Kind = token.IDENT // `sealed`; the Ctx is on the token
			}
			list = append(list, m)
			continue
		}
		break
	}
	if len(list) == 0 {
		return nil
	}
	mods := alloc[ast.Modifiers](p.arena)
	mods.List = list
	mods.Span = ast.At(lo, p.prevEnd())
	return mods
}

func isModifierKeyword(k token.Kind) bool {
	switch k {
	case token.PUBLIC, token.PROTECTED, token.PRIVATE, token.ABSTRACT,
		token.STATIC, token.FINAL, token.STRICTFP, token.TRANSIENT,
		token.VOLATILE, token.SYNCHRONIZED, token.NATIVE, token.DEFAULT:
		return true
	}
	return false
}

func (p *parser) parseTopLevelDecl() ast.Decl {
	if t, ok := p.accept(token.SEMICOLON); ok {
		d := alloc[ast.EmptyDecl](p.arena)
		d.Span = ast.At(t.Pos, t.End)
		return d
	}
	return p.parseMember(nil)
}

// parseMember reads one ClassBodyDeclaration, InterfaceMemberDeclaration or
// top-level member. enclosing is the simple name of the enclosing type, needed
// to tell a constructor from a method; nil at the top level, where a compact
// compilation unit has no name and therefore no constructor.
func (p *parser) parseMember(enclosing *ast.Ident) ast.Decl {
	if !p.enter() {
		p.error(p.pos(), p.end(), "declaration nesting too deep")
		p.advanceTo(token.RBRACE)
		return p.badDecl(p.pos())
	}
	defer p.leave()

	lo := p.pos()

	if t, ok := p.accept(token.SEMICOLON); ok {
		d := alloc[ast.EmptyDecl](p.arena)
		d.Span = ast.At(t.Pos, t.End)
		return d
	}

	// A static initializer is `static {`, which no modifier list can precede.
	if p.at(token.STATIC) && p.peek(1).Kind == token.LBRACE {
		d := alloc[ast.InitializerDecl](p.arena)
		d.Static = true
		d.StaticPos = p.next().Pos
		d.Body = p.parseBlock()
		d.Span = ast.At(lo, p.prevEnd())
		return d
	}
	if p.at(token.LBRACE) {
		d := alloc[ast.InitializerDecl](p.arena)
		d.Body = p.parseBlock()
		d.Span = ast.At(lo, p.prevEnd())
		return d
	}

	mods := p.parseModifiers()

	switch {
	case p.at(token.CLASS):
		return p.parseClassDecl(lo, mods)
	case p.at(token.INTERFACE):
		return p.parseInterfaceDecl(lo, mods)
	case p.at(token.AT) && p.peek(1).Kind == token.INTERFACE:
		return p.parseAnnotationDecl(lo, mods)
	case p.at(token.ENUM):
		return p.parseEnumDecl(lo, mods)
	case p.atRecordDecl():
		return p.parseRecordDecl(lo, mods)
	}

	// A compact constructor is `Name {` with no parameter list at all.
	if enclosing != nil && p.at(token.IDENT) && p.peek(1).Kind == token.LBRACE &&
		p.sameName(p.tok(), enclosing) {
		d := alloc[ast.ConstructorDecl](p.arena)
		d.Mods = mods
		d.Compact = true
		d.Name = p.parseIdent()
		d.Body = p.parseBlock()
		d.Span = ast.At(lo, p.prevEnd())
		return d
	}

	tparams := p.parseTypeParams()

	// A constructor is `Name (` with the enclosing type's name and no result.
	if enclosing != nil && p.at(token.IDENT) && p.peek(1).Kind == token.LPAREN &&
		p.sameName(p.tok(), enclosing) {
		d := alloc[ast.ConstructorDecl](p.arena)
		d.Mods = mods
		d.TypeParams = tparams
		d.Name = p.parseIdent()
		d.Lparen, d.Recv, d.Params, d.Rparen = p.parseParams()
		if _, ok := p.accept(token.THROWS); ok {
			d.Throws = p.parseTypeList()
		}
		d.Body = p.parseBlock()
		d.Span = ast.At(lo, p.prevEnd())
		return d
	}

	// Everything left is a method or a field: annotations between the type
	// parameters and the result type, then `void` or a type, then a name.
	midAnns := p.parseAnnotations()

	var result ast.Type
	var voidPos token.Pos
	if t, ok := p.accept(token.VOID); ok {
		voidPos = t.Pos
	} else {
		result = p.parseType()
	}

	if !p.at(token.IDENT) {
		p.errorExpected("member name")
		p.advanceTo(token.SEMICOLON, token.RBRACE)
		p.accept(token.SEMICOLON)
		return p.badDecl(lo)
	}

	// An annotation interface element is `Name ( )`; a method is `Name (` with
	// a parameter list that may be empty but is followed by more than `)`.
	if p.peek(1).Kind == token.LPAREN {
		return p.parseMethodOrElement(lo, mods, tparams, midAnns, voidPos, result)
	}

	d := alloc[ast.VarDecl](p.arena)
	d.Mods = mods
	d.Type = result
	d.Names = p.parseVarDeclarators()
	d.Semi = p.expectSemi()
	d.Span = ast.At(lo, p.prevEnd())
	return d
}

func (p *parser) parseMethodOrElement(
	lo token.Pos, mods *ast.Modifiers, tparams *ast.TypeParams,
	anns []*ast.Annotation, voidPos token.Pos, result ast.Type,
) ast.Decl {
	name := p.parseMethodIdent()
	lparen := p.expect(token.LPAREN)

	// `Name ( )` followed by `default`, `;` or `[` is an annotation element.
	if p.at(token.RPAREN) {
		save := p.i
		rparen := p.next().Pos
		dims := p.parseDims()
		if p.at(token.DEFAULT) || p.at(token.SEMICOLON) {
			d := alloc[ast.AnnotationElemDecl](p.arena)
			d.Mods = mods
			d.Type = result
			d.Name = name
			d.Lparen = lparen
			d.Rparen = rparen
			d.Dims = dims
			if t, ok := p.accept(token.DEFAULT); ok {
				d.DefaultPos = t.Pos
				d.Default = p.parseElementValue()
			}
			d.Semi = p.expectSemi()
			d.Span = ast.At(lo, p.prevEnd())
			return d
		}
		p.i = save
	}

	d := alloc[ast.MethodDecl](p.arena)
	d.Mods = mods
	d.TypeParams = tparams
	d.Annotations = anns
	d.VoidPos = voidPos
	d.Result = result
	d.Name = name
	d.Lparen = lparen
	_, d.Recv, d.Params, d.Rparen = p.parseParamsAfterLparen(lparen)
	d.Dims = p.parseDims()
	if _, ok := p.accept(token.THROWS); ok {
		d.Throws = p.parseTypeList()
	}
	if p.at(token.LBRACE) {
		d.Body = p.parseBlock()
	} else {
		d.Semi = p.expectSemi()
	}
	d.Span = ast.At(lo, p.prevEnd())
	return d
}

// atRecordDecl distinguishes a record declaration from a variable named
// `record`. The keyword is contextual, so `record Point(int x, int y)` is a
// declaration and `record = 3;` is an assignment.
func (p *parser) atRecordDecl() bool {
	if !p.atCtx(token.CtxRecord) {
		return false
	}
	if p.peek(1).Kind != token.IDENT {
		return false
	}
	k := p.peek(2).Kind
	return k == token.LPAREN || k == token.LSS
}

func (p *parser) sameName(t token.Token, id *ast.Ident) bool {
	return p.f.Slice(t.Pos, t.End) == p.f.Slice(id.Lo, id.Hi)
}

// --- type declarations ------------------------------------------------------

func (p *parser) parseClassDecl(lo token.Pos, mods *ast.Modifiers) ast.Decl {
	d := alloc[ast.ClassDecl](p.arena)
	d.Mods = mods
	d.ClassPos = p.expect(token.CLASS)
	d.Name = p.parseTypeIdent()
	d.TypeParams = p.parseTypeParams()
	if _, ok := p.accept(token.EXTENDS); ok {
		d.Extends = p.parseType()
	}
	if _, ok := p.accept(token.IMPLEMENTS); ok {
		d.Implements = p.parseTypeList()
	}
	if _, ok := p.acceptCtx(token.CtxPermits); ok {
		d.Permits = p.parseTypeList()
	}
	d.Lbrace, d.Members, d.Rbrace = p.parseTypeBody(d.Name)
	d.Span = ast.At(lo, p.prevEnd())
	return d
}

func (p *parser) parseInterfaceDecl(lo token.Pos, mods *ast.Modifiers) ast.Decl {
	d := alloc[ast.InterfaceDecl](p.arena)
	d.Mods = mods
	d.InterfacePos = p.expect(token.INTERFACE)
	d.Name = p.parseTypeIdent()
	d.TypeParams = p.parseTypeParams()
	if _, ok := p.accept(token.EXTENDS); ok {
		d.Extends = p.parseTypeList()
	}
	if _, ok := p.acceptCtx(token.CtxPermits); ok {
		d.Permits = p.parseTypeList()
	}
	// An interface has no constructors, so no enclosing name is passed down.
	d.Lbrace, d.Members, d.Rbrace = p.parseTypeBody(nil)
	d.Span = ast.At(lo, p.prevEnd())
	return d
}

func (p *parser) parseAnnotationDecl(lo token.Pos, mods *ast.Modifiers) ast.Decl {
	d := alloc[ast.AnnotationDecl](p.arena)
	d.Mods = mods
	d.AtPos = p.expect(token.AT)
	d.InterfacePos = p.expect(token.INTERFACE)
	d.Name = p.parseTypeIdent()
	d.Lbrace, d.Members, d.Rbrace = p.parseTypeBody(nil)
	d.Span = ast.At(lo, p.prevEnd())
	return d
}

func (p *parser) parseRecordDecl(lo token.Pos, mods *ast.Modifiers) ast.Decl {
	d := alloc[ast.RecordDecl](p.arena)
	d.Mods = mods
	t, _ := p.acceptCtx(token.CtxRecord)
	d.RecordPos = t.Pos
	d.Name = p.parseTypeIdent()
	d.TypeParams = p.parseTypeParams()

	d.Lparen = p.expect(token.LPAREN)
	for !p.at(token.RPAREN) && !p.atEOF() {
		d.Components = append(d.Components, p.parseRecordComponent())
		if _, ok := p.accept(token.COMMA); !ok {
			break
		}
	}
	d.Rparen = p.expect(token.RPAREN)

	if _, ok := p.accept(token.IMPLEMENTS); ok {
		d.Implements = p.parseTypeList()
	}
	d.Lbrace, d.Members, d.Rbrace = p.parseTypeBody(d.Name)
	d.Span = ast.At(lo, p.prevEnd())
	return d
}

func (p *parser) parseRecordComponent() *ast.RecordComponent {
	lo := p.pos()
	c := alloc[ast.RecordComponent](p.arena)
	c.Annotations = p.parseAnnotations()
	c.Type = p.parseType()
	c.DotAnnotations = p.parseAnnotations()
	if t, ok := p.accept(token.ELLIPSIS); ok {
		c.Ellipsis = t.Pos
	}
	c.Name = p.parseIdent()
	c.Span = ast.At(lo, p.prevEnd())
	return c
}

func (p *parser) parseEnumDecl(lo token.Pos, mods *ast.Modifiers) ast.Decl {
	d := alloc[ast.EnumDecl](p.arena)
	d.Mods = mods
	d.EnumPos = p.expect(token.ENUM)
	d.Name = p.parseTypeIdent()
	if _, ok := p.accept(token.IMPLEMENTS); ok {
		d.Implements = p.parseTypeList()
	}
	d.Lbrace = p.expect(token.LBRACE)

	for !p.at(token.RBRACE) && !p.at(token.SEMICOLON) && !p.atEOF() {
		d.Constants = append(d.Constants, p.parseEnumConstant(d.Name))
		t, ok := p.accept(token.COMMA)
		if !ok {
			break
		}
		if p.at(token.RBRACE) || p.at(token.SEMICOLON) {
			d.Comma = t.Pos
		}
	}
	if t, ok := p.accept(token.SEMICOLON); ok {
		d.Semi = t.Pos
		for !p.at(token.RBRACE) && !p.atEOF() {
			d.Members = append(d.Members, p.parseMember(d.Name))
		}
	}
	d.Rbrace = p.expect(token.RBRACE)
	d.Span = ast.At(lo, p.prevEnd())
	return d
}

// parseEnumConstant reads one constant. A trailing class body declares a class
// the grammar does not call a ClassDeclaration.
func (p *parser) parseEnumConstant(enclosing *ast.Ident) *ast.EnumConstant {
	lo := p.pos()
	c := alloc[ast.EnumConstant](p.arena)
	c.Annotations = p.parseAnnotations()
	c.Name = p.parseIdent()
	if t, ok := p.accept(token.LPAREN); ok {
		c.Lparen = t.Pos
		c.Args = p.parseArgs()
		c.Rparen = p.expect(token.RPAREN)
	}
	if p.at(token.LBRACE) {
		c.Lbrace, c.Members, c.Rbrace = p.parseTypeBody(enclosing)
	}
	c.Span = ast.At(lo, p.prevEnd())
	return c
}

// parseTypeBody reads `{ {member} }`. Under HeaderOnly the body is skipped
// balanced rather than parsed.
func (p *parser) parseTypeBody(enclosing *ast.Ident) (token.Pos, []ast.Decl, token.Pos) {
	if p.mode&HeaderOnly != 0 && p.at(token.LBRACE) {
		lb := p.pos()
		p.skipBalanced()
		return lb, nil, p.prevEnd()
	}
	lb := p.expect(token.LBRACE)
	var members []ast.Decl
	for !p.at(token.RBRACE) && !p.atEOF() {
		before := p.i
		m := p.parseMember(enclosing)
		if m != nil {
			members = append(members, m)
		}
		if p.i == before { // no progress: recover rather than spin
			p.advanceTo(token.SEMICOLON, token.RBRACE)
			p.accept(token.SEMICOLON)
		}
	}
	rb := p.expect(token.RBRACE)
	return lb, members, rb
}

// --- parameters and declarators ---------------------------------------------

func (p *parser) parseParams() (token.Pos, *ast.ReceiverParam, []*ast.Param, token.Pos) {
	lp := p.expect(token.LPAREN)
	return p.parseParamsAfterLparen(lp)
}

func (p *parser) parseParamsAfterLparen(lp token.Pos) (token.Pos, *ast.ReceiverParam, []*ast.Param, token.Pos) {
	var recv *ast.ReceiverParam
	var params []*ast.Param

	if !p.at(token.RPAREN) {
		recv = p.tryReceiverParam()
		if recv != nil {
			p.accept(token.COMMA)
		}
		for !p.at(token.RPAREN) && !p.atEOF() {
			params = append(params, p.parseFormalParam())
			if _, ok := p.accept(token.COMMA); !ok {
				break
			}
		}
	}
	rp := p.expect(token.RPAREN)
	return lp, recv, params, rp
}

// tryReceiverParam speculates: a receiver parameter ends in `this`, which no
// formal parameter can, but nothing before that distinguishes them.
func (p *parser) tryReceiverParam() *ast.ReceiverParam {
	r, _ := spec(p, func() (*ast.ReceiverParam, bool) {
		lo := p.pos()
		anns := p.parseAnnotations()
		if isPrimitiveKind(p.kind()) || p.at(token.IDENT) {
			// fall through
		} else {
			return nil, false
		}
		typ := p.parseType()
		rp := alloc[ast.ReceiverParam](p.arena)
		rp.Annotations = anns
		rp.Type = typ
		if p.at(token.IDENT) && p.peek(1).Kind == token.PERIOD && p.peek(2).Kind == token.THIS {
			rp.Qualifier = p.parseIdent()
			p.next() // .
		}
		t, ok := p.accept(token.THIS)
		if !ok {
			return nil, false
		}
		rp.ThisPos = t.Pos
		rp.Span = ast.At(lo, p.prevEnd())
		return rp, true
	})
	return r
}

func (p *parser) parseFormalParam() *ast.Param {
	lo := p.pos()
	prm := alloc[ast.Param](p.arena)
	prm.Mods = p.parseModifiers()
	prm.Type = p.parseType()
	prm.Annotations = p.parseAnnotations()
	if t, ok := p.accept(token.ELLIPSIS); ok {
		prm.Ellipsis = t.Pos
	}
	prm.Name = p.parseVarDeclaratorId()
	prm.Dims = p.parseDims()
	prm.Span = ast.At(lo, p.prevEnd())
	return prm
}

func (p *parser) parseVarDeclarators() []*ast.VarDeclarator {
	var list []*ast.VarDeclarator
	for {
		lo := p.pos()
		v := alloc[ast.VarDeclarator](p.arena)
		v.Name = p.parseVarDeclaratorId()
		v.Dims = p.parseDims()
		if t, ok := p.accept(token.ASSIGN); ok {
			v.Assign = t.Pos
			v.Init = p.parseVarInit()
		}
		v.Span = ast.At(lo, p.prevEnd())
		list = append(list, v)
		if _, ok := p.accept(token.COMMA); !ok {
			return list
		}
	}
}

// parseVarInit reads a VariableInitializer: an expression or an array
// initializer.
func (p *parser) parseVarInit() ast.Node {
	if p.at(token.LBRACE) {
		return p.parseArrayInit()
	}
	return p.parseExpr()
}

func (p *parser) parseArrayInit() *ast.ArrayInit {
	a := alloc[ast.ArrayInit](p.arena)
	a.Lbrace = p.expect(token.LBRACE)
	for !p.at(token.RBRACE) && !p.atEOF() {
		a.Elts = append(a.Elts, p.parseVarInit())
		t, ok := p.accept(token.COMMA)
		if !ok {
			break
		}
		if p.at(token.RBRACE) {
			a.Comma = t.Pos
		}
	}
	a.Rbrace = p.expect(token.RBRACE)
	a.Span = ast.At(a.Lbrace, p.prevEnd())
	return a
}