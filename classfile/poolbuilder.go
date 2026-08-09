package classfile

import (
	"fmt"
	"math"

	"github.com/vertex-language/mocha/jvm/mutf8"
)

// MaxPoolIndex is the highest usable constant pool index; constant_pool_count
// is a u2, so the table holds at most 65534 entries.
const MaxPoolIndex = 65534

// A PoolBuilder interns constant pool entries and emits the table.
//
// Interning is the whole API. Every writer method takes symbolic values, adds
// whatever entries are missing, and returns an index — callers never manage
// indices. target/dalvik needs this same shape for its string, type and
// method ID pools, which is the main reason to build it here first against a
// format javap can check.
type PoolBuilder struct {
	slots [][]byte // encoded entries; index 0 and Long/Double phantoms are nil
	err   error

	utf8  map[string]uint16
	class map[string]uint16
	str   map[string]uint16
	mt    map[string]uint16
	nat   map[[2]string]uint16
	ref   map[[4]string]uint16
	i32   map[int32]uint16
	i64   map[int64]uint16
	f32   map[uint32]uint16 // keyed on bits, not value
	f64   map[uint64]uint16
}

func newPoolBuilder() *PoolBuilder {
	return &PoolBuilder{
		slots: make([][]byte, 1), // index 0 is the reserved hole
		utf8:  map[string]uint16{},
		class: map[string]uint16{},
		str:   map[string]uint16{},
		mt:    map[string]uint16{},
		nat:   map[[2]string]uint16{},
		ref:   map[[4]string]uint16{},
		i32:   map[int32]uint16{},
		i64:   map[int64]uint16{},
		f32:   map[uint32]uint16{},
		f64:   map[uint64]uint16{},
	}
}

// Err returns the first error, if any. Errors are sticky: after a failure
// every index returned is 0 and the class will not be emitted.
func (p *PoolBuilder) Err() error { return p.err }

func (p *PoolBuilder) fail(format string, args ...any) uint16 {
	if p.err == nil {
		p.err = fmt.Errorf("classfile: constant pool: "+format, args...)
	}
	return 0
}

// add appends an entry, taking wide slots for the 8-byte constants.
func (p *PoolBuilder) add(b []byte, wide bool) uint16 {
	if p.err != nil {
		return 0
	}
	n := len(p.slots)
	need := 1
	if wide {
		need = 2
	}
	if n+need-1 > MaxPoolIndex {
		return p.fail("more than %d entries", MaxPoolIndex)
	}
	p.slots = append(p.slots, b)
	if wide {
		p.slots = append(p.slots, nil) // the unusable second half
	}
	return uint16(n)
}

// UTF8 interns a CONSTANT_Utf8_info.
func (p *PoolBuilder) UTF8(s string) uint16 {
	if i, ok := p.utf8[s]; ok {
		return i
	}
	// EncodedLen first: the overwhelming majority of strings are fine, and
	// this avoids encoding a string only to discard it.
	if n := mutf8.EncodedLen(s); n > 65535 {
		return p.fail("Utf8 constant is %d bytes, over the 65535 limit", n)
	}
	enc := mutf8.Encode(s)
	var w writer
	w.u1(uint8(TagUtf8))
	w.u2(uint16(len(enc)))
	w.raw(enc)
	i := p.add(w.b, false)
	p.utf8[s] = i
	return i
}

// Class interns a CONSTANT_Class_info. The name is in internal form; for an
// array class it is the array descriptor, e.g. "[[I".
func (p *PoolBuilder) Class(internal string) uint16 {
	if i, ok := p.class[internal]; ok {
		return i
	}
	n := p.UTF8(internal)
	i := p.addRef1(TagClass, n)
	p.class[internal] = i
	return i
}

// String interns a CONSTANT_String_info.
func (p *PoolBuilder) String(s string) uint16 {
	if i, ok := p.str[s]; ok {
		return i
	}
	n := p.UTF8(s)
	i := p.addRef1(TagString, n)
	p.str[s] = i
	return i
}

// MethodType interns a CONSTANT_MethodType_info.
func (p *PoolBuilder) MethodType(descriptor string) uint16 {
	if i, ok := p.mt[descriptor]; ok {
		return i
	}
	i := p.addRef1(TagMethodType, p.UTF8(descriptor))
	p.mt[descriptor] = i
	return i
}

