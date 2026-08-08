package parser

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
)

// parseExpr reads an Expression: a lambda or an AssignmentExpression.
func (p *parser) parseExpr() ast.Expr {
	if !p.enter() {
		p.error(p.pos(), p.end(), "expression nesting too deep")
		return p.badExpr(p.pos())
	}
	defer p.leave()

	if lam, ok := p.tryLambda(); ok {
		return lam
	}
	return p.parseAssign()
}

// parseAssign reads an AssignmentExpression. Assignment is right-associative,
// and its right side is a full Expression — so a lambda may appear there.
func (p *parser) parseAssign() ast.Expr {
	lo := p.pos()
	x := p.parseConditional()

	op, opEnd, n := p.assignOp()
	if n == 0 {
		return x
	}
	opPos := p.pos()
	p.i += n

	a := alloc[ast.AssignExpr](p.arena)
	a.LHS = x
	a.Op = op
	a.OpPos = opPos
	a.OpEnd = opEnd
	a.RHS = p.parseExpr()
	a.Span = ast.At(lo, p.prevEnd())
	return a
}

// assignOp recognizes an AssignmentOperator, including the two spelled as
// separate adjacent tokens. It returns the operator, the end of its last token,
// and how many tokens it spans.
func (p *parser) assignOp() (token.Kind, token.Pos, int) {
	switch k := p.kind(); k {
	case token.ASSIGN, token.MUL_ASSIGN, token.QUO_ASSIGN, token.REM_ASSIGN,
		token.ADD_ASSIGN, token.SUB_ASSIGN, token.SHL_ASSIGN,
		token.AND_ASSIGN, token.XOR_ASSIGN, token.OR_ASSIGN:
		return k, p.end(), 1
	case token.GTR:
		// `> >=` and `> > >=`. The scanner split them; adjacency is what makes
		// rejoining them legal, and token.Join checks it.
		if joined, n := token.Join(p.toks[p.i:]); n > 0 {
			switch joined {
			case token.SHR_ASSIGN, token.USHR_ASSIGN:
				return joined, p.peek(n - 1).End, n
			}
		}
	}
	return token.ILLEGAL, token.NoPos, 0
}

// parseConditional reads a ConditionalExpression. The `? :` operator is
// right-associative and its else branch may be a lambda.
func (p *parser) parseConditional() ast.Expr {
	lo := p.pos()
	cond := p.parseBinary(1)

	q, ok := p.accept(token.QUESTION)
	if !ok {
		return cond
	}
	c := alloc[ast.CondExpr](p.arena)
	c.Cond = cond
	c.Question = q.Pos
	c.Then = p.parseExpr()
	c.Colon = p.expect(token.COLON)
	if lam, ok := p.tryLambda(); ok {
		c.Else = lam
	} else {
		c.Else = p.parseConditional()
	}
	c.Span = ast.At(lo, p.prevEnd())
	return c
}

// parseBinary is precedence climbing over the left-associative levels.
//
// Two escapes, both forced by the language rather than by the algorithm:
// `instanceof` takes a type or a pattern on its right instead of an operand,
// and `>` may be the head of a joined shift operator rather than a comparison.
func (p *parser) parseBinary(minPrec int) ast.Expr {
	if !p.enter() {
		p.error(p.pos(), p.end(), "expression nesting too deep")
		return p.badExpr(p.pos())
	}
	defer p.leave()

	lo := p.pos()
	x := p.parseUnary()

	for {
		if p.at(token.INSTANCEOF) {
			if token.INSTANCEOF.Precedence() < minPrec {
				return x
			}
			opPos := p.next().Pos
			io := alloc[ast.InstanceOfExpr](p.arena)
			io.X = x
			io.OpPos = opPos
			if pat, ok := p.tryPattern(); ok {
				io.Pattern = pat
			} else {
				io.Type = p.parseType()
			}
			io.Span = ast.At(lo, p.prevEnd())
			x = io
			continue
		}

		op, opEnd, n := p.binaryOp()
		if n == 0 || op.Precedence() < minPrec {
			return x
		}
		opPos := p.pos()
		p.i += n

		b := alloc[ast.BinaryExpr](p.arena)
		b.X = x
		b.Op = op
		b.OpPos = opPos
		b.OpEnd = opEnd
		b.Y = p.parseBinary(op.Precedence() + 1)
		b.Span = ast.At(lo, p.prevEnd())
		x = b
	}
}

