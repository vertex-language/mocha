// Package parser turns a token.File into an ast.File plus a sorted diagnostic
// slice. Recursive descent for declarations and statements, precedence climbing
// for expressions, one mark/rollback mechanism for the three genuinely
// ambiguous prefixes.
//
// The parser interprets nothing. It decides which production applies and where
// each node begins and ends; it does not decode literals, resolve names, or
// check that a contextual keyword was used in a sensible place beyond the
// production admitting it.
//
// A partial parse is a usable one. Every entry point returns a node — a Bad*
// placeholder if it has to — so consumers read a tree, not a success flag.
package parser

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/scanner"
	"github.com/vertex-language/mocha/token"
)

// Mode is a set of parser options.
type Mode uint

const (
	// ParseComments retains comment tokens on the tree's File. Without it
	// comments are trivia, recoverable from spans via token.File.Between.
	ParseComments Mode = 1 << iota

	// HeaderOnly stops after the package declaration, the imports and any
	// module directives. Type bodies are skipped balanced, not parsed.
	//
	// In Java the result is a lower bound on the dependency graph, never the
	// graph: same-package types need no import, on-demand imports name a
	// package, module imports name a module, and a fully qualified name can
	// appear inline in any expression.
	HeaderOnly

	// Tolerant keeps going past the resync budget instead of abandoning the
	// rest of the unit. Useful for editors, wasteful for batch builds.
	Tolerant
)

const DefaultMode Mode = 0

const (
	// maxDepth caps nesting. Deeply nested generated sources exist; unbounded
	// recursion on hostile input does not have to.
	maxDepth = 1000

	// maxResync caps recovery attempts per unit. Past it the parser stops
	// producing diagnostics and runs to EOF, unless Tolerant is set.
	maxResync = 100
)

// ParseFile scans and parses one compilation unit. The returned tree is never
// nil, and its Release method returns the arena backing it.
func ParseFile(f *token.File, mode Mode) (*ast.File, []token.Diagnostic) {
	smode := scanner.Mode(0)
	if mode&ParseComments != 0 {
		smode |= scanner.ScanComments
	}
	toks, diags := scanner.Scan(f, smode)

	p := &parser{f: f, mode: mode, diags: diags, arena: newArena()}
	p.load(toks)

	file := p.parseCompilationUnit()
	file.Unit = f
	file.Releaser = p.arena

	token.SortDiagnostics(p.diags)
	return file, p.diags
}

type parser struct {
	f     *token.File
	mode  Mode
	arena *arena

	toks     []token.Token
	comments []token.Token
	i        int

	diags   []token.Diagnostic
	quiet   bool // suppressing until the next token is successfully consumed
	lastErr token.Pos
	resyncs int
	depth   int
}

// load splits the scanner's output into the parse stream and, under
// ParseComments, a separate comment slice. The parse stream is immutable for
// the rest of the run, which is what lets speculation be an integer.
func (p *parser) load(toks []token.Token) {
	if p.mode&ParseComments == 0 {
		p.toks = toks
		return
	}
	p.toks = make([]token.Token, 0, len(toks))
	for _, t := range toks {
		if t.Kind == token.COMMENT {
			p.comments = append(p.comments, t)
			continue
		}
		p.toks = append(p.toks, t)
	}
}

// --- cursor -----------------------------------------------------------------

func (p *parser) tok() token.Token  { return p.toks[p.i] }
func (p *parser) kind() token.Kind  { return p.toks[p.i].Kind }
func (p *parser) pos() token.Pos    { return p.toks[p.i].Pos }
func (p *parser) end() token.Pos    { return p.toks[p.i].End }
func (p *parser) atEOF() bool       { return p.toks[p.i].Kind == token.EOF }

// prevEnd is the end of the last consumed token — the right edge of any node
// that just finished.
func (p *parser) prevEnd() token.Pos {
	if p.i == 0 {
		return p.toks[0].Pos
	}
	return p.toks[p.i-1].End
}

func (p *parser) peek(n int) token.Token {
	if p.i+n >= len(p.toks) {
		return p.toks[len(p.toks)-1] // EOF
	}
	return p.toks[p.i+n]
}

