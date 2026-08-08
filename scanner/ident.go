package scanner

import (
	"unicode"
	"unicode/utf8"

	"github.com/vertex-language/mocha/token"
)

// scanIdent scans an IdentifierChars at pos and emits the resulting token,
// reporting whether it consumed anything. It resolves reserved keywords through
// token.Lookup (so '_' becomes UNDERSCORE, not IDENT) and tags contextual
// keywords with a token.Ctx without deciding anything.
func (s *scanner) scanIdent(pos int) bool {
	r, w := s.rune(pos)
	if !isJavaLetter(r) {
		return false
	}
	end := pos + w
	for end < len(s.src) {
		r, w := s.rune(end)
		if !isJavaLetterOrDigit(r) {
			break
		}
		end += w
	}
	s.off = end
	lit := string(s.src[pos:end])

	// The hyphenated contextual keyword. Its left adjacency is already implied:
	// a JavaLetterOrDigit before "non" would have been scanned into it.
	if lit == "non" && s.spliceNonSealed(pos) {
		return true
	}

	if k := token.Lookup(lit); k != token.IDENT {
		s.emit(k, token.CtxNone, pos, end)
		return true
	}
	// Everything else is an IDENT, including all sixteen non-hyphenated
	// contextual keywords. LookupCtx returns CtxNone for ordinary identifiers.
	s.emit(token.IDENT, token.LookupCtx(lit), pos, end)
	return true
}

// spliceNonSealed extends an already-scanned "non" into a single NON_SEALED
// token when '-' and "sealed" follow with nothing between and no
// JavaLetterOrDigit immediately after. Otherwise it leaves s.off alone.
func (s *scanner) spliceNonSealed(pos int) bool {
	const tail = "-sealed"
	if s.off+len(tail) > len(s.src) || string(s.src[s.off:s.off+len(tail)]) != tail {
		return false
	}
	after := s.off + len(tail)
	if after < len(s.src) {
		if r, _ := s.rune(after); isJavaLetterOrDigit(r) {
			return false // "non-sealedclass": three tokens
		}
	}
	s.off = after
	s.emit(token.NON_SEALED, token.CtxNonSealed, pos, after)
	return true
}

func (s *scanner) rune(i int) (rune, int) {
	if c := s.src[i]; c < utf8.RuneSelf {
		return rune(c), 1
	}
	return utf8.DecodeRune(s.src[i:])
}

// isJavaLetterStartByte is the ASCII fast path used to route into scanIdent.
func isJavaLetterStartByte(c byte) bool {
	return c == '$' || c == '_' ||
		('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

// isJavaLetter follows Character.isJavaIdentifierStart: letters, letter
// numbers, currency symbols (which is how '$' qualifies) and connecting
// punctuation (which is how '_' does).
func isJavaLetter(r rune) bool {
	if r < utf8.RuneSelf {
		return isJavaLetterStartByte(byte(r))
	}
	return unicode.IsLetter(r) || unicode.In(r, unicode.Nl, unicode.Sc, unicode.Pc)
}

// isJavaLetterOrDigit follows Character.isJavaIdentifierPart, which adds
// digits, combining marks and format characters.
func isJavaLetterOrDigit(r rune) bool {
	if r < utf8.RuneSelf {
		return isJavaLetterStartByte(byte(r)) || isDecDigit(byte(r))
	}
	return isJavaLetter(r) ||
		unicode.In(r, unicode.Nd, unicode.Mn, unicode.Mc, unicode.Cf)
}