package classfile

// writer is a big-endian byte sink with backpatching, the mirror of reader.
type writer struct{ b []byte }

func (w *writer) u1(v uint8)  { w.b = append(w.b, v) }
func (w *writer) u2(v uint16) { w.b = append(w.b, byte(v>>8), byte(v)) }
func (w *writer) u4(v uint32) {
	w.b = append(w.b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
func (w *writer) raw(b []byte) { w.b = append(w.b, b...) }
func (w *writer) len() int     { return len(w.b) }

func (w *writer) patchU2(off int, v uint16) {
	w.b[off] = byte(v >> 8)
	w.b[off+1] = byte(v)
}

func (w *writer) patchU4(off int, v uint32) {
	w.b[off] = byte(v >> 24)
	w.b[off+1] = byte(v >> 16)
	w.b[off+2] = byte(v >> 8)
	w.b[off+3] = byte(v)
}

// attr writes an attribute_info, backpatching attribute_length once body has
// run. The length excludes the six bytes of the header itself.
func (w *writer) attr(nameIdx uint16, body func(*writer)) {
	w.u2(nameIdx)
	lenOff := w.len()
	w.u4(0)
	start := w.len()
	body(w)
	w.patchU4(lenOff, uint32(w.len()-start))
}