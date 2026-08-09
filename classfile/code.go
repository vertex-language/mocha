package classfile

import (
	"fmt"

	"github.com/vertex-language/mocha/jvm/op"
)

// An Instr is one decoded instruction. It holds a slice of the code array
// rather than unpacked operands, so iterating a method allocates nothing.
type Instr struct {
	Op   op.Op
	PC   uint32 // offset of the opcode within the code array
	Wide bool   // the instruction was preceded by a wide prefix
	raw  []byte // the whole instruction, opcode included
}

// Len is the encoded length in bytes.
func (in Instr) Len() int { return len(in.raw) }

// operands returns the bytes after the opcode, skipping a wide prefix.
func (in Instr) operands() []byte {
	if in.Wide {
		return in.raw[2:]
	}
	return in.raw[1:]
}

// Index returns the constant pool index operand. It is valid for the field,
// invoke, ldc, new, anewarray, checkcast, instanceof and multianewarray
// opcodes; anything else returns 0.
func (in Instr) Index() uint16 {
	b := in.operands()
	switch in.Op.Kind() {
	case op.ConstU1:
		return uint16(b[0])
	case op.ConstU2, op.MultiK, op.InterfaceK, op.DynamicK:
		return be16(b)
	}
	return 0
}

// Local returns the local variable slot, honouring a wide prefix.
func (in Instr) Local() uint16 {
	b := in.operands()
	switch in.Op.Kind() {
	case op.Local, op.IincK:
		if in.Wide {
			return be16(b)
		}
		return uint16(b[0])
	}
	// The implicit forms encode the slot in the opcode itself.
	switch o := in.Op; {
	case o >= op.Iload0 && o <= op.Aload3:
		return uint16((o - op.Iload0) % 4)
	case o >= op.Istore0 && o <= op.Astore3:
		return uint16((o - op.Istore0) % 4)
	}
	return 0
}

// Increment returns the signed delta of an iinc instruction.
func (in Instr) Increment() int32 {
	if in.Op != op.Iinc {
		return 0
	}
	b := in.operands()
	if in.Wide {
		return int32(int16(be16(b[2:])))
	}
	return int32(int8(b[1]))
}

// Immediate returns the operand of bipush or sipush.
func (in Instr) Immediate() int32 {
	b := in.operands()
	switch in.Op {
	case op.Bipush:
		return int32(int8(b[0]))
	case op.Sipush:
		return int32(int16(be16(b)))
	}
	return 0
}

// Target returns the absolute branch destination within the code array.
func (in Instr) Target() uint32 {
	b := in.operands()
	switch in.Op.Kind() {
	case op.Branch2:
		return uint32(int32(in.PC) + int32(int16(be16(b))))
	case op.Branch4:
		return uint32(int32(in.PC) + int32(be32(b)))
	}
	return 0
}

// ArrayType returns the primitive type code of a newarray instruction.
func (in Instr) ArrayType() uint8 {
	if in.Op != op.Newarray {
		return 0
	}
	return in.operands()[0]
}

// Dimensions returns the dimension count of a multianewarray instruction.
func (in Instr) Dimensions() uint8 {
	if in.Op != op.Multianewarray {
		return 0
	}
	return in.operands()[2]
}

// A SwitchCase pairs a match value with an absolute target.
type SwitchCase struct {
	Match  int32
	Target uint32
}

// Switch decodes tableswitch or lookupswitch into a default target and cases.
// Both are 4-byte aligned relative to the start of the code array, which is
// why an instruction's length cannot be derived from its opcode alone.
//
// The entry counts are computed in int64 for the same reason instrLen does:
// high-low+1 and npairs*8 both overflow int32 for hostile inputs. An Instr
// produced by Iter has already been validated, but Instr is a plain struct
// and nothing stops a caller building one by hand.
func (in Instr) Switch() (defaultTarget uint32, cases []SwitchCase) {
	pad := (4 - (in.PC+1)%4) % 4
	b := in.raw[1+pad:]
	if len(b) < 4 {
		return 0, nil
	}
	base := int32(in.PC)
	defaultTarget = uint32(base + be32(b))

	switch in.Op {
	case op.Tableswitch:
		if len(b) < 12 {
			return defaultTarget, nil
		}
		low, high := be32(b[4:]), be32(b[8:])
		if high < low {
			return defaultTarget, nil
		}
		n := int64(high) - int64(low) + 1
		if 12+4*n > int64(len(b)) {
			return defaultTarget, nil
		}
		cases = make([]SwitchCase, 0, n)
		for i := int64(0); i < n; i++ {
			off := be32(b[12+4*i:])
			cases = append(cases, SwitchCase{Match: low + int32(i), Target: uint32(base + off)})
		}
	case op.Lookupswitch:
		if len(b) < 8 {
			return defaultTarget, nil
		}
		n := int64(be32(b[4:]))
		if n < 0 || 8+8*n > int64(len(b)) {
			return defaultTarget, nil
		}
		cases = make([]SwitchCase, 0, n)
		for i := int64(0); i < n; i++ {
			match := be32(b[8+8*i:])
			off := be32(b[12+8*i:])
			cases = append(cases, SwitchCase{Match: match, Target: uint32(base + off)})
		}
	}
	return defaultTarget, cases
}

