package token

// Ctx names one of the seventeen contextual keywords of §3.9. The scanner
// attaches a Ctx to every IDENT whose spelling matches, and to the NON_SEALED
// token; it does not decide whether the occurrence is a keyword. That is the
// parser's job, and it turns on the production, not the spelling: a contextual
// keyword is recognized only where it appears as a terminal.
//
// The adjacency half of the §3.9 condition — no JavaLetterOrDigit immediately
// before or after — is already satisfied by anything that scanned as a single
// IdentifierChars, which is why `varfilename` never carries CtxVar.
type Ctx uint8

const (
	CtxNone Ctx = iota
	CtxExports
	CtxModule
	CtxNonSealed
	CtxOpen
	CtxOpens
	CtxPermits
	CtxProvides
	CtxRecord
	CtxRequires
	CtxSealed
	CtxTo
	CtxTransitive
	CtxUses
	CtxVar
	CtxWhen
	CtxWith
	CtxYield
	numCtx
)

var ctxStrings = [...]string{
	CtxNone:       "",
	CtxExports:    "exports",
	CtxModule:     "module",
	CtxNonSealed:  "non-sealed",
	CtxOpen:       "open",
	CtxOpens:      "opens",
	CtxPermits:    "permits",
	CtxProvides:   "provides",
	CtxRecord:     "record",
	CtxRequires:   "requires",
	CtxSealed:     "sealed",
	CtxTo:         "to",
	CtxTransitive: "transitive",
	CtxUses:       "uses",
	CtxVar:        "var",
	CtxWhen:       "when",
	CtxWith:       "with",
	CtxYield:      "yield",
}

func (c Ctx) String() string { return ctxStrings[c] }

var ctxTable = map[string]Ctx{}

func init() {
	for c := CtxNone + 1; c < numCtx; c++ {
		ctxTable[ctxStrings[c]] = c
	}
	if len(ctxTable) != 17 {
		panic("token: contextual keyword count is not 17")
	}
}

// LookupCtx returns the Ctx for a spelling, or CtxNone.
func LookupCtx(s string) Ctx {
	return ctxTable[s]
}

// §3.8 TypeIdentifier: Identifier but not permits, record, sealed, var, or yield.
func (c Ctx) BarredFromTypeIdentifier() bool {
	switch c {
	case CtxPermits, CtxRecord, CtxSealed, CtxVar, CtxYield:
		return true
	}
	return false
}

// §3.8 UnqualifiedMethodIdentifier: Identifier but not yield.
func (c Ctx) BarredFromMethodIdentifier() bool { return c == CtxYield }