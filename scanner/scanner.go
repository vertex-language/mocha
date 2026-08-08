// Package scanner turns a token.File into a complete token slice.
//
// It tokenizes the whole unit up front and never stops early: every scan path
// advances at least one byte, and malformed input yields an exact span plus one
// diagnostic rather than a cascade. Nothing here interprets: literals keep
// their raw spelling, text blocks keep their delimiters and their incidental
// whitespace, and contextual keywords are tagged with a token.Ctx for the
// parser to accept or reject per production.
//
// Two rules are not the JLS's:
//
//   - A '>' is never combined with a following '>'. The split is
//     unconditional; mocha has no lexical notion of type context. Longest match
//     still governs '>=', so ">>=" scans as '>' '>=' and ">>>" as '>' '>' '>'.
//     The parser rejoins adjacent runs via token.Join.
//   - "non-sealed" is spliced into one token when nothing separates the three
//     pieces and no JavaLetterOrDigit abuts either end. "non-sealedclass" is
//     therefore three tokens.
package scanner

import (
	"unicode/utf8"

	"github.com/vertex-language/mocha/token"
)

// Mode selects optional scanner behaviour.
type Mode uint

const (
	// ScanComments keeps COMMENT tokens in the stream. Without it comments are
	// consumed as trivia; either way they break adjacency and are visible in
	// the gap between tokens via token.File.Between.
	ScanComments Mode = 1 << iota
)

// Scan tokenizes f in full. The returned slice always ends in an EOF token —
// the one token with a zero-width span — and the diagnostics are sorted.
//
// Diagnostics produced during escape translation (token.NewFile) are included,
// so the caller does not need to merge two slices.
func Scan(f *token.File, mode Mode) ([]token.Token, []token.Diagnostic) {
	s := &scanner{f: f, src: f.Text(), mode: mode}
	s.diags = append(s.diags, f.Diagnostics()...)
	s.run()
	token.SortDiagnostics(s.diags)
	return s.toks, s.diags
}

type scanner struct {
	f    *token.File
	src  []byte
	mode Mode
	off  int

	toks  []token.Token
	diags []token.Diagnostic

	frames frameStack

	gap bool // white space or a comment since the last token
	nl  bool // a LineTerminator since the last token
}

func (s *scanner) run() {
	// A rough token every four bytes; one allocation for most files.
	s.toks = make([]token.Token, 0, len(s.src)/4+8)

	for {
		s.skipTrivia()
		if s.off >= len(s.src) {
			break
		}
		s.scanToken()
	}
	s.frames.finish(s)

	end := len(s.src)
	s.emit(token.EOF, token.CtxNone, end, end)
}

func (s *scanner) scanToken() {
	pos := s.off
	c := s.src[pos]

	switch {
	case isJavaLetterStartByte(c) || c >= utf8.RuneSelf:
		if s.scanIdent(pos) {
			return
		}
		// A non-ASCII byte that is not a Java letter falls through to illegal.
		s.illegalRune(pos)
		return
	case isDecDigit(c):
		s.emit(s.scanNumber(), token.CtxNone, pos, s.off)
		return
	}

	switch c {
	case '"':
		if s.has(`"""`) {
			s.scanTextBlock(pos)
		} else {
			s.scanString(pos)
		}
		return
	case '\'':
		s.scanChar(pos)
		return
	case '.':
		if pos+1 < len(s.src) && isDecDigit(s.src[pos+1]) {
			s.emit(s.scanNumber(), token.CtxNone, pos, s.off)
			return
		}
	}

	kind, n := s.punct(pos)
	if n == 0 {
		s.illegalRune(pos)
		return
	}
	s.off = pos + n
	s.frames.track(s, kind, pos, s.off)
	s.emit(kind, token.CtxNone, pos, s.off)
}

