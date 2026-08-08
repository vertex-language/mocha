package parser

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/token"
)

func isPrimitiveKind(k token.Kind) bool {
	switch k {
	case token.BOOLEAN, token.BYTE, token.SHORT, token.INT_KW,
		token.LONG, token.CHARK, token.FLOATK, token.DOUBLE:
		return true
	}
	return false
}

// parseType reads a Type: a PrimitiveType or a ReferenceType, with any trailing
// Dims folded into an ArrayType.
func (p *parser) parseType() ast.Type {
	if !p.enter() {
		p.error(p.pos(), p.end(), "type nesting too deep")
		return p.badType(p.pos())
	}
	defer p.leave()

	lo := p.pos()
	anns := p.parseAnnotations()

	var base ast.Type
	switch {
	case isPrimitiveKind(p.kind()):
		t := p.next()
		pt := alloc[ast.PrimitiveType](p.arena)
		pt.Span = ast.At(lo, t.End)
		pt.Annotations = anns
		pt.Kind = t.Kind
		pt.KwPos = t.Pos
		base = pt
	case p.at(token.IDENT):
		base = p.parseNamedType(lo, anns)
	default:
		p.errorExpected("type")
		return p.badType(lo)
	}

	if dims := p.parseDims(); len(dims) > 0 {
		at := alloc[ast.ArrayType](p.arena)
		at.Span = ast.At(lo, p.prevEnd())
		at.Elt = base
		at.Dims = dims
		return at
	}
	return base
}

// parseNamedType reads a ClassType, InterfaceType or TypeVariable, including
// qualification and type arguments at each level. Whether a qualifier is a
// package or an enclosing type is resolution's problem.
func (p *parser) parseNamedType(lo token.Pos, anns []*ast.Annotation) ast.Type {
	nt := alloc[ast.NamedType](p.arena)
	nt.Annotations = anns
	nt.Name = p.parseTypeIdent()
	nt.TypeArgs = p.tryTypeArgs()
	nt.Span = ast.At(lo, p.prevEnd())

	for p.at(token.PERIOD) {
		// `.class`, `.this`, `.new` and `.super` end the type and belong to an
		// enclosing expression.
		nk := p.peek(1).Kind
		if nk != token.IDENT {
			break
		}
		save := p.i
		p.next() // .
		innerLo := p.pos()
		innerAnns := p.parseAnnotations()
		if !p.at(token.IDENT) {
			p.i = save
			break
		}
		inner := alloc[ast.NamedType](p.arena)
		inner.Qualifier = nt
		inner.Annotations = innerAnns
		inner.Name = p.parseTypeIdent()
		inner.TypeArgs = p.tryTypeArgs()
		inner.Span = ast.At(lo, p.prevEnd())
		_ = innerLo
		nt = inner
	}
	return nt
}

// parseDims reads `{Annotation} [ ]` repeatedly. It never consumes a `[` that
// begins an array access, because that `[` is followed by an expression.
func (p *parser) parseDims() []*ast.Dim {
	var dims []*ast.Dim
	for {
		save := p.i
		lo := p.pos()
		anns := p.parseAnnotations()
		if !p.at(token.LBRACK) || p.peek(1).Kind != token.RBRACK {
			p.i = save
			return dims
		}
		lb := p.next().Pos
		rb := p.next().End
		d := alloc[ast.Dim](p.arena)
		d.Span = ast.At(lo, rb)
		d.Annotations = anns
		d.Lbrack = lb
		d.Rbrack = rb - 1
		dims = append(dims, d)
	}
}

// tryTypeArgs speculates on `<`. A `<` that does not open a well-formed
// TypeArguments is a less-than, and the cursor is restored for the expression
// parser to read it as one.
func (p *parser) tryTypeArgs() *ast.TypeArgs {
	if !p.at(token.LSS) {
		return nil
	}
	ta, ok := spec(p, func() (*ast.TypeArgs, bool) {
		return p.parseTypeArgs()
	})
	if !ok {
		return nil
	}
	return ta
}

