package parser

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
)

// One recoverable diagnostic, never a cascade. After an error the parser goes
// quiet until it successfully consumes a token, and it never reports twice at
// the same position.

func (p *parser) error(lo, hi token.Pos, msg string) {
	if p.quiet || lo == p.lastErr {
		return
	}
	if hi <= lo {
		hi = lo + 1
	}
	p.quiet = true
	p.lastErr = lo
	p.diags = append(p.diags, token.Diagnostic{
		Pos: lo, End: hi, Severity: token.SevError, Msg: msg,
	})
}

func (p *parser) errorExpected(what string) {
	t := p.tok()
	got := t.Kind.String()
	if t.Kind == token.EOF {
		got = "end of file"
	} else if t.Kind == token.IDENT {
		got = "'" + p.f.Slice(t.Pos, t.End) + "'"
	} else {
		got = "'" + got + "'"
	}
	p.error(t.Pos, t.End, "expected "+what+", found "+got)
}

// budget reports whether the parser may still attempt recovery. Past the cap it
// runs to EOF silently rather than spending the rest of the file resyncing.
func (p *parser) budget() bool {
	if p.mode&Tolerant != 0 {
		return true
	}
	p.resyncs++
	return p.resyncs <= maxResync
}

// advanceTo skips tokens until one of the given kinds is current, or EOF. It
// steps over balanced bracket groups, so a `;` inside a nested block does not
// look like the statement terminator being searched for.
func (p *parser) advanceTo(follow ...token.Kind) {
	if !p.budget() {
		p.i = len(p.toks) - 1
		return
	}
	for !p.atEOF() {
		k := p.kind()
		for _, f := range follow {
			if k == f {
				return
			}
		}
		switch k {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			p.skipBalanced()
		default:
			p.next()
		}
	}
}

// skipBalanced consumes one bracket group, opener through matching closer. A
// group left unclosed at EOF is consumed to EOF — the scanner has already
// reported it, and there is nothing better to stop at.
func (p *parser) skipBalanced() {
	var stack []token.Kind
	for !p.atEOF() {
		switch p.kind() {
		case token.LPAREN:
			stack = append(stack, token.RPAREN)
		case token.LBRACK:
			stack = append(stack, token.RBRACK)
		case token.LBRACE:
			stack = append(stack, token.RBRACE)
		case token.RPAREN, token.RBRACK, token.RBRACE:
			if n := len(stack); n > 0 {
				want := stack[n-1]
				stack = stack[:n-1]
				p.next()
				if p.kind() != want && len(stack) == 0 {
					return
				}
				if len(stack) == 0 {
					return
				}
				continue
			}
			return // a closer with no opener belongs to an enclosing group
		}
		p.next()
		if len(stack) == 0 {
			return
		}
	}
}

// --- Bad* placeholders ------------------------------------------------------
//
// Each covers the tokens the parser gave up on, and each span is non-empty even
// when nothing was consumed.

func (p *parser) badExpr(lo token.Pos) *ast.BadExpr {
	n := alloc[ast.BadExpr](p.arena)
	n.Span = p.badSpan(lo)
	return n
}

func (p *parser) badStmt(lo token.Pos) *ast.BadStmt {
	n := alloc[ast.BadStmt](p.arena)
	n.Span = p.badSpan(lo)
	return n
}

func (p *parser) badDecl(lo token.Pos) *ast.BadDecl {
	n := alloc[ast.BadDecl](p.arena)
	n.Span = p.badSpan(lo)
	return n
}

func (p *parser) badType(lo token.Pos) *ast.BadType {
	n := alloc[ast.BadType](p.arena)
	n.Span = p.badSpan(lo)
	return n
}

func (p *parser) badPattern(lo token.Pos) *ast.BadPattern {
	n := alloc[ast.BadPattern](p.arena)
	n.Span = p.badSpan(lo)
	return n
}

func (p *parser) badSpan(lo token.Pos) ast.Span {
	hi := p.prevEnd()
	if hi <= lo {
		hi = lo + 1
	}
	return ast.At(lo, hi)
}