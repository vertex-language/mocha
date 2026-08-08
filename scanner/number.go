package scanner

import "github.com/vertex-language/mocha/token"

func isDecDigit(c byte) bool { return '0' <= c && c <= '9' }
func isOctDigit(c byte) bool { return '0' <= c && c <= '7' }
func isBinDigit(c byte) bool { return c == '0' || c == '1' }
func isHexDigit(c byte) bool {
	return isDecDigit(c) || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

// scanNumber scans an IntegerLiteral or FloatingPointLiteral starting at s.off
// and returns its Kind. The spelling is left undecoded: "1_024" stays five
// bytes and the underscores are validated, not removed.
func (s *scanner) scanNumber() token.Kind {
	start := s.off

	if s.at(start) == '0' {
		switch s.at(start + 1) {
		case 'x', 'X':
			return s.scanHexNumber(start)
		case 'b', 'B':
			s.off += 2
			ds := s.off
			s.digits(isBinDigit)
			s.checkRun(ds, s.off, isBinDigit, "binary")
			s.intSuffix()
			return token.INT
		}
	}

	isFloat := s.at(start) == '.'
	intStart, intEnd := start, start
	if !isFloat {
		s.digits(isDecDigit)
		intEnd = s.off
	}

	// A '.' is a fraction only when it is not the head of "...".
	if s.at(s.off) == '.' && !(s.at(s.off+1) == '.' && s.at(s.off+2) == '.') {
		isFloat = true
		s.off++
		fs := s.off
		s.digits(isDecDigit)
		if s.off > fs {
			s.checkRun(fs, s.off, isDecDigit, "decimal")
		} else if intEnd == intStart {
			s.error(start, s.off, "floating-point literal has no digits")
		}
	}

	if c := s.at(s.off); c == 'e' || c == 'E' {
		isFloat = true
		s.off++
		if c := s.at(s.off); c == '+' || c == '-' {
			s.off++
		}
		es := s.off
		s.digits(isDecDigit)
		if s.off == es {
			s.error(start, s.off, "exponent has no digits")
		} else {
			s.checkRun(es, s.off, isDecDigit, "decimal")
		}
	}

	switch s.at(s.off) {
	case 'f', 'F', 'd', 'D':
		s.off++
		isFloat = true
	case 'l', 'L':
		if isFloat {
			s.error(start, s.off+1, "floating-point literal cannot have an integer type suffix")
		}
		s.off++
	}

	if isFloat {
		return token.FLOAT
	}

	s.checkRun(intStart, intEnd, isDecDigit, "decimal")
	// An integer beginning with 0 and continuing is octal. OctalNumeral admits
	// leading underscores ("0_777") where HexNumeral and BinaryNumeral do not,
	// so the run is checked from after the underscores.
	if intEnd-intStart > 1 && s.src[intStart] == '0' {
		os := intStart + 1
		for os < intEnd && s.src[os] == '_' {
			os++
		}
		if os == intEnd {
			s.error(intStart, intEnd, "octal literal has no digits")
		} else {
			s.checkRun(os, intEnd, isOctDigit, "octal")
		}
	}
	return token.INT
}

func (s *scanner) scanHexNumber(start int) token.Kind {
	s.off += 2 // "0x"
	ds := s.off
	s.digits(isHexDigit)
	de := s.off

	hasDot := false
	if s.at(s.off) == '.' {
		hasDot = true
		s.off++
		fs := s.off
		s.digits(isHexDigit)
		if s.off > fs {
			s.checkRun(fs, s.off, isHexDigit, "hexadecimal")
		} else if de == ds {
			s.error(start, s.off, "hexadecimal literal has no digits")
		}
	}
	if de > ds {
		s.checkRun(ds, de, isHexDigit, "hexadecimal")
	} else if !hasDot {
		s.error(start, s.off, "hexadecimal literal has no digits")
	}

	if c := s.at(s.off); c == 'p' || c == 'P' {
		s.off++
		if c := s.at(s.off); c == '+' || c == '-' {
			s.off++
		}
		es := s.off
		s.digits(isDecDigit) // BinaryExponent takes SignedInteger, i.e. decimal
		if s.off == es {
			s.error(start, s.off, "binary exponent has no digits")
		} else {
			s.checkRun(es, s.off, isDecDigit, "decimal")
		}
		switch s.at(s.off) {
		case 'f', 'F', 'd', 'D':
			s.off++
		}
		return token.FLOAT
	}

	if hasDot {
		s.error(start, s.off, "hexadecimal floating-point literal requires a binary exponent")
		return token.FLOAT
	}
	s.intSuffix()
	return token.INT
}

func (s *scanner) digits(ok func(byte) bool) {
	for s.off < len(s.src) {
		c := s.src[s.off]
		if !ok(c) && c != '_' {
			// Consume a stray digit outside the radix so the whole malformed
			// literal gets one span and one diagnostic instead of splitting
			// into a number followed by an identifier.
			if !isDecDigit(c) {
				return
			}
		}
		s.off++
	}
}

func (s *scanner) intSuffix() {
	if c := s.at(s.off); c == 'l' || c == 'L' {
		s.off++
	}
}

// checkRun enforces the two rules every Digits production shares: underscores
// may sit only between digits, and every character must belong to the radix.
// At most one diagnostic per run.
func (s *scanner) checkRun(lo, hi int, ok func(byte) bool, radix string) {
	if lo >= hi {
		return
	}
	if s.src[lo] == '_' || s.src[hi-1] == '_' {
		s.error(lo, hi, "underscore may appear only between digits")
		return
	}
	for i := lo; i < hi; i++ {
		if c := s.src[i]; c != '_' && !ok(c) {
			s.error(i, i+1, "invalid digit in "+radix+" literal")
			return
		}
	}
}