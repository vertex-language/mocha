# scanner

`package scanner` turns a `*token.File` into a complete token slice.

```
import "github.com/vertex-language/mocha/scanner"
```

```
go get github.com/vertex-language/mocha/scanner
```

It tokenizes the whole unit up front and never stops early. Every scan path advances at least one byte, and malformed input yields an exact span plus **one** diagnostic rather than a cascade. Nothing here interprets: literals keep their raw spelling, text blocks keep their delimiters and their incidental whitespace, and contextual keywords are tagged with a `token.Ctx` for the parser to accept or reject per production.

---

## Usage

```go
package main

import (
	"fmt"
	"os"

	"github.com/vertex-language/mocha/scanner"
	"github.com/vertex-language/mocha/token"
)

func main() {
	src, err := os.ReadFile("A.java")
	if err != nil {
		panic(err)
	}

	f := token.NewFile("A.java", src)
	toks, diags := scanner.Scan(f, 0)

	for _, d := range diags {
		p := f.Position(d.Pos)
		fmt.Printf("%s:%d:%d: %s: %s\n", p.Filename, p.Line, p.Column, d.Severity, d.Msg)
	}

	for _, t := range toks {
		if t.Kind == token.EOF {
			break
		}
		p := f.Position(t.Pos)
		fmt.Printf("%d:%d\t%-10s\t%q\n", p.Line, p.Column, t.Kind, f.Slice(t.Pos, t.End))
	}
}
```

`Scan` is the entire API surface: one function, one `Mode`.

```go
func Scan(f *token.File, mode Mode) ([]token.Token, []token.Diagnostic)
```

The returned slice always ends in an `EOF` token — the one token with a zero-width span. Diagnostics are sorted, and diagnostics produced during escape translation (inside `token.NewFile`) are already merged in, so the caller never has to combine two slices.

### Mode

```go
toks, diags := scanner.Scan(f, scanner.ScanComments)
```

`ScanComments` keeps `COMMENT` tokens in the stream. Without it, comments are consumed as trivia. Either way a comment **breaks adjacency**, and its text remains reachable through `token.File.Between`, so a formatter loses nothing by scanning without it.

---

## Two rules that are not the JLS's

### `>` is never combined with a following `>`

The split is unconditional. mocha has no lexical notion of type context, so the scanner does not need one. Longest match still governs `>=`:

| Source | Tokens |
| --- | --- |
| `>` | `GTR` |
| `>=` | `GEQ` |
| `>>` | `GTR` `GTR` |
| `>>=` | `GTR` `GEQ` |
| `>>>` | `GTR` `GTR` `GTR` |
| `>>>=` | `GTR` `GTR` `GEQ` |

The parser rejoins adjacent runs with `token.Join`, which is why `FlagAdjacent` is on the token and not in a side table. `List<List<String>>` closes without lexer feedback; `a > > b` stays a syntax error rather than becoming a shift.

### `non-sealed` is spliced

The hyphen means this contextual keyword cannot survive as an `IDENT`, so the scanner assembles it — but only when nothing separates the three pieces and no `JavaLetterOrDigit` abuts either end.

```java
non-sealed class C {}   // NON_SEALED  CLASS  IDENT ...
non-sealedclass         // IDENT("non")  SUB  IDENT("sealedclass")
non - sealed            // IDENT("non")  SUB  IDENT("sealed")
```

Left adjacency comes for free: a `JavaLetterOrDigit` before `non` would have been scanned into it.

---

## What the scanner does not decide

Reserved words resolve through `token.Lookup`, so `_` becomes `UNDERSCORE` rather than `IDENT`. Everything else that scans as `IdentifierChars` is an `IDENT`, including all sixteen non-hyphenated contextual keywords, which are **tagged** with a `token.Ctx` and nothing more.

```go
// `record` in an expression position is just a variable name.
tok.Kind          // token.IDENT
tok.Is(token.CtxRecord) // true — spells it; the production decides
```

Identifier characters follow `Character.isJavaIdentifierStart` and `isJavaIdentifierPart`: letters, letter numbers, currency symbols (how `$` qualifies), connecting punctuation (how `_` does), plus digits, combining marks and format characters for the continuation set. ASCII takes a fast path.

---

## Literals stay undecoded

`1_024` stays five bytes. Underscores are **validated, not removed**. Text blocks keep their `"""` delimiters and every byte of incidental whitespace — normalizing line terminators, stripping incidental whitespace and interpreting escapes are three later transformations, in that order, and none of them happens here.

Numeric scanning enforces what every `Digits` production shares: underscores only between digits, every character in the radix, at most one diagnostic per run. A few cases are worth knowing:

- **`.` is a fraction only when it is not the head of `...`**, so `1...2` does not swallow the ellipsis.
- **A stray decimal digit outside the radix is consumed**, so `0b1012` produces one malformed literal with one diagnostic instead of a number followed by an identifier.
- **`OctalNumeral` admits leading underscores** where `HexNumeral` and `BinaryNumeral` do not, so `0_777` is checked from after the underscores.
- **`0x1.8` without a `p` exponent** is reported: hexadecimal floating-point requires a binary exponent.
- **A binary exponent takes a `SignedInteger`**, i.e. decimal digits, not hex.

### Escape translation happens first

Because `token.NewFile` translates Unicode escapes before tokenization, `'\u000a'` arrives at the scanner as a real line terminator between the quotes — and is reported as an unterminated character literal, which is exactly why it is not valid Java. The scanner never sees a `\uXXXX`.

`\` followed by a line terminator is a continuation, legal only inside a text block, and reported anywhere else.

---

## Diagnostics and recovery

Every span the scanner reports is non-empty (invariant 3) and clamped to the source. Unterminated literals still emit a token covering what was consumed, so the parser gets a stream with the shape it expected.

Bracket nesting is tracked by an advisory `frameStack` — the scanner never alters its tokenization based on it, and the parser does its own balanced skipping during recovery. Its whole purpose is invariant 4: **one recoverable diagnostic, never a cascade.**

- A closer with nothing open → `unmatched )`, once.
- A mismatch → blame the *opener*, whose position is the useful one, then go quiet. `unclosed {, closed by )`.
- End of file with openers left → the *innermost* one, which is nearest the real mistake.

After the first such report the stack stops talking entirely. One brace typo in a large file produces one message, not one per method.

---

## Relationship to `token`

`scanner` produces; [`token`](../token) defines. Positions, spans, `Kind`, `Ctx`, `Flags`, `Join` and `Precedence` all live there, and every span produced here resolves back to raw source through the same `*token.File` that was passed in.