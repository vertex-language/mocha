package parser

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
)

func (p *parser) parseBlock() *ast.Block {
	b := alloc[ast.Block](p.arena)
	b.Lbrace = p.expect(token.LBRACE)
	for !p.at(token.RBRACE) && !p.atEOF() {
		before := p.i
		b.Stmts = append(b.Stmts, p.parseBlockStmt())
		if p.i == before {
			p.advanceTo(token.SEMICOLON, token.RBRACE)
			p.accept(token.SEMICOLON)
		}
	}
	b.Rbrace = p.expect(token.RBRACE)
	b.Span = ast.At(b.Lbrace, p.prevEnd())
	return b
}

// parseBlockStmt reads a BlockStatement: a local class or interface, a local
// variable declaration, or a statement.
func (p *parser) parseBlockStmt() ast.Stmt {
	lo := p.pos()

	// A local type declaration is recognizable by its keyword, possibly behind
	// modifiers. `final class C {}` is a declaration; `final` alone is not.
	if p.atLocalTypeDecl() {
		d := p.parseMember(nil)
		s := alloc[ast.DeclStmt](p.arena)
		s.Decl = d
		s.Span = ast.At(lo, p.prevEnd())
		return s
	}

	// A local variable declaration versus an expression statement. Try the
	// declaration; a `var` or a modifier settles it immediately, and otherwise
	// the attempt commits only if a declarator identifier follows the type.
	if d, ok := p.tryLocalVarDecl(true); ok {
		s := alloc[ast.DeclStmt](p.arena)
		s.Decl = d
		s.Span = ast.At(lo, p.prevEnd())
		return s
	}
	return p.parseStmt()
}

func (p *parser) atLocalTypeDecl() bool {
	save := p.i
	defer func() { p.i = save }()
	for isModifierKeyword(p.kind()) || (p.at(token.AT) && p.peek(1).Kind != token.INTERFACE) ||
		p.at(token.NON_SEALED) || p.atCtx(token.CtxSealed) {
		if p.at(token.AT) {
			p.parseAnnotation()
			continue
		}
		p.next()
	}
	switch {
	case p.at(token.CLASS), p.at(token.INTERFACE), p.at(token.ENUM):
		return true
	case p.at(token.AT) && p.peek(1).Kind == token.INTERFACE:
		return true
	}
	return p.atRecordDecl()
}

// tryLocalVarDecl speculates on a LocalVariableDeclaration. semi controls
// whether a terminating `;` is consumed — a for-init and a resource have none.
func (p *parser) tryLocalVarDecl(semi bool) (*ast.VarDecl, bool) {
	return spec(p, func() (*ast.VarDecl, bool) {
		lo := p.pos()
		mods := p.parseModifiers()

		var typ ast.Type
		if p.atCtx(token.CtxVar) && p.peek(1).Kind == token.IDENT {
			t := p.next()
			vt := alloc[ast.VarType](p.arena)
			vt.Span = ast.At(t.Pos, t.End)
			typ = vt
		} else {
			if !isPrimitiveKind(p.kind()) && !p.at(token.IDENT) {
				return nil, false
			}
			typ = p.parseType()
			if _, bad := typ.(*ast.BadType); bad {
				return nil, false
			}
		}

		// The commit point. A type followed by an identifier or `_` is a
		// declaration; anything else was an expression all along.
		if !p.at(token.IDENT) && !p.at(token.UNDERSCORE) {
			return nil, false
		}

		d := alloc[ast.VarDecl](p.arena)
		d.Mods = mods
		d.Type = typ
		d.Names = p.parseVarDeclarators()
		if semi {
			if !p.at(token.SEMICOLON) {
				return nil, false
			}
			d.Semi = p.next().Pos
		}
		d.Span = ast.At(lo, p.prevEnd())
		return d, true
	})
}