// NameAndType interns a CONSTANT_NameAndType_info.
func (p *PoolBuilder) NameAndType(name, descriptor string) uint16 {
	key := [2]string{name, descriptor}
	if i, ok := p.nat[key]; ok {
		return i
	}
	n, d := p.UTF8(name), p.UTF8(descriptor)
	i := p.addRef2(TagNameAndType, n, d)
	p.nat[key] = i
	return i
}

// FieldRef interns a CONSTANT_Fieldref_info.
func (p *PoolBuilder) FieldRef(owner, name, descriptor string) uint16 {
	return p.memberRef(TagFieldref, owner, name, descriptor)
}

// MethodRef interns a CONSTANT_Methodref_info.
func (p *PoolBuilder) MethodRef(owner, name, descriptor string) uint16 {
	return p.memberRef(TagMethodref, owner, name, descriptor)
}

// InterfaceMethodRef interns a CONSTANT_InterfaceMethodref_info.
func (p *PoolBuilder) InterfaceMethodRef(owner, name, descriptor string) uint16 {
	return p.memberRef(TagInterfaceMethodref, owner, name, descriptor)
}

func (p *PoolBuilder) memberRef(tag Tag, owner, name, descriptor string) uint16 {
	key := [4]string{string(rune(tag)), owner, name, descriptor}
	if i, ok := p.ref[key]; ok {
		return i
	}
	c := p.Class(owner)
	nt := p.NameAndType(name, descriptor)
	i := p.addRef2(tag, c, nt)
	p.ref[key] = i
	return i
}

// Int interns a CONSTANT_Integer_info.
func (p *PoolBuilder) Int(v int32) uint16 {
	if i, ok := p.i32[v]; ok {
		return i
	}
	var w writer
	w.u1(uint8(TagInteger))
	w.u4(uint32(v))
	i := p.add(w.b, false)
	p.i32[v] = i
	return i
}

// Float interns a CONSTANT_Float_info.
//
// Interning is keyed on the bit pattern rather than the value. Two NaNs with
// different payloads are distinct constants that compare unequal, and +0.0 and
// -0.0 compare equal but are distinct constants; keying on the float would
// merge or duplicate the wrong ones.
func (p *PoolBuilder) Float(v float32) uint16 {
	bits := math.Float32bits(v)
	if i, ok := p.f32[bits]; ok {
		return i
	}
	var w writer
	w.u1(uint8(TagFloat))
	w.u4(bits)
	i := p.add(w.b, false)
	p.f32[bits] = i
	return i
}

// Long interns a CONSTANT_Long_info, which occupies two slots.
func (p *PoolBuilder) Long(v int64) uint16 {
	if i, ok := p.i64[v]; ok {
		return i
	}
	var w writer
	w.u1(uint8(TagLong))
	w.u4(uint32(uint64(v) >> 32))
	w.u4(uint32(uint64(v)))
	i := p.add(w.b, true)
	p.i64[v] = i
	return i
}

// Double interns a CONSTANT_Double_info, which occupies two slots.
func (p *PoolBuilder) Double(v float64) uint16 {
	bits := math.Float64bits(v)
	if i, ok := p.f64[bits]; ok {
		return i
	}
	var w writer
	w.u1(uint8(TagDouble))
	w.u4(uint32(bits >> 32))
	w.u4(uint32(bits))
	i := p.add(w.b, true)
	p.f64[bits] = i
	return i
}

func (p *PoolBuilder) addRef1(tag Tag, a uint16) uint16 {
	var w writer
	w.u1(uint8(tag))
	w.u2(a)
	return p.add(w.b, false)
}

func (p *PoolBuilder) addRef2(tag Tag, a, b uint16) uint16 {
	var w writer
	w.u1(uint8(tag))
	w.u2(a)
	w.u2(b)
	return p.add(w.b, false)
}

// count is constant_pool_count: one more than the highest index in use.
func (p *PoolBuilder) count() uint16 { return uint16(len(p.slots)) }

func (p *PoolBuilder) emit(w *writer) {
	w.u2(p.count())
	for _, b := range p.slots[1:] {
		if b != nil {
			w.raw(b)
		}
	}
}