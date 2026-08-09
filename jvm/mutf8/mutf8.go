// Package mutf8 implements the modified UTF-8 encoding used by JVMS §4.4.7.
//
// It differs from standard UTF-8 in two ways: U+0000 is encoded in the
// two-byte form so strings never contain an embedded NUL, and code points
// above U+FFFF are encoded as two three-byte surrogate halves rather than in
// the four-byte form. The .dex format uses the same encoding, so this package
// is shared by classfile and target/dalvik and must not depend on either.
package mutf8

import (
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// A SyntaxError reports a byte that cannot appear in a modified UTF-8 string.
type SyntaxError struct {
	Off  int    // byte offset within the string
	Byte byte   // the offending byte, if applicable
	Msg  string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("mutf8: at byte %d: %s", e.Off, e.Msg)
}

// Decode converts modified UTF-8 to a Go string.
//
// Lone surrogates are legal in JVM strings but cannot be represented in Go, so
// they decode to U+FFFD. Names and descriptors emitted by javac never contain
// them; only string literals conceivably can.
func Decode(b []byte) (string, error) {
	// Fast path. Class files are overwhelmingly ASCII, and this runs on every
	// Utf8 entry of every class, so it is worth the extra pass.
	simple := true
	for _, c := range b {
		if c == 0 || c >= 0x80 {
			simple = false
			break
		}
	}
	if simple {
		return string(b), nil
	}

	var sb strings.Builder
	sb.Grow(len(b))
	for i := 0; i < len(b); {
		x := b[i]
		switch {
		case x == 0:
			return "", &SyntaxError{Off: i, Byte: x, Msg: "NUL byte (must use the two-byte form)"}

		case x < 0x80:
			sb.WriteByte(x)
			i++

		case x&0xE0 == 0xC0:
			if i+1 >= len(b) {
				return "", &SyntaxError{Off: i, Byte: x, Msg: "truncated two-byte sequence"}
			}
			y := b[i+1]
			if y&0xC0 != 0x80 {
				return "", &SyntaxError{Off: i + 1, Byte: y, Msg: "bad continuation byte"}
			}
			r := rune(x&0x1F)<<6 | rune(y&0x3F)
			// C0 80 is the one legal overlong form: it is how U+0000 is
			// spelled. Anything else below U+0080 is a non-canonical encoding
			// of a character that has a one-byte form.
			if r != 0 && r < 0x80 {
				return "", &SyntaxError{Off: i, Byte: x, Msg: "overlong two-byte sequence"}
			}
			sb.WriteRune(r)
			i += 2

		case x&0xF0 == 0xE0:
			if i+2 >= len(b) {
				return "", &SyntaxError{Off: i, Byte: x, Msg: "truncated three-byte sequence"}
			}
			y, z := b[i+1], b[i+2]
			if y&0xC0 != 0x80 || z&0xC0 != 0x80 {
				return "", &SyntaxError{Off: i + 1, Msg: "bad continuation byte"}
			}
			r := rune(x&0x0F)<<12 | rune(y&0x3F)<<6 | rune(z&0x3F)
			if r < 0x800 {
				return "", &SyntaxError{Off: i, Byte: x, Msg: "overlong three-byte sequence"}
			}

			// A supplementary character is two three-byte sequences.
			if utf16.IsSurrogate(r) && i+5 < len(b) {
				x2, y2, z2 := b[i+3], b[i+4], b[i+5]
				if x2&0xF0 == 0xE0 && y2&0xC0 == 0x80 && z2&0xC0 == 0x80 {
					r2 := rune(x2&0x0F)<<12 | rune(y2&0x3F)<<6 | rune(z2&0x3F)
					if pair := utf16.DecodeRune(r, r2); pair != utf8.RuneError {
						sb.WriteRune(pair)
						i += 6
						continue
					}
				}
			}
			if utf16.IsSurrogate(r) {
				r = utf8.RuneError
			}
			sb.WriteRune(r)
			i += 3

		default: // 0xF0..0xFF
			return "", &SyntaxError{Off: i, Byte: x, Msg: "byte in range 0xF0-0xFF (four-byte UTF-8 is not permitted)"}
		}
	}
	return sb.String(), nil
}

// Encode converts a Go string to modified UTF-8.
func Encode(s string) []byte {
	out := make([]byte, 0, len(s)+len(s)/8+2)
	for _, r := range s {
		switch {
		case r >= 0x01 && r <= 0x7F:
			out = append(out, byte(r))
		case r == 0 || r <= 0x7FF:
			out = append(out, byte(0xC0|r>>6), byte(0x80|r&0x3F))
		case r <= 0xFFFF:
			out = append(out, triple(r)...)
		default:
			hi, lo := utf16.EncodeRune(r)
			out = append(out, triple(hi)...)
			out = append(out, triple(lo)...)
		}
	}
	return out
}

func triple(r rune) []byte {
	return []byte{byte(0xE0 | r>>12), byte(0x80 | r>>6&0x3F), byte(0x80 | r&0x3F)}
}

// EncodedLen returns len(Encode(s)) without allocating. The class file format
// caps a Utf8 entry at 65535 bytes, so writers must check this before emitting.
func EncodedLen(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case r >= 0x01 && r <= 0x7F:
			n++
		case r == 0 || r <= 0x7FF:
			n += 2
		case r <= 0xFFFF:
			n += 3
		default:
			n += 6
		}
	}
	return n
}