func (p *parser) parseStmt() ast.Stmt {
	if !p.enter() {
		p.error(p.pos(), p.end(), "statement nesting too deep")
		p.advanceTo(token.SEMICOLON, token.RBRACE)
		return p.badStmt(p.pos())
	}
	defer p.leave()

	lo := p.pos()
	switch p.kind() {
	case token.LBRACE:
		return p.parseBlock()

	case token.SEMICOLON:
		t := p.next()
		s := alloc[ast.EmptyStmt](p.arena)
		s.Span = ast.At(t.Pos, t.End)
		return s

	case token.IF:
		s := alloc[ast.IfStmt](p.arena)
		s.IfPos = p.next().Pos
		p.expect(token.LPAREN)
		s.Cond = p.parseExpr()
		p.expect(token.RPAREN)
		s.Then = p.parseStmt()
		if t, ok := p.accept(token.ELSE); ok {
			// The dangling else binds to the nearest if, which is what reading
			// it here rather than returning gives for free.
			s.ElsePos = t.Pos
			s.Else = p.parseStmt()
		}
		s.Span = ast.At(lo, p.prevEnd())
		return s

	case token.WHILE:
		s := alloc[ast.WhileStmt](p.arena)
		s.WhilePos = p.next().Pos
		p.expect(token.LPAREN)
		s.Cond = p.parseExpr()
		p.expect(token.RPAREN)
		s.Body = p.parseStmt()
		s.Span = ast.At(lo, p.prevEnd())
		return s

	case token.DO:
		s := alloc[ast.DoStmt](p.arena)
		s.DoPos = p.next().Pos
		s.Body = p.parseStmt()
		s.WhilePos = p.expect(token.WHILE)
		p.expect(token.LPAREN)
		s.Cond = p.parseExpr()
		p.expect(token.RPAREN)
		p.expectSemi()
		s.Span = ast.At(lo, p.prevEnd())
		return s

	case token.FOR:
		return p.parseForStmt()

	case token.SWITCH:
		s := alloc[ast.SwitchStmt](p.arena)
		s.SwitchPos = p.next().Pos
		p.expect(token.LPAREN)
		s.Tag = p.parseExpr()
		p.expect(token.RPAREN)
		s.Block = p.parseSwitchBlock()
		s.Span = ast.At(lo, p.prevEnd())
		return s

	case token.TRY:
		return p.parseTryStmt()

	case token.RETURN:
		s := alloc[ast.ReturnStmt](p.arena)
		s.ReturnPos = p.next().Pos
		if !p.at(token.SEMICOLON) {
			s.Result = p.parseExpr()
		}
		p.expectSemi()
		s.Span = ast.At(lo, p.prevEnd())
		return s

	case token.THROW:
		s := alloc[ast.ThrowStmt](p.arena)
		s.ThrowPos = p.next().Pos
		s.X = p.parseExpr()
		p.expectSemi()
		s.Span = ast.At(lo, p.prevEnd())
		return s

	case token.BREAK:
		s := alloc[ast.BreakStmt](p.arena)
		s.BreakPos = p.next().Pos
		if p.at(token.IDENT) {
			s.Label = p.parseIdent()
		}
		p.expectSemi()
		s.Span = ast.At(lo, p.prevEnd())
		return s

	case token.CONTINUE:
		s := alloc[ast.ContinueStmt](p.arena)
		s.ContinuePos = p.next().Pos
		if p.at(token.IDENT) {
			s.Label = p.parseIdent()
		}
		p.expectSemi()
		s.Span = ast.At(lo, p.prevEnd())
		return s

	case token.SYNCHRONIZED:
		s := alloc[ast.SyncStmt](p.arena)
		s.SyncPos = p.next().Pos
		p.expect(token.LPAREN)
		s.X = p.parseExpr()
		p.expect(token.RPAREN)
		s.Body = p.parseBlock()
		s.Span = ast.At(lo, p.prevEnd())
		return s

	case token.ASSERT:
		s := alloc[ast.AssertStmt](p.arena)
		s.AssertPos = p.next().Pos
		s.X = p.parseExpr()
		if t, ok := p.accept(token.COLON); ok {
			s.Colon = t.Pos
			s.Msg = p.parseExpr()
		}
		p.expectSemi()
		s.Span = ast.At(lo, p.prevEnd())
		return s
	}

	// `yield expr;` is a statement only where the contextual keyword applies;
	// `yield(1)` is a method call and `yield = 2` an assignment.
	if p.atCtx(token.CtxYield) && !startsExprContinuation(p.peek(1).Kind) {
		s := alloc[ast.YieldStmt](p.arena)
		s.YieldPos = p.next().Pos
		s.X = p.parseExpr()
		p.expectSemi()
		s.Span = ast.At(lo, p.prevEnd())
		return s
	}

	// A label is `Identifier :` and nothing else can be.
	if p.at(token.IDENT) && p.peek(1).Kind == token.COLON {
		s := alloc[ast.LabeledStmt](p.arena)
		s.Label = p.parseIdent()
		s.Colon = p.next().Pos
		s.Stmt = p.parseStmt()
		s.Span = ast.At(lo, p.prevEnd())
		return s
	}

	// An explicit constructor invocation is a statement here so that a flexible
	// constructor body needs no split between prologue and epilogue.
	if s := p.tryConstructorCall(); s != nil {
		return s
	}

	x := p.parseExpr()
	if _, bad := x.(*ast.BadExpr); bad {
		p.advanceTo(token.SEMICOLON, token.RBRACE)
		p.accept(token.SEMICOLON)
		return p.badStmt(lo)
	}
	s := alloc[ast.ExprStmt](p.arena)
	s.X = x
	p.expectSemi()
	s.Span = ast.At(lo, p.prevEnd())
	return s
}