// binaryOp recognizes a binary operator at the cursor, joining `> >` and
// `> > >` where they are adjacent.
//
// The greedy join in token.Join is what makes `a >>> b` a single unsigned
// shift; a non-adjacent `a > > b` joins nothing and is left to fail as a
// comparison against a comparison, which is the error the user wants to see.
func (p *parser) binaryOp() (token.Kind, token.Pos, int) {
	k := p.kind()
	if k == token.GTR {
		if joined, n := token.Join(p.toks[p.i:]); n > 0 {
			switch joined {
			case token.SHR, token.USHR:
				return joined, p.peek(n - 1).End, n
			case token.SHR_ASSIGN, token.USHR_ASSIGN:
				return token.ILLEGAL, token.NoPos, 0 // an assignment, not a binary op
			}
		}
	}
	if k.Precedence() == token.LowestPrec {
		return token.ILLEGAL, token.NoPos, 0
	}
	// A lone `&` and `|` reach here only in expression position; the catch-type
	// and type-bound uses are consumed by their own productions.
	return k, p.end(), 1
}

func (p *parser) parseUnary() ast.Expr {
	lo := p.pos()
	switch k := p.kind(); k {
	case token.ADD, token.SUB, token.TILDE, token.NOT, token.INC, token.DEC:
		opPos := p.next().Pos
		u := alloc[ast.UnaryExpr](p.arena)
		u.Op = k
		u.OpPos = opPos
		u.X = p.parseUnary()
		u.Span = ast.At(lo, p.prevEnd())
		return u
	case token.LPAREN:
		if c, ok := p.tryCast(); ok {
			return c
		}
	case token.SWITCH:
		s := alloc[ast.SwitchExpr](p.arena)
		s.SwitchPos = p.next().Pos
		p.expect(token.LPAREN)
		s.Tag = p.parseExpr()
		p.expect(token.RPAREN)
		s.Block = p.parseSwitchBlock()
		s.Span = ast.At(lo, p.prevEnd())
		return s
	}
	return p.parsePostfix()
}

// tryCast speculates on a CastExpression. A `(` may open a cast, a lambda
// parameter list, or a parenthesized expression, and only what follows the
// matching `)` tells them apart.
func (p *parser) tryCast() (ast.Expr, bool) {
	return spec(p, func() (ast.Expr, bool) {
		lo := p.pos()
		lp := p.next().Pos

		if !isPrimitiveKind(p.kind()) && !p.at(token.IDENT) && !p.at(token.AT) {
			return nil, false
		}
		primitive := isPrimitiveKind(p.kind())
		typ := p.parseType()
		if _, bad := typ.(*ast.BadType); bad {
			return nil, false
		}
		var bounds []ast.Type
		for {
			if _, ok := p.accept(token.AND); !ok {
				break
			}
			bounds = append(bounds, p.parseType())
			primitive = false // an intersection cast is always a reference cast
		}
		if !p.at(token.RPAREN) {
			return nil, false
		}
		rp := p.next().Pos

		// A cast to a primitive type takes any UnaryExpression, so `(int) -x`
		// is a cast. A cast to a reference type takes a
		// UnaryExpressionNotPlusMinus, so `(a) - b` is a subtraction.
		if !p.startsCastOperand(primitive) {
			return nil, false
		}

		c := alloc[ast.CastExpr](p.arena)
		c.Lparen = lp
		c.Type = typ
		c.Bounds = bounds
		c.Rparen = rp
		if primitive {
			c.X = p.parseUnary()
		} else if lam, ok := p.tryLambda(); ok {
			c.X = lam
		} else {
			c.X = p.parseUnaryNotPlusMinus()
		}
		c.Span = ast.At(lo, p.prevEnd())
		return c, true
	})
}

