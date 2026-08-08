package parser

import "github.com/vertex-language/mocha/token"

// The token buffer is immutable and the cursor is an integer, so a checkpoint
// is a struct copy and a rollback is an assignment. Nodes allocated during a
// failed attempt stay in the arena as garbage until Release — cheap, and much
// simpler than unwinding allocation.
//
// Three sites need this, and only three:
//
//   - `(` opening a cast versus a parenthesized expression
//   - `(` opening a lambda parameter list versus either of the above
//   - `<` opening type arguments versus less-than
//
// A fourth case looks like speculation and is not: a local variable declaration
// versus an expression statement is decided by trying the declaration, which
// uses the same mechanism but commits on the declarator identifier.

type mark struct {
	i       int
	ndiags  int
	quiet   bool
	lastErr token.Pos
	resyncs int
	depth   int
}

func (p *parser) mark() mark {
	return mark{
		i: p.i, ndiags: len(p.diags), quiet: p.quiet,
		lastErr: p.lastErr, resyncs: p.resyncs, depth: p.depth,
	}
}

// rollback restores the cursor and discards any diagnostics the attempt
// produced. Discarding is the point: a failed speculation is not an error, and
// reporting one would violate the one-diagnostic rule with noise the user
// cannot act on.
func (p *parser) rollback(m mark) {
	p.i = m.i
	p.diags = p.diags[:m.ndiags]
	p.quiet = m.quiet
	p.lastErr = m.lastErr
	p.resyncs = m.resyncs
	p.depth = m.depth
}

// speculate runs try. If try returns false, the parser is restored exactly as
// it was and speculate returns false; the caller then parses the alternative
// for real.
func (p *parser) speculate(try func() bool) bool {
	m := p.mark()
	if try() {
		return true
	}
	p.rollback(m)
	return false
}

// spec is speculate for an attempt that produces a value.
func spec[T any](p *parser, try func() (T, bool)) (T, bool) {
	m := p.mark()
	v, ok := try()
	if !ok {
		p.rollback(m)
		var zero T
		return zero, false
	}
	return v, true
}