// startsExprContinuation reports whether a token could continue an expression
// begun by an identifier — the test that keeps `yield` usable as a name.
func startsExprContinuation(k token.Kind) bool {
	switch k {
	case token.LPAREN, token.PERIOD, token.ASSIGN, token.LBRACK,
		token.COLONCOLON, token.INC, token.DEC, token.SEMICOLON:
		return true
	}
	return k.Precedence() != token.LowestPrec
}

func (p *parser) tryConstructorCall() ast.Stmt {
	s, _ := spec(p, func() (ast.Stmt, bool) {
		lo := p.pos()
		c := alloc[ast.ConstructorCall](p.arena)

		// The unqualified forms: [TypeArguments] this|super ( ... ) ;
		if p.at(token.LSS) {
			ta, ok := p.parseTypeArgs()
			if !ok {
				return nil, false
			}
			c.TypeArgs = ta
		}
		if p.at(token.THIS) || p.at(token.SUPER) {
			t := p.next()
			c.Kind, c.KwPos = t.Kind, t.Pos
			if !p.at(token.LPAREN) {
				return nil, false
			}
			c.Lparen = p.next().Pos
			c.Args = p.parseArgs()
			c.Rparen = p.expect(token.RPAREN)
			p.expectSemi()
			c.Span = ast.At(lo, p.prevEnd())
			return c, true
		}
		// The qualified forms (Primary . super ( ... )) are read by the
		// expression parser and rewritten by the caller of parseBlock; treating
		// them here would need arbitrary lookahead for no benefit.
		return nil, false
	})
	if s == nil {
		return nil
	}
	return s
}

func (p *parser) parseForStmt() ast.Stmt {
	lo := p.pos()
	forPos := p.expect(token.FOR)
	p.expect(token.LPAREN)

	// An enhanced for is a declaration followed by `:`. Try that first; the
	// declaration is the same nonterminal either way.
	if d, ok := spec(p, func() (*ast.VarDecl, bool) {
		d, ok := p.tryLocalVarDecl(false)
		if !ok || !p.at(token.COLON) {
			return nil, false
		}
		return d, true
	}); ok {
		s := alloc[ast.RangeStmt](p.arena)
		s.ForPos = forPos
		s.Decl = d
		s.Colon = p.expect(token.COLON)
		s.X = p.parseExpr()
		p.expect(token.RPAREN)
		s.Body = p.parseStmt()
		s.Span = ast.At(lo, p.prevEnd())
		return s
	}

	s := alloc[ast.ForStmt](p.arena)
	s.ForPos = forPos
	if !p.at(token.SEMICOLON) {
		if d, ok := p.tryLocalVarDecl(false); ok {
			st := alloc[ast.DeclStmt](p.arena)
			st.Decl = d
			st.Span = d.Span
			s.Init = append(s.Init, st)
		} else {
			for {
				elo := p.pos()
				x := p.parseExpr()
				es := alloc[ast.ExprStmt](p.arena)
				es.X = x
				es.Span = ast.At(elo, p.prevEnd())
				s.Init = append(s.Init, es)
				if _, ok := p.accept(token.COMMA); !ok {
					break
				}
			}
		}
	}
	p.expectSemi()
	if !p.at(token.SEMICOLON) {
		s.Cond = p.parseExpr()
	}
	p.expectSemi()
	for !p.at(token.RPAREN) && !p.atEOF() {
		s.Post = append(s.Post, p.parseExpr())
		if _, ok := p.accept(token.COMMA); !ok {
			break
		}
	}
	p.expect(token.RPAREN)
	s.Body = p.parseStmt()
	s.Span = ast.At(lo, p.prevEnd())
	return s
}