func (p *parser) startsCastOperand(primitive bool) bool {
	switch p.kind() {
	case token.ADD, token.SUB:
		return primitive
	case token.INC, token.DEC, token.TILDE, token.NOT, token.LPAREN,
		token.IDENT, token.THIS, token.SUPER, token.NEW, token.SWITCH,
		token.UNDERSCORE:
		return true
	}
	return isPrimitiveKind(p.kind()) || p.tok().Kind.IsLiteral()
}

func (p *parser) parseUnaryNotPlusMinus() ast.Expr {
	lo := p.pos()
	switch k := p.kind(); k {
	case token.TILDE, token.NOT:
		opPos := p.next().Pos
		u := alloc[ast.UnaryExpr](p.arena)
		u.Op = k
		u.OpPos = opPos
		u.X = p.parseUnary()
		u.Span = ast.At(lo, p.prevEnd())
		return u
	}
	return p.parseUnary()
}

// tryLambda speculates on a LambdaExpression. The concise single-parameter form
// needs one token of lookahead; the parenthesized form needs a full parameter
// list followed by `->`.
func (p *parser) tryLambda() (ast.Expr, bool) {
	if (p.at(token.IDENT) || p.at(token.UNDERSCORE)) && p.peek(1).Kind == token.ARROW {
		lo := p.pos()
		l := alloc[ast.LambdaExpr](p.arena)
		prm := alloc[ast.Param](p.arena)
		prm.Name = p.parseVarDeclaratorId()
		prm.Span = prm.Name.Span
		l.Params = []*ast.Param{prm}
		l.Arrow = p.expect(token.ARROW)
		l.Body = p.parseLambdaBody()
		l.Span = ast.At(lo, p.prevEnd())
		return l, true
	}
	if !p.at(token.LPAREN) {
		return nil, false
	}
	return spec(p, func() (ast.Expr, bool) {
		lo := p.pos()
		l := alloc[ast.LambdaExpr](p.arena)
		l.Paren = true
		l.Lparen = p.next().Pos

		for !p.at(token.RPAREN) && !p.atEOF() {
			prm, ok := p.tryLambdaParam()
			if !ok {
				return nil, false
			}
			l.Params = append(l.Params, prm)
			if _, ok := p.accept(token.COMMA); !ok {
				break
			}
		}
		if !p.at(token.RPAREN) {
			return nil, false
		}
		l.Rparen = p.next().Pos
		if !p.at(token.ARROW) {
			return nil, false
		}
		l.Arrow = p.next().Pos
		l.Body = p.parseLambdaBody()
		l.Span = ast.At(lo, p.prevEnd())
		return l, true
	})
}

// tryLambdaParam reads one parameter of a parenthesized lambda. The concise and
// normal forms cannot be mixed, but that is a semantic rule; here both shapes
// produce an ast.Param and Type is nil for the concise one.
func (p *parser) tryLambdaParam() (*ast.Param, bool) {
	lo := p.pos()
	if (p.at(token.IDENT) || p.at(token.UNDERSCORE)) &&
		(p.peek(1).Kind == token.COMMA || p.peek(1).Kind == token.RPAREN) {
		prm := alloc[ast.Param](p.arena)
		prm.Name = p.parseVarDeclaratorId()
		prm.Span = ast.At(lo, p.prevEnd())
		return prm, true
	}
	prm := alloc[ast.Param](p.arena)
	prm.Mods = p.parseModifiers()
	if p.atCtx(token.CtxVar) {
		t := p.next()
		vt := alloc[ast.VarType](p.arena)
		vt.Span = ast.At(t.Pos, t.End)
		prm.Type = vt
	} else {
		if !isPrimitiveKind(p.kind()) && !p.at(token.IDENT) {
			return nil, false
		}
		prm.Type = p.parseType()
		if _, bad := prm.Type.(*ast.BadType); bad {
			return nil, false
		}
	}
	prm.Annotations = p.parseAnnotations()
	if t, ok := p.accept(token.ELLIPSIS); ok {
		prm.Ellipsis = t.Pos
	}
	if !p.at(token.IDENT) && !p.at(token.UNDERSCORE) {
		return nil, false
	}
	prm.Name = p.parseVarDeclaratorId()
	prm.Dims = p.parseDims()
	prm.Span = ast.At(lo, p.prevEnd())
	return prm, true
}