func (p *parser) at(k token.Kind) bool { return p.kind() == k }

// atCtx implements the contextual-keyword policy: a spelling is a keyword only
// where the production admits it, which is exactly where this is called. The
// adjacency half of §3.9 was settled by the scanner.
func (p *parser) atCtx(c token.Ctx) bool {
	t := p.tok()
	return (t.Kind == token.IDENT || t.Kind == token.NON_SEALED) && t.Ctx == c
}

func (p *parser) next() token.Token {
	t := p.toks[p.i]
	if t.Kind != token.EOF {
		p.i++
	}
	p.quiet = false // a successful consume ends the suppression window
	return t
}

func (p *parser) accept(k token.Kind) (token.Token, bool) {
	if p.kind() == k {
		return p.next(), true
	}
	return token.Token{}, false
}

func (p *parser) acceptCtx(c token.Ctx) (token.Token, bool) {
	if p.atCtx(c) {
		return p.next(), true
	}
	return token.Token{}, false
}

// expect consumes k or reports its absence. It returns the token's position
// either way, so a node's span is never left invalid by a missing delimiter.
func (p *parser) expect(k token.Kind) token.Pos {
	if p.kind() == k {
		return p.next().Pos
	}
	p.errorExpected(k.String())
	return p.prevEnd()
}

func (p *parser) expectSemi() token.Pos {
	// No automatic semicolon insertion. NLBefore stays on the token for
	// diagnostics and formatters; this is a plain `;` expectation.
	return p.expect(token.SEMICOLON)
}

// --- identifiers ------------------------------------------------------------

func (p *parser) parseIdent() *ast.Ident {
	t := p.tok()
	if t.Kind != token.IDENT {
		p.errorExpected("identifier")
		id := alloc[ast.Ident](p.arena)
		id.Span = ast.At(t.Pos, t.Pos+1)
		return id
	}
	p.next()
	id := alloc[ast.Ident](p.arena)
	id.Span = ast.At(t.Pos, t.End)
	id.Ctx = t.Ctx
	return id
}

// parseTypeIdent enforces §3.8 TypeIdentifier: not permits, record, sealed,
// var or yield.
func (p *parser) parseTypeIdent() *ast.Ident {
	if p.at(token.IDENT) && p.tok().Ctx.BarredFromTypeIdentifier() {
		p.error(p.pos(), p.end(), "'"+p.tok().Ctx.String()+"' cannot be used as a type name")
	}
	return p.parseIdent()
}

// parseMethodIdent enforces §3.8 UnqualifiedMethodIdentifier: not yield.
func (p *parser) parseMethodIdent() *ast.Ident {
	if p.at(token.IDENT) && p.tok().Ctx.BarredFromMethodIdentifier() {
		p.error(p.pos(), p.end(), "'yield' cannot be used as a method name")
	}
	return p.parseIdent()
}

// parseVarDeclaratorId handles the `_` alternative. Which contexts admit an
// unnamed variable is a semantic question; the grammar admits it everywhere a
// VariableDeclaratorId appears.
func (p *parser) parseVarDeclaratorId() *ast.Ident {
	if t, ok := p.accept(token.UNDERSCORE); ok {
		id := alloc[ast.Ident](p.arena)
		id.Span = ast.At(t.Pos, t.End)
		id.Underscore = true
		return id
	}
	return p.parseIdent()
}

// parseName reads a dotted name. Which of §6's six name nonterminals it is
// depends on resolution, so the tree keeps only the parts.
func (p *parser) parseName() *ast.Name {
	n := alloc[ast.Name](p.arena)
	lo := p.pos()
	n.Parts = append(n.Parts, p.parseIdent())
	for p.at(token.PERIOD) && p.peek(1).Kind == token.IDENT {
		p.next()
		n.Parts = append(n.Parts, p.parseIdent())
	}
	n.Span = ast.At(lo, p.prevEnd())
	return n
}

func (p *parser) enter() bool {
	p.depth++
	return p.depth <= maxDepth
}

func (p *parser) leave() { p.depth-- }