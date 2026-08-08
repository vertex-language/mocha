package scanner

import "github.com/vertex-language/mocha/token"

// frameStack tracks bracket nesting so an unclosed or stray delimiter is
// reported once, at the position that actually explains it, rather than as a
// cascade of surprises further down the file. It is advisory: the scanner never
// alters its tokenization based on the stack, and the parser does its own
// balanced skipping during recovery.
type frameStack struct {
	open     []frame
	reported bool // invariant 4: one recoverable diagnostic, never a cascade
}

type frame struct {
	kind token.Kind
	pos  int
}

var closerOf = map[token.Kind]token.Kind{
	token.LPAREN: token.RPAREN,
	token.LBRACK: token.RBRACK,
	token.LBRACE: token.RBRACE,
}

func (fs *frameStack) track(s *scanner, k token.Kind, pos, end int) {
	switch k {
	case token.LPAREN, token.LBRACK, token.LBRACE:
		fs.open = append(fs.open, frame{kind: k, pos: pos})
		return
	case token.RPAREN, token.RBRACK, token.RBRACE:
	default:
		return
	}

	if len(fs.open) == 0 {
		if !fs.reported {
			fs.reported = true
			s.error(pos, end, "unmatched "+k.String())
		}
		return
	}
	top := fs.open[len(fs.open)-1]
	if closerOf[top.kind] == k {
		fs.open = fs.open[:len(fs.open)-1]
		return
	}
	// A mismatch means one of the two is wrong and we cannot tell which. Blame
	// the opener, whose position is the useful one, pop it, and go quiet.
	if !fs.reported {
		fs.reported = true
		s.error(top.pos, top.pos+1, "unclosed "+top.kind.String()+
			", closed by "+k.String())
	}
	fs.open = fs.open[:len(fs.open)-1]
}

func (fs *frameStack) finish(s *scanner) {
	if len(fs.open) == 0 || fs.reported {
		return
	}
	fs.reported = true
	// The innermost unclosed opener is the one nearest the real mistake.
	top := fs.open[len(fs.open)-1]
	s.error(top.pos, top.pos+1, "unclosed "+top.kind.String()+" at end of file")
}