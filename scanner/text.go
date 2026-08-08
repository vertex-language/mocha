package scanner

import "github.com/vertex-language/mocha/token"

// scanChar scans a CharacterLiteral. Because Unicode escapes were translated
// before tokenization, '\u000a' arrives here as a real line terminator inside
// the quotes and is reported as unterminated — which is exactly why it is not a
// valid character literal.
func (s *scanner) scanChar(pos int) {
	s.off++ // '
	n := 0
	for s.off < len(s.src) {
		c := s.src[s.off]
		if c == '\'' {
			s.off++
			if n == 0 {
				s.error(pos, s.off, "empty character literal")
			} else if n > 1 {
				s.error(pos, s.off, "character literal has more than one character")
			}
			s.emit(token.CHAR, token.CtxNone, pos, s.off)
			return
		}
		if c == '\n' || c == '\r' {
			break
		}
		if c == '\\' {
			s.escape(pos, false)
		} else {
			_, w := s.rune(s.off)
			s.off += w
		}
		n++
	}
	s.error(pos, s.off, "unterminated character literal")
	s.emit(token.CHAR, token.CtxNone, pos, s.off)
}

func (s *scanner) scanString(pos int) {
	s.off++ // "
	for s.off < len(s.src) {
		c := s.src[s.off]
		if c == '"' {
			s.off++
			s.emit(token.STRING, token.CtxNone, pos, s.off)
			return
		}
		if c == '\n' || c == '\r' {
			break
		}
		if c == '\\' {
			s.escape(pos, false)
			continue
		}
		_, w := s.rune(s.off)
		s.off += w
	}
	s.error(pos, s.off, "unterminated string literal")
	s.emit(token.STRING, token.CtxNone, pos, s.off)
}

// scanTextBlock scans a TextBlock. The token keeps the raw span, delimiters
// included: normalizing line terminators, stripping incidental whitespace and
// interpreting escapes are three later transformations, in that order, and none
// of them happens here.
func (s *scanner) scanTextBlock(pos int) {
	s.off += 3 // """

	// {TextBlockWhiteSpace} LineTerminator
	for {
		c := s.at(s.off)
		if c == ' ' || c == '\t' || c == '\f' {
			s.off++
			continue
		}
		break
	}
	switch s.at(s.off) {
	case '\n':
		s.off++
	case '\r':
		s.off++
		if s.at(s.off) == '\n' {
			s.off++
		}
	default:
		s.error(pos, s.off, "text block opening delimiter must be followed by a line terminator")
	}

	for s.off < len(s.src) {
		switch s.src[s.off] {
		case '\\':
			// A backslash escapes the next character, including a line
			// terminator (the continuation form, legal only here).
			s.off += 2
			if s.off > len(s.src) {
				s.off = len(s.src)
			}
		case '"':
			if s.has(`"""`) {
				s.off += 3
				s.emit(token.TEXTBLOCK, token.CtxNone, pos, s.off)
				return
			}
			s.off++
		default:
			_, w := s.rune(s.off)
			s.off += w
		}
	}
	s.error(pos, s.off, "unterminated text block")
	s.emit(token.TEXTBLOCK, token.CtxNone, pos, s.off)
}

// escape consumes one EscapeSequence at s.off, which is known to be '\'.
// litPos is the start of the enclosing literal, used only for spans.
func (s *scanner) escape(litPos int, inTextBlock bool) {
	start := s.off
	s.off++
	if s.off >= len(s.src) {
		s.error(start, s.off, "incomplete escape sequence")
		return
	}
	c := s.src[s.off]
	switch c {
	case 'b', 's', 't', 'n', 'f', 'r', '"', '\'', '\\':
		s.off++
		return
	case '0', '1', '2', '3', '4', '5', '6', '7':
		// OctalEscape: one to three digits, three only when the first is 0-3.
		s.off++
		if isOctDigit(s.at(s.off)) {
			s.off++
			if c <= '3' && isOctDigit(s.at(s.off)) {
				s.off++
			}
		}
		return
	case '\n', '\r':
		if !inTextBlock {
			s.error(start, s.off+1, "line continuation is permitted only in a text block")
		}
		s.off++
		if c == '\r' && s.at(s.off) == '\n' {
			s.off++
		}
		return
	}
	_, w := s.rune(s.off)
	s.off += w
	s.error(start, s.off, "invalid escape sequence")
}

func (s *scanner) scanLineComment() {
	pos := s.off
	s.off += 2
	for s.off < len(s.src) && s.src[s.off] != '\n' && s.src[s.off] != '\r' {
		s.off++
	}
	s.comment(pos, s.off, false)
}

func (s *scanner) scanBlockComment() {
	pos := s.off
	s.off += 2
	multiline := false
	for s.off < len(s.src) {
		switch s.src[s.off] {
		case '*':
			if s.at(s.off+1) == '/' {
				s.off += 2
				s.comment(pos, s.off, multiline)
				return
			}
			s.off++
		case '\n', '\r':
			multiline = true
			s.off++
		default:
			s.off++
		}
	}
	s.error(pos, s.off, "unterminated comment")
	s.comment(pos, s.off, multiline)
}

func (s *scanner) comment(pos, end int, multiline bool) {
	if s.mode&ScanComments != 0 {
		var fl token.Flags
		if !s.gap && len(s.toks) > 0 {
			fl |= token.FlagAdjacent
		}
		if s.nl {
			fl |= token.FlagNLBefore
		}
		s.toks = append(s.toks, token.Token{
			Kind:  token.COMMENT,
			Flags: fl,
			Pos:   s.f.Pos(pos),
			End:   s.f.Pos(end),
		})
	}
	// A comment is a gap whether or not it is kept, so the token after it is
	// never adjacent — which is what makes the '>' rejoin and the "non-sealed"
	// splice refuse to cross one.
	s.gap = true
	if multiline {
		s.nl = true
	}
}