func (p *parser) parseTryStmt() ast.Stmt {
	lo := p.pos()
	s := alloc[ast.TryStmt](p.arena)
	s.TryPos = p.expect(token.TRY)

	if t, ok := p.accept(token.LPAREN); ok {
		s.Lparen = t.Pos
		for !p.at(token.RPAREN) && !p.atEOF() {
			s.Resources = append(s.Resources, p.parseResource())
			semi, ok := p.accept(token.SEMICOLON)
			if !ok {
				break
			}
			if p.at(token.RPAREN) {
				s.Semi = semi.Pos
			}
		}
		s.Rparen = p.expect(token.RPAREN)
	}

	s.Body = p.parseBlock()
	for p.at(token.CATCH) {
		s.Catches = append(s.Catches, p.parseCatchClause())
	}
	if t, ok := p.accept(token.FINALLY); ok {
		s.FinallyPos = t.Pos
		s.Finally = p.parseBlock()
	}
	if len(s.Catches) == 0 && s.Finally == nil && len(s.Resources) == 0 {
		p.error(s.TryPos, s.TryPos+3, "try statement requires a catch clause or a finally block")
	}
	s.Span = ast.At(lo, p.prevEnd())
	return s
}

// parseResource reads one Resource. That a declaring resource must declare
// exactly one variable with an initializer is semantic, not syntactic.
func (p *parser) parseResource() *ast.Resource {
	lo := p.pos()
	r := alloc[ast.Resource](p.arena)
	if d, ok := p.tryLocalVarDecl(false); ok {
		r.Decl = d
	} else {
		r.X = p.parseExpr()
	}
	r.Span = ast.At(lo, p.prevEnd())
	return r
}

func (p *parser) parseCatchClause() *ast.CatchClause {
	lo := p.pos()
	c := alloc[ast.CatchClause](p.arena)
	c.CatchPos = p.expect(token.CATCH)
	p.expect(token.LPAREN)
	c.Mods = p.parseModifiers()
	c.Types = append(c.Types, p.parseType())
	for {
		// `|` in a catch type is a union separator, never a binary operator.
		if _, ok := p.accept(token.OR); !ok {
			break
		}
		c.Types = append(c.Types, p.parseType())
	}
	c.Name = p.parseVarDeclaratorId()
	p.expect(token.RPAREN)
	c.Body = p.parseBlock()
	c.Span = ast.At(lo, p.prevEnd())
	return c
}

// --- switch blocks ----------------------------------------------------------

// parseSwitchBlock reads the body shared by the switch statement and the switch
// expression. The arrow form and the colon form cannot be mixed; the first
// label's separator decides which shape the block has.
func (p *parser) parseSwitchBlock() *ast.SwitchBlock {
	b := alloc[ast.SwitchBlock](p.arena)
	b.Lbrace = p.expect(token.LBRACE)

	for !p.at(token.RBRACE) && !p.atEOF() {
		before := p.i
		label := p.parseSwitchLabel()

		if t, ok := p.accept(token.ARROW); ok {
			r := alloc[ast.SwitchRule](p.arena)
			r.Label = label
			r.Arrow = t.Pos
			switch {
			case p.at(token.LBRACE):
				r.Body = p.parseBlock()
			case p.at(token.THROW):
				r.Body = p.parseStmt()
			default:
				r.Body = p.parseExpr()
				p.expectSemi()
			}
			r.Span = ast.At(label.Lo, p.prevEnd())
			b.Rules = append(b.Rules, r)
		} else {
			p.expect(token.COLON)
			g := alloc[ast.SwitchGroup](p.arena)
			g.Labels = append(g.Labels, label)
			for p.at(token.CASE) || p.at(token.DEFAULT) {
				l := p.parseSwitchLabel()
				p.expect(token.COLON)
				g.Labels = append(g.Labels, l)
			}
			for !p.at(token.CASE) && !p.at(token.DEFAULT) &&
				!p.at(token.RBRACE) && !p.atEOF() {
				g.Stmts = append(g.Stmts, p.parseBlockStmt())
			}
			if len(g.Stmts) == 0 && p.at(token.RBRACE) {
				// Trailing labels governing nothing: the grammar admits them.
				b.Labels = append(b.Labels, g.Labels...)
			} else {
				g.Span = ast.At(g.Labels[0].Lo, p.prevEnd())
				b.Groups = append(b.Groups, g)
			}
		}

		if p.i == before {
			p.advanceTo(token.CASE, token.DEFAULT, token.RBRACE)
		}
	}
	b.Rbrace = p.expect(token.RBRACE)
	b.Span = ast.At(b.Lbrace, p.prevEnd())
	return b
}

