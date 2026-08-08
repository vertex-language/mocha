package token

// Flags carries the two facts about a token's surroundings that survive
// scanning. Both are properties of the gap *before* the token.
type Flags uint8

const (
	// FlagAdjacent: no white space and no comment separates this token from the
	// one before it. The `>` join and the `non-sealed` splice are both legal
	// only across adjacent tokens, which is why this lives here and not in a
	// side table.
	FlagAdjacent Flags = 1 << iota

	// FlagNLBefore: a LineTerminator appeared before this token. Java has no
	// automatic semicolon insertion, so the grammar never consults this; it is
	// for diagnostics and formatters.
	FlagNLBefore
)

// Token is a scanned lexeme. It holds no text: spans resolve through the File
// that produced them (invariant 1 — nothing below the parser interprets).
//
// Pos is inclusive, End exclusive, and End > Pos always, including for ILLEGAL
// tokens (invariant 3).
type Token struct {
	Kind  Kind
	Ctx   Ctx
	Flags Flags
	_     uint8
	Pos   Pos
	End   Pos
}

func (t Token) Adjacent() bool { return t.Flags&FlagAdjacent != 0 }
func (t Token) NLBefore() bool { return t.Flags&FlagNLBefore != 0 }
func (t Token) Len() int       { return int(t.End - t.Pos) }

// Is reports whether t spells the given contextual keyword. It says nothing
// about whether the occurrence is a keyword here; the caller's production does.
func (t Token) Is(c Ctx) bool { return t.Ctx == c && t.Ctx != CtxNone }

// IsTypeIdentifier and IsMethodIdentifier implement the two restricted
// identifier nonterminals of §3.8.
func (t Token) IsTypeIdentifier() bool {
	return t.Kind == IDENT && !t.Ctx.BarredFromTypeIdentifier()
}

func (t Token) IsMethodIdentifier() bool {
	return t.Kind == IDENT && !t.Ctx.BarredFromMethodIdentifier()
}

// Join reports the compound shift operator spelled by the head of toks, and how
// many tokens it consumes.
//
// mocha's scanner never merges `>` with a following `>`, so the four compound
// forms of §15.19 and §15.26 arrive as separate tokens and are rejoined here.
// Every token after the first must be adjacent; `a > > b` in the source is a
// syntax error, not a shift.
//
// The join is greedy, so `> > >` yields USHR rather than SHR followed by GTR.
// Call it only where a shift or assignment operator is admissible — inside
// TypeArguments the parser reads each `>` as a closing delimiter and must not
// consult this function at all.
func Join(toks []Token) (Kind, int) {
	if len(toks) < 2 || toks[0].Kind != GTR || !toks[1].Adjacent() {
		return ILLEGAL, 0
	}
	switch toks[1].Kind {
	case GEQ: // > >=
		return SHR_ASSIGN, 2
	case GTR:
		if len(toks) >= 3 && toks[2].Adjacent() {
			switch toks[2].Kind {
			case GEQ: // > > >=
				return USHR_ASSIGN, 3
			case GTR: // > > >
				return USHR, 3
			}
		}
		return SHR, 2 // > >
	}
	return ILLEGAL, 0
}