// parseLambdaBody reads an Expression or a Block. The expression form extends
// maximally: `x -> a ? b : c` has the whole conditional as its body.
func (p *parser) parseLambdaBody() ast.Node {
	if p.at(token.LBRACE) {
		return p.parseBlock()
	}
	return p.parseExpr()
}

// --- postfix and primary ----------------------------------------------------

func (p *parser) parsePostfix() ast.Expr {
	lo := p.pos()
	x := p.parsePrimary()

	for {
		switch p.kind() {
		case token.PERIOD:
			x = p.parseSelector(lo, x)

		case token.LBRACK:
			lb := p.next().Pos
			ix := alloc[ast.IndexExpr](p.arena)
			ix.X = x
			ix.Lbrack = lb
			ix.Index = p.parseExpr()
			ix.Rbrack = p.expect(token.RBRACK)
			ix.Span = ast.At(lo, p.prevEnd())
			x = ix

		case token.COLONCOLON:
			cp := p.next().Pos
			mr := alloc[ast.MethodRef](p.arena)
			mr.X = x
			mr.ColonPos = cp
			mr.TypeArgs = p.tryTypeArgs()
			if t, ok := p.accept(token.NEW); ok {
				mr.NewPos = t.Pos
			} else {
				mr.Name = p.parseIdent()
			}
			mr.Span = ast.At(lo, p.prevEnd())
			x = mr

		case token.INC, token.DEC:
			t := p.next()
			pe := alloc[ast.PostfixExpr](p.arena)
			pe.X = x
			pe.Op = t.Kind
			pe.OpPos = t.Pos
			pe.Span = ast.At(lo, t.End)
			x = pe

		default:
			return x
		}
	}
}

// parseSelector handles everything after a `.`: a field or method, `this`,
// `class`, `new`, `super`, and an explicitly typed method invocation.
func (p *parser) parseSelector(lo token.Pos, x ast.Expr) ast.Expr {
	dot := p.next().Pos

	switch {
	case p.at(token.THIS):
		t := p.next()
		th := alloc[ast.This](p.arena)
		if n, ok := x.(*ast.Name); ok {
			th.Qualifier = n
		}
		th.ThisPos = t.Pos
		th.Span = ast.At(lo, t.End)
		return th

	case p.at(token.CLASS):
		t := p.next()
		cl := alloc[ast.ClassLit](p.arena)
		cl.ClassPos = t.Pos
		cl.Span = ast.At(lo, t.End)
		return cl

	case p.at(token.NEW):
		inner := p.parseNewExpr(p.pos())
		if ne, ok := inner.(*ast.NewExpr); ok {
			ne.Outer = x
			ne.Span = ast.At(lo, p.prevEnd())
		}
		return inner

	case p.at(token.SUPER):
		t := p.next()
		sup := alloc[ast.Super](p.arena)
		if n, ok := x.(*ast.Name); ok {
			sup.Qualifier = n
		}
		sup.SuperPos = t.Pos
		sup.Span = ast.At(lo, t.End)
		return sup

	case p.at(token.LSS):
		// `X . <T> m(...)`: explicit type arguments on an invocation.
		ta := p.tryTypeArgs()
		c := alloc[ast.CallExpr](p.arena)
		c.X = x
		c.TypeArgs = ta
		c.Fun = p.parseMethodIdent()
		c.Lparen = p.expect(token.LPAREN)
		c.Args = p.parseArgs()
		c.Rparen = p.expect(token.RPAREN)
		c.Span = ast.At(lo, p.prevEnd())
		return c
	}

	sel := p.parseIdent()
	if p.at(token.LPAREN) {
		c := alloc[ast.CallExpr](p.arena)
		c.X = x
		c.Fun = sel
		c.Lparen = p.next().Pos
		c.Args = p.parseArgs()
		c.Rparen = p.expect(token.RPAREN)
		c.Span = ast.At(lo, p.prevEnd())
		return c
	}

	// A dotted name stays a name for as long as it can. Resolution decides
	// whether `a.b.c` is a package, a type or a field chain; throwing that away
	// into SelectorExpr chains here would discard what the parser knew.
	if n, ok := x.(*ast.Name); ok {
		n.Parts = append(n.Parts, sel)
		n.Span = ast.At(lo, p.prevEnd())
		return n
	}
	se := alloc[ast.SelectorExpr](p.arena)
	se.X = x
	se.Dot = dot
	se.Sel = sel
	se.Span = ast.At(lo, p.prevEnd())
	return se
}

