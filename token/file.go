package token

import (
	"bytes"
	"sort"
	"unicode/utf8"
)

// Pos is a byte offset into a File's translated text, biased by one so that the
// zero value means "no position". Positions are per-unit and travel with their
// File: there is no FileSet and no cross-file address space (invariant 2).
type Pos uint32

const NoPos Pos = 0

func (p Pos) IsValid() bool { return p != NoPos }

// Position is a resolved, user-facing location. Offset, Line and Column all
// refer to the raw bytes the user typed, never to translated text.
type Position struct {
	Filename string
	Offset   int // 0-based, raw
	Line     int // 1-based
	Column   int // 1-based, in bytes
}

// edit records one Unicode escape: where its output lives in the translated
// text, and what it replaced in the raw source.
type edit struct {
	tOff, tLen int
	rOff, rLen int
}

// File owns one compilation unit's position space. It holds both the raw source
// and the translated text, plus the map between them, so that every span the
// front end produces resolves back to bytes the user typed.
type File struct {
	name  string
	raw   []byte
	text  []byte
	edits []edit // sorted by tOff; nil when the source has no escapes
	lines []int  // raw offsets of line starts; lines[0] == 0
	diags []Diagnostic
}

// NewFile performs escape translation and builds the offset map. This is step 1
// of the three-step lexical translation of §3.2 and happens before any
// tokenization: the character an escape produces does not itself participate in
// further escapes, so `\u000a` really is a line terminator and `\u0022` really
// is a quote.
func NewFile(name string, src []byte) *File {
	f := &File{name: name, raw: src}
	f.translate()
	// §3.5: a trailing ASCII SUB is ignored.
	if n := len(f.text); n > 0 && f.text[n-1] == 0x1A {
		f.text = f.text[:n-1]
	}
	f.buildLines()
	return f
}

func (f *File) Name() string        { return f.name }
func (f *File) Text() []byte        { return f.text }
func (f *File) Source() []byte      { return f.raw }
func (f *File) Size() int           { return len(f.text) }
func (f *File) LineCount() int      { return len(f.lines) }
func (f *File) Diagnostics() []Diagnostic { return f.diags }

// Pos converts a translated byte offset to a Pos. Offset is the inverse.
func (f *File) Pos(off int) Pos { return Pos(off + 1) }
func (f *File) Offset(p Pos) int {
	if p == NoPos {
		return -1
	}
	return int(p) - 1
}

// Slice returns the translated text of a span. This is what the scanner reads
// and what a literal decoder should be handed.
func (f *File) Slice(pos, end Pos) string {
	return string(f.text[f.Offset(pos):f.Offset(end)])
}

// Raw returns the bytes the user actually typed for a span. Where a span cuts
// through a Unicode escape, the result is widened to whole escapes.
func (f *File) Raw(pos, end Pos) string {
	a, b := f.RawSpan(pos, end)
	return string(f.raw[a:b])
}

// Between returns the translated text separating two tokens — white space and
// comments. Trivia recovery for formatters; the grammar never needs it.
func (f *File) Between(a, b Token) string {
	if a.End > b.Pos {
		return ""
	}
	return f.Slice(a.End, b.Pos)
}

// RawSpan maps a translated span to raw byte offsets.
func (f *File) RawSpan(pos, end Pos) (int, int) {
	a := f.rawStart(f.Offset(pos))
	b := f.rawEnd(f.Offset(end))
	if b < a {
		b = a
	}
	return a, b
}

func (f *File) rawStart(t int) int {
	if len(f.edits) == 0 {
		return t
	}
	i := sort.Search(len(f.edits), func(k int) bool { return f.edits[k].tOff > t }) - 1
	if i < 0 {
		return t
	}
	e := f.edits[i]
	if t < e.tOff+e.tLen {
		return e.rOff // inside an escape's output: point at the backslash
	}
	return e.rOff + e.rLen + (t - (e.tOff + e.tLen))
}

func (f *File) rawEnd(t int) int {
	if len(f.edits) == 0 {
		return t
	}
	// An end position exactly at an escape's start lies *before* that escape.
	i := sort.Search(len(f.edits), func(k int) bool { return f.edits[k].tOff >= t }) - 1
	if i < 0 {
		return t
	}
	e := f.edits[i]
	if t < e.tOff+e.tLen {
		return e.rOff + e.rLen // cuts through an escape: widen to the whole one
	}
	return e.rOff + e.rLen + (t - (e.tOff + e.tLen))
}