// parseSwitchLabel reads one `case` or `default` label.
//
// Constants and patterns share a prefix, so each element is attempted as a
// pattern first and falls back to a ConditionalExpression. That a multi-pattern
// label may declare no pattern variables, and that its guard governs the whole
// label, are semantic rules.
func (p *parser) parseSwitchLabel() *ast.SwitchLabel {
	lo := p.pos()
	l := alloc[ast.SwitchLabel](p.arena)

	if t, ok := p.accept(token.DEFAULT); ok {
		l.KwPos = t.Pos
		l.Default = true
		l.DefaultPos = t.Pos
		l.Span = ast.At(lo, p.prevEnd())
		return l
	}

	l.KwPos = p.expect(token.CASE)

	// `case null` and `case null, default` are their own alternative.
	if p.at(token.NULL) {
		t := p.next()
		lit := alloc[ast.BasicLit](p.arena)
		lit.Span = ast.At(t.Pos, t.End)
		lit.Kind = token.NULL
		l.Cases = append(l.Cases, lit)
		if _, ok := p.accept(token.COMMA); ok {
			if d, ok := p.accept(token.DEFAULT); ok {
				l.Default = true
				l.DefaultPos = d.Pos
			} else {
				p.errorExpected("'default'")
			}
		}
		l.Span = ast.At(lo, p.prevEnd())
		return l
	}

	for {
		if pat, ok := p.tryPattern(); ok {
			l.Cases = append(l.Cases, pat)
		} else {
			l.Cases = append(l.Cases, p.parseConditional())
		}
		if _, ok := p.accept(token.COMMA); !ok {
			break
		}
	}

	if t, ok := p.acceptCtx(token.CtxWhen); ok {
		l.WhenPos = t.Pos
		l.Guard = p.parseExpr()
	}
	l.Span = ast.At(lo, p.prevEnd())
	return l
}

// --- patterns ---------------------------------------------------------------

// tryPattern speculates on a Pattern. A bare name is a constant, not a pattern,
// so the attempt requires either a record pattern's `(` or a type followed by
// an identifier.
func (p *parser) tryPattern() (ast.Pattern, bool) {
	return spec(p, func() (ast.Pattern, bool) {
		lo := p.pos()
		mods := p.parseModifiers()
		if !isPrimitiveKind(p.kind()) && !p.at(token.IDENT) {
			return nil, false
		}
		typ := p.parseType()
		if _, bad := typ.(*ast.BadType); bad {
			return nil, false
		}

		if p.at(token.LPAREN) {
			rp := alloc[ast.RecordPattern](p.arena)
			rp.Type = typ
			rp.Lparen = p.next().Pos
			for !p.at(token.RPAREN) && !p.atEOF() {
				elt, ok := p.parseComponentPattern()
				if !ok {
					return nil, false
				}
				rp.Elts = append(rp.Elts, elt)
				if _, ok := p.accept(token.COMMA); !ok {
					break
				}
			}
			if !p.at(token.RPAREN) {
				return nil, false
			}
			rp.Rparen = p.next().Pos
			rp.Span = ast.At(lo, p.prevEnd())
			return rp, true
		}

		if !p.at(token.IDENT) && !p.at(token.UNDERSCORE) {
			return nil, false
		}
		tp := alloc[ast.TypePattern](p.arena)
		tp.Mods = mods
		tp.Type = typ
		tp.Name = p.parseVarDeclaratorId()
		tp.Span = ast.At(lo, p.prevEnd())
		return tp, true
	})
}

func (p *parser) parseComponentPattern() (ast.Pattern, bool) {
	if t, ok := p.accept(token.UNDERSCORE); ok {
		m := alloc[ast.MatchAllPattern](p.arena)
		m.Span = ast.At(t.Pos, t.End)
		return m, true
	}
	return p.tryPattern()
}