# token

`package token` defines the lexical vocabulary of **Java SE 25** as [mocha](https://github.com/vertex-language/mocha) scans it, together with the per-compilation-unit position space that every span in the front end resolves through.

```
import "github.com/vertex-language/mocha/token"
```

```
go get github.com/vertex-language/mocha/token
```

The package holds no scanner and no parser. It is the vocabulary they share: `Kind`, `Ctx`, `Token`, `File`, `Diagnostic`.

---

## Invariants

1. **Nothing below the parser interprets.** A `Token` carries no text. Literals arrive undecoded; spans resolve through the `File` that produced them.
2. **No cross-file address space.** `Pos` is per-`File`. There is no `FileSet`, and a `Pos` from one file is meaningless in another.
3. **Every span is non-empty.** `End > Pos` always, including for `ILLEGAL`.

## Two departures from the JLS

- **`>` is never merged.** `SHR`, `USHR`, `SHR_ASSIGN` and `USHR_ASSIGN` exist as `Kind`s, but the scanner never emits them. The parser reassembles them from adjacent tokens with [`Join`](#join-the--rule). This is what lets `List<List<String>>` close its type arguments without lexer feedback.
- **Contextual keywords are `IDENT`.** All sixteen non-hyphenated contextual keywords of §3.9 scan as `IDENT` and carry a [`Ctx`](#contextual-keywords) tag. The scanner tags; the parser decides. `non-sealed` is the one exception — the hyphen forces a splice, so it gets its own `Kind`.

---

## `File` — source, translation, positions

`NewFile` performs Unicode escape translation (§3.2 step 1, §3.3) *before* any tokenization, and builds the map between translated text and raw bytes. The character an escape produces does not participate in further escapes, so `\u000a` really is a line terminator and `\u0022` really is a quote.

```go
package main

import (
	"fmt"

	"github.com/vertex-language/mocha/token"
)

func main() {
	src := []byte(`int \u0061 = 1;`)
	f := token.NewFile("A.java", src)

	fmt.Printf("%q\n", f.Text())   // "int a = 1;"   translated
	fmt.Printf("%q\n", f.Source()) // `int \u0061 = 1;`  raw

	// The identifier occupies one byte of translated text at offset 4.
	pos, end := f.Pos(4), f.Pos(5)

	fmt.Printf("%q\n", f.Slice(pos, end)) // "a"        what the scanner read
	fmt.Printf("%q\n", f.Raw(pos, end))   // `\u0061`   what the user typed

	p := f.Position(pos)
	fmt.Printf("%s:%d:%d\n", p.Filename, p.Line, p.Column) // A.java:1:5
}
```

`Slice` is what a literal decoder should be handed. `Raw` is what a diagnostic should underline — and where a span cuts through an escape, it widens to whole escapes rather than emitting half of one. `Position.Offset`, `Line` and `Column` all refer to raw bytes; `Column` is counted in bytes, not runes.

Backslash runs follow §3.3: a backslash begins an escape only when preceded by an even number of contiguous backslashes, which is why `\\u2122` and `\u2122` translate differently. Adjacent surrogate escapes are paired into one code point; an unpaired surrogate is preserved in three bytes rather than collapsing to `RuneError`, so spans round-trip.

Sources without a backslash take a fast path where translated offsets *are* raw offsets.

### Trivia

```go
gap := f.Between(prev, next) // white space and comments separating two tokens
```

The grammar never needs this. Formatters do.

---

## `Token`

```go
type Token struct {
	Kind  Kind
	Ctx   Ctx
	Flags Flags
	Pos   Pos // inclusive
	End   Pos // exclusive
}
```

`Flags` records two facts about the gap *before* the token:

| Flag | Meaning |
| --- | --- |
| `FlagAdjacent` | No white space and no comment separates this token from the previous one. |
| `FlagNLBefore` | A `LineTerminator` appeared before this token. |

Java has no automatic semicolon insertion, so `NLBefore` is for diagnostics and formatters only. `Adjacent` is load-bearing: both the `>` join and the `non-sealed` splice are legal only across adjacent tokens.

---

## `Kind`

`Kind` classifies a token: `ILLEGAL`, `EOF`, `COMMENT`, `IDENT`, the literal kinds, operators, separators, the fifty-one reserved character sequences of §3.9 (including `UNDERSCORE`), `NON_SEALED`, and the four parser-synthesized shift operators.

Three reserved words collide with literal kinds and are spelled differently in Go: `CHARK` (`char`), `FLOATK` (`float`), `INT_KW` (`int`).

```go
token.Lookup("class")   // token.CLASS
token.Lookup("sealed")  // token.IDENT — contextual, see below
token.Lookup("myVar")   // token.IDENT
token.Lookup("true")    // token.TRUE — excluded from Identifier by §3.8

token.CLASS.IsKeyword()      // true
token.SHR.IsSynthetic()      // true — never produced by the scanner
token.TEXTBLOCK.IsLiteral()  // true
```

`TEXTBLOCK` spans include their delimiters and are undecoded, per invariant 1.

---

## Contextual keywords

Seventeen `Ctx` values cover §3.9. The scanner attaches a `Ctx` to every `IDENT` whose spelling matches, and to `NON_SEALED`. It does not decide whether the occurrence *is* a keyword — that turns on the production, not the spelling.

```
exports  module  non-sealed  open   opens  permits  provides  record  requires
sealed   to      transitive  uses   var    when     with      yield
```

```go
tok := token.Token{Kind: token.IDENT, Ctx: token.LookupCtx("record")}

tok.Is(token.CtxRecord) // true — spells it
                        // whether it *is* a keyword here is the caller's call
```

The adjacency half of the §3.9 condition — no `JavaLetterOrDigit` immediately before or after — is already satisfied by anything that scanned as a single `IdentifierChars`, which is why `varfilename` never carries `CtxVar`.

### Restricted identifiers (§3.8)

```go
tok.IsTypeIdentifier()   // Identifier but not permits, record, sealed, var, yield
tok.IsMethodIdentifier() // Identifier but not yield
```

---

## `Join` — the `>` rule

Because the scanner never merges `>`, a parser at a shift position must ask for the compound form:

```go
// toks is the remaining token stream, toks[0].Kind == token.GTR
if k, n := token.Join(toks); k != token.ILLEGAL {
	op := k          // SHR, USHR, SHR_ASSIGN or USHR_ASSIGN
	toks = toks[n:]  // n is 2 or 3
	_ = op
}
```

Every token after the first must be adjacent, so `a > > b` in the source is a syntax error, not a shift. The join is greedy: `> > >` yields `USHR`, not `SHR` followed by `GTR`.

**Call `Join` only where a shift or assignment operator is admissible.** Inside `TypeArguments` the parser reads each `>` as a closing delimiter and must not consult it at all.

---

## Precedence

```go
func (k Kind) Precedence() int // LowestPrec (0) for non-binary operators
```

For precedence climbing. Higher binds tighter. Two operators need care:

- **`GTR` reports relational precedence**, because that is what a lone `>` is. At a shift position, call `Join` first and use the precedence of the *joined* kind — otherwise adjacent `> >` parses as a comparison.
- **`INSTANCEOF` shares the relational level** but takes a `ReferenceType` or a `Pattern` on its right, not an expression, so the operand loop has to break out for it.

Assignment, `? :` and lambda are right-associative and are not driven by this table.

```go
for {
	k := peek().Kind
	if k == token.GTR {
		if joined, n := token.Join(rest()); joined != token.ILLEGAL {
			k = joined
			_ = n
		}
	}
	prec := k.Precedence()
	if prec < minPrec {
		break
	}
	// ...
}
```

---

## Diagnostics

Escape translation is the one phase in this package that can report. Its diagnostics live on the `File`:

```go
f := token.NewFile("A.java", src)

diags := f.Diagnostics()
token.SortDiagnostics(diags)

for _, d := range diags {
	p := f.Position(d.Pos)
	fmt.Printf("%s:%d:%d: %s: %s\n", p.Filename, p.Line, p.Column, d.Severity, d.Msg)
}
```

`SortDiagnostics` orders by position, then extent, then message, and is stable — diagnostics reported at the same span keep the order in which the phases produced them.

---

## What lives elsewhere

Tokenization, the parser, and literal decoding are separate packages. This one defines what they agree on.