// Position resolves a Pos to a filename, raw offset, line and column.
func (f *File) Position(p Pos) Position {
	if !p.IsValid() {
		return Position{Filename: f.name, Offset: -1}
	}
	off := f.rawStart(f.Offset(p))
	i := sort.Search(len(f.lines), func(k int) bool { return f.lines[k] > off }) - 1
	if i < 0 {
		i = 0
	}
	return Position{
		Filename: f.name,
		Offset:   off,
		Line:     i + 1,
		Column:   off - f.lines[i] + 1,
	}
}

func (f *File) buildLines() {
	f.lines = append(f.lines[:0], 0)
	for i := 0; i < len(f.raw); i++ {
		switch f.raw[i] {
		case '\n':
			f.lines = append(f.lines, i+1)
		case '\r':
			if i+1 < len(f.raw) && f.raw[i+1] == '\n' {
				i++
			}
			f.lines = append(f.lines, i+1)
		}
	}
}

// translate implements §3.3. A backslash begins a Unicode escape only if it is
// preceded by an even number of contiguous backslashes, which is why `\\u2122`
// and `\u2122` translate differently.
func (f *File) translate() {
	raw := f.raw
	if bytes.IndexByte(raw, '\\') < 0 {
		f.text = raw // fast path: translated offsets are raw offsets
		return
	}
	text := make([]byte, 0, len(raw))
	i := 0
	for i < len(raw) {
		if raw[i] != '\\' {
			text = append(text, raw[i])
			i++
			continue
		}
		j := i
		for j < len(raw) && raw[j] == '\\' {
			j++
		}
		// Only the last backslash of a run can be followed by `u`, and it is
		// eligible exactly when the run length is odd.
		if (j-i)%2 == 0 || j == len(raw) || raw[j] != 'u' {
			text = append(text, raw[i:j]...)
			i = j
			continue
		}
		text = append(text, raw[i:j-1]...)
		i = j - 1

		r, next, ok := decodeEscape(raw, i)
		if !ok {
			start := len(text)
			text = append(text, '\\')
			f.diags = append(f.diags, Diagnostic{
				Pos:      Pos(start + 1),
				End:      Pos(start + 2),
				Severity: SevError,
				Msg:      "invalid Unicode escape",
			})
			i++
			continue
		}
		rOff, rNext := i, next
		// A surrogate pair is two escapes in the JLS's UTF-16 stream but one
		// code point in UTF-8, so pair them here when they are adjacent.
		if r >= 0xD800 && r <= 0xDBFF && next < len(raw) && raw[next] == '\\' {
			if lo, next2, ok2 := decodeEscape(raw, next); ok2 && lo >= 0xDC00 && lo <= 0xDFFF {
				r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
				rNext = next2
			}
		}
		tOff := len(text)
		text = appendRune(text, r)
		f.edits = append(f.edits, edit{tOff: tOff, tLen: len(text) - tOff, rOff: rOff, rLen: rNext - rOff})
		i = rNext
	}
	f.text = text
}

// decodeEscape reads `\ u {u} HexDigit x4` starting at the backslash.
func decodeEscape(raw []byte, i int) (rune, int, bool) {
	k := i + 1
	for k < len(raw) && raw[k] == 'u' {
		k++
	}
	if k == i+1 || k+4 > len(raw) {
		return 0, 0, false
	}
	v := 0
	for m := 0; m < 4; m++ {
		d := hexVal(raw[k+m])
		if d < 0 {
			return 0, 0, false
		}
		v = v<<4 | d
	}
	return rune(v), k + 4, true
}

func hexVal(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'a' <= c && c <= 'f':
		return int(c-'a') + 10
	case 'A' <= c && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// appendRune encodes an unpaired surrogate in three bytes rather than losing it
// to RuneError. The JLS permits a lone surrogate in the input stream, and the
// span must round-trip.
func appendRune(dst []byte, r rune) []byte {
	if r >= 0xD800 && r <= 0xDFFF {
		return append(dst,
			byte(0xE0|(r>>12)),
			byte(0x80|((r>>6)&0x3F)),
			byte(0x80|(r&0x3F)))
	}
	return utf8.AppendRune(dst, r)
}