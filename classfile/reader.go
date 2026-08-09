package classfile

import "fmt"

// A SyntaxError reports a malformed class file, carrying the byte offset at
// which the problem was detected.
type SyntaxError struct {
	File string // the origin, if known
	Off  int    // byte offset into the class file
	Msg  string
}

func (e *SyntaxError) Error() string {
	where := e.File
	if where == "" {
		where = "class file"
	}
	return fmt.Sprintf("%s:0x%x: %s", where, e.Off, e.Msg)
}

// reader is a bounded big-endian cursor with a sticky error, so the decoder
// can read a whole structure and check once at the end. After the first
// failure every read returns a zero value and the offset stops advancing.
type reader struct {
	b    []byte
	off  int
	file string
	err  error
}

func (r *reader) fail(format string, args ...any) {
	if r.err == nil {
		r.err = &SyntaxError{File: r.file, Off: r.off, Msg: fmt.Sprintf(format, args...)}
	}
}

func (r *reader) short(n int, what string) bool {
	if r.err != nil {
		return true
	}
	if r.off+n > len(r.b) {
		r.fail("unexpected end of file reading %s (want %d bytes, have %d)", what, n, len(r.b)-r.off)
		return true
	}
	return false
}

func (r *reader) u1() uint8 {
	if r.short(1, "u1") {
		return 0
	}
	v := r.b[r.off]
	r.off++
	return v
}

func (r *reader) u2() uint16 {
	if r.short(2, "u2") {
		return 0
	}
	v := uint16(r.b[r.off])<<8 | uint16(r.b[r.off+1])
	r.off += 2
	return v
}

func (r *reader) u4() uint32 {
	if r.short(4, "u4") {
		return 0
	}
	v := uint32(r.b[r.off])<<24 | uint32(r.b[r.off+1])<<16 |
		uint32(r.b[r.off+2])<<8 | uint32(r.b[r.off+3])
	r.off += 4
	return v
}

func (r *reader) s4() int32 { return int32(r.u4()) }

// bytes returns a sub-slice of the input without copying. The returned slice
// aliases the class file bytes, so a Class keeps its input alive.
func (r *reader) bytes(n int) []byte {
	if n < 0 {
		r.fail("negative length %d", n)
		return nil
	}
	if r.short(n, "byte array") {
		return nil
	}
	v := r.b[r.off : r.off+n : r.off+n]
	r.off += n
	return v
}

func (r *reader) skip(n int) {
	if r.short(n, "padding") {
		return
	}
	r.off += n
}

func (r *reader) done() bool { return r.err != nil }