func (p *parser) parsePrimary() ast.Expr {
	lo := p.pos()

	switch k := p.kind(); {
	case k.IsLiteral():
		t := p.next()
		lit := alloc[ast.BasicLit](p.arena)
		lit.Span = ast.At(t.Pos, t.End)
		lit.Kind = t.Kind
		return lit

	case k == token.THIS:
		t := p.next()
		th := alloc[ast.This](p.arena)
		th.ThisPos = t.Pos
		th.Span = ast.At(t.Pos, t.End)
		return th

	case k == token.SUPER:
		t := p.next()
		sup := alloc[ast.Super](p.arena)
		sup.SuperPos = t.Pos
		sup.Span = ast.At(t.Pos, t.End)
		return sup

	case k == token.NEW:
		return p.parseNewExpr(lo)

	case k == token.VOID:
		// `void.class` is the only expression `void` can begin.
		vp := p.next().Pos
		cl := alloc[ast.ClassLit](p.arena)
		cl.VoidPos = vp
		p.expect(token.PERIOD)
		cl.ClassPos = p.expect(token.CLASS)
		cl.Span = ast.At(lo, p.prevEnd())
		return cl

	case isPrimitiveKind(k):
		// `int.class`, `int[].class`, `int[]::new`.
		typ := p.parseType()
		if p.at(token.COLONCOLON) {
			cp := p.next().Pos
			mr := alloc[ast.MethodRef](p.arena)
			mr.X = typ
			mr.ColonPos = cp
			mr.NewPos = p.expect(token.NEW)
			mr.Span = ast.At(lo, p.prevEnd())
			return mr
		}
		cl := alloc[ast.ClassLit](p.arena)
		cl.Type = typ
		p.expect(token.PERIOD)
		cl.ClassPos = p.expect(token.CLASS)
		cl.Span = ast.At(lo, p.prevEnd())
		return cl

	case k == token.LPAREN:
		lp := p.next().Pos
		pe := alloc[ast.ParenExpr](p.arena)
		pe.Lparen = lp
		pe.X = p.parseExpr()
		pe.Rparen = p.expect(token.RPAREN)
		pe.Span = ast.At(lo, p.prevEnd())
		return pe

	case k == token.IDENT:
		return p.parseNameOrCall(lo)
	}

	p.errorExpected("expression")
	p.next() // always advance: an expression that consumed nothing loops
	return p.badExpr(lo)
}

// parseNameOrCall reads an identifier-headed primary: a bare method
// invocation, a name, a generic type's `.class` literal, or a method reference
// on a parameterized type.
func (p *parser) parseNameOrCall(lo token.Pos) ast.Expr {
	if p.peek(1).Kind == token.LPAREN {
		c := alloc[ast.CallExpr](p.arena)
		c.Fun = p.parseMethodIdent()
		c.Lparen = p.next().Pos
		c.Args = p.parseArgs()
		c.Rparen = p.expect(token.RPAREN)
		c.Span = ast.At(lo, p.prevEnd())
		return c
	}

	// A `<` after a name may open type arguments on a method reference or a
	// class literal's type. Speculate; a failed attempt leaves a less-than.
	if x, ok := spec(p, func() (ast.Expr, bool) {
		if p.peek(1).Kind != token.LSS && p.peek(1).Kind != token.LBRACK {
			return nil, false
		}
		typ := p.parseType()
		if _, bad := typ.(*ast.BadType); bad {
			return nil, false
		}
		switch {
		case p.at(token.COLONCOLON):
			cp := p.next().Pos
			mr := alloc[ast.MethodRef](p.arena)
			mr.X = typ
			mr.ColonPos = cp
			mr.TypeArgs = p.tryTypeArgs()
			if t, ok := p.accept(token.NEW); ok {
				mr.NewPos = t.Pos
			} else if p.at(token.IDENT) {
				mr.Name = p.parseIdent()
			} else {
				return nil, false
			}
			mr.Span = ast.At(lo, p.prevEnd())
			return mr, true
		case p.at(token.PERIOD) && p.peek(1).Kind == token.CLASS:
			p.next()
			cl := alloc[ast.ClassLit](p.arena)
			cl.Type = typ
			cl.ClassPos = p.next().Pos
			cl.Span = ast.At(lo, p.prevEnd())
			return cl, true
		}
		return nil, false
	}); ok {
		return x
	}

	return p.parseName()
}