// punct recognizes one Separator or Operator by longest match, with the single
// exception that '>' is never extended by a following '>'. It returns the Kind
// and its byte length, or (ILLEGAL, 0).
func (s *scanner) punct(pos int) (token.Kind, int) {
	c := s.src[pos]
	next := func(i int) byte {
		if pos+i < len(s.src) {
			return s.src[pos+i]
		}
		return 0
	}

	switch c {
	// The deviation. '>' takes a following '=' and nothing else; a following
	// '>' starts its own token, in every context.
	case '>':
		if next(1) == '=' {
			return token.GEQ, 2
		}
		return token.GTR, 1

	case '<':
		switch {
		case next(1) == '<' && next(2) == '=':
			return token.SHL_ASSIGN, 3
		case next(1) == '<':
			return token.SHL, 2
		case next(1) == '=':
			return token.LEQ, 2
		}
		return token.LSS, 1

	case '=':
		if next(1) == '=' {
			return token.EQL, 2
		}
		return token.ASSIGN, 1
	case '!':
		if next(1) == '=' {
			return token.NEQ, 2
		}
		return token.NOT, 1
	case '+':
		switch next(1) {
		case '+':
			return token.INC, 2
		case '=':
			return token.ADD_ASSIGN, 2
		}
		return token.ADD, 1
	case '-':
		switch next(1) {
		case '-':
			return token.DEC, 2
		case '=':
			return token.SUB_ASSIGN, 2
		case '>':
			return token.ARROW, 2
		}
		return token.SUB, 1
	case '*':
		if next(1) == '=' {
			return token.MUL_ASSIGN, 2
		}
		return token.MUL, 1
	case '/':
		if next(1) == '=' {
			return token.QUO_ASSIGN, 2
		}
		return token.QUO, 1
	case '%':
		if next(1) == '=' {
			return token.REM_ASSIGN, 2
		}
		return token.REM, 1
	case '&':
		switch next(1) {
		case '&':
			return token.LAND, 2
		case '=':
			return token.AND_ASSIGN, 2
		}
		return token.AND, 1
	case '|':
		switch next(1) {
		case '|':
			return token.LOR, 2
		case '=':
			return token.OR_ASSIGN, 2
		}
		return token.OR, 1
	case '^':
		if next(1) == '=' {
			return token.XOR_ASSIGN, 2
		}
		return token.XOR, 1
	case '~':
		return token.TILDE, 1
	case '?':
		return token.QUESTION, 1
	case ':':
		if next(1) == ':' {
			return token.COLONCOLON, 2
		}
		return token.COLON, 1
	case '.':
		if next(1) == '.' && next(2) == '.' {
			return token.ELLIPSIS, 3
		}
		return token.PERIOD, 1
	case '(':
		return token.LPAREN, 1
	case ')':
		return token.RPAREN, 1
	case '{':
		return token.LBRACE, 1
	case '}':
		return token.RBRACE, 1
	case '[':
		return token.LBRACK, 1
	case ']':
		return token.RBRACK, 1
	case ';':
		return token.SEMICOLON, 1
	case ',':
		return token.COMMA, 1
	case '@':
		return token.AT, 1
	}
	return token.ILLEGAL, 0
}

// skipTrivia consumes white space and comments, recording whether a gap and a
// line terminator occurred so the next token can carry FlagAdjacent/FlagNLBefore.
func (s *scanner) skipTrivia() {
	for s.off < len(s.src) {
		switch c := s.src[s.off]; c {
		case ' ', '\t', '\f':
			s.gap = true
			s.off++
		case '\n':
			s.gap, s.nl = true, true
			s.off++
		case '\r':
			s.gap, s.nl = true, true
			s.off++
			if s.off < len(s.src) && s.src[s.off] == '\n' {
				s.off++
			}
		case '/':
			if s.off+1 >= len(s.src) {
				return
			}
			switch s.src[s.off+1] {
			case '/':
				s.scanLineComment()
			case '*':
				s.scanBlockComment()
			default:
				return
			}
		default:
			return
		}
	}
}

func (s *scanner) emit(k token.Kind, c token.Ctx, pos, end int) {
	var fl token.Flags
	if !s.gap && len(s.toks) > 0 {
		fl |= token.FlagAdjacent
	}
	if s.nl {
		fl |= token.FlagNLBefore
	}
	s.toks = append(s.toks, token.Token{
		Kind:  k,
		Ctx:   c,
		Flags: fl,
		Pos:   s.f.Pos(pos),
		End:   s.f.Pos(end),
	})
	s.gap, s.nl = false, false
}

func (s *scanner) error(pos, end int, msg string) {
	if end <= pos {
		end = pos + 1 // invariant 3: never a zero-width diagnostic
	}
	if end > len(s.src) {
		end = len(s.src)
	}
	s.diags = append(s.diags, token.Diagnostic{
		Pos:      s.f.Pos(pos),
		End:      s.f.Pos(end),
		Severity: token.SevError,
		Msg:      msg,
	})
}

// illegalRune consumes one rune and reports it, so the loop always advances.
func (s *scanner) illegalRune(pos int) {
	_, w := utf8.DecodeRune(s.src[pos:])
	if w == 0 {
		w = 1
	}
	s.off = pos + w
	s.error(pos, s.off, "illegal character in input")
	s.emit(token.ILLEGAL, token.CtxNone, pos, s.off)
}

func (s *scanner) has(lit string) bool {
	return s.off+len(lit) <= len(s.src) && string(s.src[s.off:s.off+len(lit)]) == lit
}

func (s *scanner) at(i int) byte {
	if i < len(s.src) {
		return s.src[i]
	}
	return 0
}