func (in Instr) String() string {
	switch in.Op.Kind() {
	case op.None:
		return in.Op.Name()
	case op.ConstU1, op.ConstU2, op.MultiK, op.InterfaceK, op.DynamicK:
		return fmt.Sprintf("%s #%d", in.Op, in.Index())
	case op.Local:
		return fmt.Sprintf("%s %d", in.Op, in.Local())
	case op.IincK:
		return fmt.Sprintf("%s %d, %d", in.Op, in.Local(), in.Increment())
	case op.Branch2, op.Branch4:
		return fmt.Sprintf("%s %d", in.Op, in.Target())
	case op.Byte1, op.Short2:
		return fmt.Sprintf("%s %d", in.Op, in.Immediate())
	}
	return in.Op.Name()
}

// Iter walks the instructions of a code array in encoding order.
type Iter struct {
	code []byte
	pc   uint32
	cur  Instr
	err  error
}

// Iter returns an iterator over the method body.
func (c *Code) Iter() *Iter { return &Iter{code: c.Bytes} }

// Instr returns the instruction decoded by the last call to Next.
func (it *Iter) Instr() Instr { return it.cur }

// Err returns the decoding error that stopped iteration, if any. Always check
// it after the loop: a malformed code array ends iteration early.
func (it *Iter) Err() error { return it.err }

// Next advances to the next instruction, reporting whether one was decoded.
func (it *Iter) Next() bool {
	if it.err != nil || int(it.pc) >= len(it.code) {
		return false
	}
	pc := it.pc
	o := op.Op(it.code[pc])
	if !o.Valid() {
		it.failf(pc, "unknown or reserved opcode 0x%02x", uint8(o))
		return false
	}

	wide := false
	if o == op.Wide {
		if int(pc)+1 >= len(it.code) {
			it.failf(pc, "wide prefix at end of code array")
			return false
		}
		inner := op.Op(it.code[pc+1])
		switch inner.Kind() {
		case op.Local, op.IincK:
		default:
			it.failf(pc, "wide prefix applied to %s, which takes no local index", inner)
			return false
		}
		wide = true
		o = inner
	}

	n, err := instrLen(it.code, pc, o, wide)
	if err != nil {
		it.err = err
		return false
	}
	if int(pc)+n > len(it.code) {
		it.failf(pc, "%s runs %d bytes past the end of the code array", o, int(pc)+n-len(it.code))
		return false
	}

	it.cur = Instr{Op: o, PC: pc, Wide: wide, raw: it.code[pc : int(pc)+n]}
	it.pc = pc + uint32(n)
	return true
}

func (it *Iter) failf(pc uint32, format string, args ...any) {
	it.err = fmt.Errorf("code+%d: %s", pc, fmt.Sprintf(format, args...))
}

// instrLen computes the encoded length of the instruction at pc.
func instrLen(code []byte, pc uint32, o op.Op, wide bool) (int, error) {
	if wide {
		if o == op.Iinc {
			return 6, nil // wide, iinc, u2 index, s2 delta
		}
		return 4, nil // wide, op, u2 index
	}
	if n := o.Len(); n >= 0 {
		return n, nil
	}

	// Only the two switches remain, and both pad the byte after the opcode up
	// to the next 4-byte boundary measured from the start of the code array.
	pad := int((4 - (pc+1)%4) % 4)
	base := int(pc) + 1 + pad
	need := func(n int) error {
		if base+n > len(code) {
			return fmt.Errorf("code+%d: truncated %s", pc, o)
		}
		return nil
	}

	switch o {
	case op.Tableswitch:
		if err := need(12); err != nil {
			return 0, err
		}
		low, high := be32(code[base+4:]), be32(code[base+8:])
		if high < low {
			return 0, fmt.Errorf("code+%d: tableswitch high %d is below low %d", pc, high, low)
		}
		n := int64(high) - int64(low) + 1
		if n > int64(len(code)) {
			return 0, fmt.Errorf("code+%d: tableswitch claims %d entries", pc, n)
		}
		return 1 + pad + 12 + 4*int(n), nil

	case op.Lookupswitch:
		if err := need(8); err != nil {
			return 0, err
		}
		n := be32(code[base+4:])
		if n < 0 || int64(n)*8 > int64(len(code)) {
			return 0, fmt.Errorf("code+%d: lookupswitch claims %d pairs", pc, n)
		}
		return 1 + pad + 8 + 8*int(n), nil
	}
	return 0, fmt.Errorf("code+%d: opcode %s has no length rule", pc, o)
}

func be16(b []byte) uint16 { return uint16(b[0])<<8 | uint16(b[1]) }

func be32(b []byte) int32 {
	return int32(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
}