func (p *parser) parseNewExpr(lo token.Pos) ast.Expr {
	newPos := p.expect(token.NEW)
	ta := p.tryTypeArgs()

	var elt ast.Type
	if isPrimitiveKind(p.kind()) {
		t := p.next()
		pt := alloc[ast.PrimitiveType](p.arena)
		pt.Span = ast.At(t.Pos, t.End)
		pt.Kind = t.Kind
		pt.KwPos = t.Pos
		elt = pt
	} else {
		elt = p.parseNamedTypeToInstantiate()
	}

	// An array creation is `[` followed by an expression (a DimExpr) or by `]`
	// (a Dims, which must then be followed by an initializer).
	if p.at(token.LBRACK) || (p.at(token.AT) && p.peek(1).Kind != token.INTERFACE) {
		na := alloc[ast.NewArrayExpr](p.arena)
		na.NewPos = newPos
		na.Elt = elt
		for {
			save := p.i
			dlo := p.pos()
			anns := p.parseAnnotations()
			if !p.at(token.LBRACK) {
				p.i = save
				break
			}
			lb := p.next().Pos
			if p.at(token.RBRACK) {
				p.i = save
				break // the rest is a Dims
			}
			de := alloc[ast.DimExpr](p.arena)
			de.Annotations = anns
			de.Lbrack = lb
			de.X = p.parseExpr()
			de.Rbrack = p.expect(token.RBRACK)
			de.Span = ast.At(dlo, p.prevEnd())
			na.DimExprs = append(na.DimExprs, de)
		}
		na.Dims = p.parseDims()
		if len(na.DimExprs) == 0 {
			na.Init = p.parseArrayInit()
		}
		na.Span = ast.At(lo, p.prevEnd())
		return na
	}

	ne := alloc[ast.NewExpr](p.arena)
	ne.NewPos = newPos
	ne.TypeArgs = ta
	if nt, ok := elt.(*ast.NamedType); ok {
		ne.Type = nt
	}
	ne.Lparen = p.expect(token.LPAREN)
	ne.Args = p.parseArgs()
	ne.Rparen = p.expect(token.RPAREN)
	if p.at(token.LBRACE) {
		// An anonymous class body. It declares a class the grammar does not
		// call a ClassDeclaration, and it has no name, so no constructors.
		ne.Lbrace, ne.Body, ne.Rbrace = p.parseTypeBody(nil)
	}
	ne.Span = ast.At(lo, p.prevEnd())
	return ne
}

// parseNamedTypeToInstantiate reads a ClassOrInterfaceTypeToInstantiate, which
// differs from a ClassType in admitting the diamond.
func (p *parser) parseNamedTypeToInstantiate() ast.Type {
	lo := p.pos()
	nt := alloc[ast.NamedType](p.arena)
	nt.Annotations = p.parseAnnotations()
	nt.Name = p.parseTypeIdent()
	nt.TypeArgs = p.tryTypeArgs()
	nt.Span = ast.At(lo, p.prevEnd())

	for p.at(token.PERIOD) && p.peek(1).Kind == token.IDENT {
		p.next()
		inner := alloc[ast.NamedType](p.arena)
		inner.Qualifier = nt
		inner.Annotations = p.parseAnnotations()
		inner.Name = p.parseTypeIdent()
		inner.TypeArgs = p.tryTypeArgs()
		inner.Span = ast.At(lo, p.prevEnd())
		nt = inner
	}
	return nt
}

func (p *parser) parseArgs() []ast.Expr {
	var args []ast.Expr
	for !p.at(token.RPAREN) && !p.atEOF() {
		args = append(args, p.parseExpr())
		if _, ok := p.accept(token.COMMA); !ok {
			break
		}
	}
	return args
}