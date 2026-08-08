package parser

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
)

// parseAnnotations reads `{Annotation}`. An `@` followed by `interface` is a
// declaration, not an annotation, and is left for the caller.
func (p *parser) parseAnnotations() []*ast.Annotation {
	var anns []*ast.Annotation
	for p.at(token.AT) && p.peek(1).Kind != token.INTERFACE {
		anns = append(anns, p.parseAnnotation())
	}
	return anns
}

func (p *parser) parseAnnotation() *ast.Annotation {
	a := alloc[ast.Annotation](p.arena)
	a.AtPos = p.expect(token.AT)
	a.Name = p.parseName()

	if _, ok := p.accept(token.LPAREN); ok {
		if !p.at(token.RPAREN) {
			// A SingleElementAnnotation is one ElementValue with no `=`; a
			// NormalAnnotation is a pair list. One token of lookahead past the
			// identifier separates them.
			if p.at(token.IDENT) && p.peek(1).Kind == token.ASSIGN {
				for {
					a.Pairs = append(a.Pairs, p.parseElementValuePair())
					if _, ok := p.accept(token.COMMA); !ok {
						break
					}
				}
			} else {
				lo := p.pos()
				pair := alloc[ast.ElementValuePair](p.arena)
				pair.Value = p.parseElementValue()
				pair.Span = ast.At(lo, p.prevEnd())
				a.Pairs = append(a.Pairs, pair)
			}
		}
		p.expect(token.RPAREN)
	}
	a.Span = ast.At(a.AtPos, p.prevEnd())
	return a
}

func (p *parser) parseElementValuePair() *ast.ElementValuePair {
	lo := p.pos()
	pair := alloc[ast.ElementValuePair](p.arena)
	pair.Name = p.parseIdent()
	p.expect(token.ASSIGN)
	pair.Value = p.parseElementValue()
	pair.Span = ast.At(lo, p.prevEnd())
	return pair
}

// parseElementValue reads a ConditionalExpression, a nested annotation, or an
// array initializer. Note the expression form is conditional, not assignment:
// an annotation element value cannot be an assignment or a lambda.
func (p *parser) parseElementValue() ast.Node {
	switch {
	case p.at(token.AT):
		return p.parseAnnotation()
	case p.at(token.LBRACE):
		lo := p.pos()
		arr := alloc[ast.ElementValueArray](p.arena)
		p.next()
		for !p.at(token.RBRACE) && !p.atEOF() {
			arr.Elts = append(arr.Elts, p.parseElementValue())
			t, ok := p.accept(token.COMMA)
			if !ok {
				break
			}
			if p.at(token.RBRACE) {
				arr.Comma = t.Pos
			}
		}
		p.expect(token.RBRACE)
		arr.Span = ast.At(lo, p.prevEnd())
		return arr
	}
	return p.parseConditional()
}