// parseTypeArgs reads `< TypeArgumentList >` or the diamond `<>`.
//
// Nested arguments need no special handling. The scanner never merges `>` with
// a following `>`, so `List<List<String>>` closes on two separate tokens and
// each level consumes exactly one — this is the whole payoff of the deviation,
// and it is why nothing here calls token.Join.
func (p *parser) parseTypeArgs() (*ast.TypeArgs, bool) {
	lt := p.expect(token.LSS)
	ta := alloc[ast.TypeArgs](p.arena)
	ta.Lt = lt

	if gt, ok := p.accept(token.GTR); ok {
		ta.Diamond = true
		ta.Gt = gt.Pos
		ta.Span = ast.At(lt, gt.End)
		return ta, true
	}

	for {
		arg, ok := p.parseTypeArg()
		if !ok {
			return nil, false
		}
		ta.List = append(ta.List, arg)
		if _, ok := p.accept(token.COMMA); !ok {
			break
		}
	}

	if !p.at(token.GTR) {
		return nil, false
	}
	gt := p.next()
	ta.Gt = gt.Pos
	ta.Span = ast.At(lt, gt.End)
	return ta, true
}

func (p *parser) parseTypeArg() (ast.Type, bool) {
	lo := p.pos()
	anns := p.parseAnnotations()

	if q, ok := p.accept(token.QUESTION); ok {
		w := alloc[ast.Wildcard](p.arena)
		w.Annotations = anns
		w.QPos = q.Pos
		switch {
		case p.at(token.EXTENDS), p.at(token.SUPER):
			w.BoundKind = p.next().Kind
			w.Bound = p.parseType()
		}
		w.Span = ast.At(lo, p.prevEnd())
		return w, true
	}

	if !isPrimitiveKind(p.kind()) && !p.at(token.IDENT) {
		return nil, false
	}
	// A TypeArgument is a ReferenceType, so a primitive is admissible only as
	// an array element type. Left to the semantic phase; the shape is a type.
	p.i-- // re-read the annotations inside parseType for a correct span
	p.i++
	t := p.parseTypeWithAnnotations(lo, anns)
	if _, bad := t.(*ast.BadType); bad {
		return nil, false
	}
	return t, true
}

// parseTypeWithAnnotations is parseType with the leading annotations already
// consumed by the caller.
func (p *parser) parseTypeWithAnnotations(lo token.Pos, anns []*ast.Annotation) ast.Type {
	var base ast.Type
	switch {
	case isPrimitiveKind(p.kind()):
		t := p.next()
		pt := alloc[ast.PrimitiveType](p.arena)
		pt.Span = ast.At(lo, t.End)
		pt.Annotations = anns
		pt.Kind = t.Kind
		pt.KwPos = t.Pos
		base = pt
	case p.at(token.IDENT):
		base = p.parseNamedType(lo, anns)
	default:
		p.errorExpected("type")
		return p.badType(lo)
	}
	if dims := p.parseDims(); len(dims) > 0 {
		at := alloc[ast.ArrayType](p.arena)
		at.Span = ast.At(lo, p.prevEnd())
		at.Elt = base
		at.Dims = dims
		return at
	}
	return base
}

// parseTypeParams reads `< TypeParameterList >`.
func (p *parser) parseTypeParams() *ast.TypeParams {
	if !p.at(token.LSS) {
		return nil
	}
	tp := alloc[ast.TypeParams](p.arena)
	tp.Lt = p.next().Pos
	for {
		tp.List = append(tp.List, p.parseTypeParam())
		if _, ok := p.accept(token.COMMA); !ok {
			break
		}
	}
	gt := p.expect(token.GTR)
	tp.Gt = gt
	tp.Span = ast.At(tp.Lt, p.prevEnd())
	return tp
}

func (p *parser) parseTypeParam() *ast.TypeParam {
	lo := p.pos()
	tp := alloc[ast.TypeParam](p.arena)
	tp.Annotations = p.parseAnnotations()
	tp.Name = p.parseTypeIdent()
	if _, ok := p.accept(token.EXTENDS); ok {
		tp.Bounds = append(tp.Bounds, p.parseType())
		for {
			// `&` here is an AdditionalBound, never a binary operator: no
			// expression can appear in a type bound.
			if _, ok := p.accept(token.AND); !ok {
				break
			}
			tp.Bounds = append(tp.Bounds, p.parseType())
		}
	}
	tp.Span = ast.At(lo, p.prevEnd())
	return tp
}

// parseTypeList reads a comma-separated list of types (an InterfaceTypeList, a
// permits clause, an ExceptionTypeList).
func (p *parser) parseTypeList() []ast.Type {
	var list []ast.Type
	for {
		list = append(list, p.parseType())
		if _, ok := p.accept(token.COMMA); !ok {
			return list
		}
	}
}