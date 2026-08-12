package classfile

import (
	"fmt"
	"math"
	"sort"

	"github.com/vertex-language/mocha/jvm/desc"
	"github.com/vertex-language/mocha/jvm/op"
)

// MaxStack and MaxLocals are the ceilings on the Code attribute's max_stack
// and max_locals items, both of which are u2.
const (
	MaxStack  = 65535
	MaxLocals = 65535
)

// A Label is a branch target. Create labels inside the Code closure with
// NewLabel and place them with Mark.
type Label struct {
	id    int
	pc    int32 // resolved offset, or -1
	depth int32 // operand stack depth on entry, or -1 if not yet known
}

type branch struct {
	ord   int // emission ordinal, stable across replays
	op    op.Op
	label *Label
	at    int // byte offset of the opcode in the current pass
	wide  bool
}

// switchRec is a tableswitch or lookupswitch awaiting its offsets. Unlike a
// branch it never needs widening: every offset in a switch is a signed 32-bit
// field. What it does need is its own padding remembered, since that is
// measured from the start of the code array and therefore moves when an
// earlier branch widens.
type switchRec struct {
	op      op.Op
	at      int // byte offset of the opcode
	pad     int
	def     *Label
	targets []*Label
}

// localRec is one row of a LocalVariableTable, in label form. The PCs are not
// known until the labels resolve, which is after the closure has run.
type localRec struct {
	slot       int
	name, desc string
	start, end *Label
}

// A CodeWriter builds a method body. Instances are handed to the closure
// passed to MethodBuilder.Code and must not escape it: the closure is re-run
// whenever a branch turns out not to fit in a signed 16-bit offset.
type CodeWriter struct {
	p   *PoolBuilder
	w   writer
	err error

	ret desc.Type // the enclosing method's return type, for Return
	ver Version   // the enclosing class's target version, for Cconst

	labels   []*Label
	branches []branch
	switches []switchRec
	handlers []handlerRec
	lines    []LineNumber
	locals   []localRec

	widen map[int]bool // branch ordinals that must use the long form
	pass  int

	depth, maxStack int32
	maxLocals       int32
	reachable       bool
}

type handlerRec struct {
	start, end, handler *Label
	catchType           string
}

func (c *CodeWriter) fail(format string, args ...any) {
	if c.err == nil {
		c.err = fmt.Errorf("classfile: code: "+format, args...)
	}
}

// ---------------------------------------------------------------- labels

// NewLabel allocates an unplaced branch target.
func (c *CodeWriter) NewLabel() *Label {
	l := &Label{id: len(c.labels), pc: -1, depth: -1}
	c.labels = append(c.labels, l)
	return l
}

// Mark places a label at the current offset.
func (c *CodeWriter) Mark(l *Label) {
	if l.pc >= 0 {
		c.fail("label %d placed twice", l.id)
		return
	}
	l.pc = int32(c.w.len())

	switch {
	case l.depth >= 0:
		// Some earlier branch fixed the depth here.
		if c.reachable && c.depth != l.depth {
			c.fail("stack depth %d falling into label %d, which expects %d",
				c.depth, l.id, l.depth)
			return
		}
		c.depth = l.depth
	case c.reachable:
		l.depth = c.depth
	default:
		c.fail("label %d is unreachable: no branch targets it and control cannot fall in", l.id)
		return
	}
	c.reachable = true
}

// noteDepth records the stack depth a branch requires at its target.
func (c *CodeWriter) noteDepth(l *Label) {
	if l.depth < 0 {
		l.depth = c.depth
		return
	}
	if l.depth != c.depth {
		c.fail("branch to label %d with stack depth %d, but %d was recorded earlier",
			l.id, c.depth, l.depth)
	}
}

// ---------------------------------------------------------------- stack

func (c *CodeWriter) push(n int32) {
	c.depth += n
	if c.depth > c.maxStack {
		if c.depth > MaxStack {
			c.fail("operand stack depth %d exceeds the max_stack limit of %d", c.depth, MaxStack)
			return
		}
		c.maxStack = c.depth
	}
}

func (c *CodeWriter) pop(n int32) {
	c.depth -= n
	if c.depth < 0 {
		c.fail("operand stack underflow at offset %d", c.w.len())
		c.depth = 0
	}
}

// useLocal widens max_locals to cover a slot of the given width. max_locals is
// a u2, so a two-slot local at 65535 does not fit and must be refused rather
// than silently wrapped on the way out.
func (c *CodeWriter) useLocal(slot int, width int32) {
	if slot < 0 || slot > MaxLocals {
		c.fail("local slot %d out of range", slot)
		return
	}
	n := int32(slot) + width
	if n > MaxLocals {
		c.fail("local slot %d of width %d needs max_locals %d, over the limit of %d",
			slot, width, n, MaxLocals)
		return
	}
	if n > c.maxLocals {
		c.maxLocals = n
	}
}

// terminate marks the following bytes unreachable until the next Mark.
func (c *CodeWriter) terminate() { c.reachable = false }

func (c *CodeWriter) emitOp(o op.Op) {
	if !c.reachable {
		c.fail("instruction %s at offset %d is unreachable", o, c.w.len())
		return
	}
	c.w.u1(uint8(o))
}

// ---------------------------------------------------------------- loads

// Aload pushes a reference from a local slot, choosing the compact form.
func (c *CodeWriter) Aload(slot int) { c.load(op.Aload, op.Aload0, slot, 1) }

// Iload pushes an int from a local slot.
func (c *CodeWriter) Iload(slot int) { c.load(op.Iload, op.Iload0, slot, 1) }

// Fload pushes a float from a local slot.
func (c *CodeWriter) Fload(slot int) { c.load(op.Fload, op.Fload0, slot, 1) }

// Lload pushes a long from a local slot, which occupies two slots.
func (c *CodeWriter) Lload(slot int) { c.load(op.Lload, op.Lload0, slot, 2) }

// Dload pushes a double from a local slot, which occupies two slots.
func (c *CodeWriter) Dload(slot int) { c.load(op.Dload, op.Dload0, slot, 2) }

func (c *CodeWriter) load(general, compact op.Op, slot int, width int32) {
	c.localOp(general, compact, slot, width)
	c.push(width)
}

// Astore pops a reference into a local slot.
func (c *CodeWriter) Astore(slot int) { c.store(op.Astore, op.Astore0, slot, 1) }

// Istore pops an int into a local slot.
func (c *CodeWriter) Istore(slot int) { c.store(op.Istore, op.Istore0, slot, 1) }

// Fstore pops a float into a local slot.
func (c *CodeWriter) Fstore(slot int) { c.store(op.Fstore, op.Fstore0, slot, 1) }

// Lstore pops a long into a local slot.
func (c *CodeWriter) Lstore(slot int) { c.store(op.Lstore, op.Lstore0, slot, 2) }

// Dstore pops a double into a local slot.
func (c *CodeWriter) Dstore(slot int) { c.store(op.Dstore, op.Dstore0, slot, 2) }

func (c *CodeWriter) store(general, compact op.Op, slot int, width int32) {
	c.localOp(general, compact, slot, width)
	c.pop(width)
}

func (c *CodeWriter) localOp(general, compact op.Op, slot int, width int32) {
	c.useLocal(slot, width)
	if c.err != nil {
		return
	}
	switch {
	case slot < 4:
		c.emitOp(compact + op.Op(slot))
	case slot < 256:
		c.emitOp(general)
		c.w.u1(uint8(slot))
	default:
		c.emitOp(op.Wide)
		c.w.u1(uint8(general))
		c.w.u2(uint16(slot))
	}
}

// Iinc adds a constant to an int local, widening when either operand needs it.
func (c *CodeWriter) Iinc(slot, delta int) {
	c.useLocal(slot, 1)
	if c.err != nil {
		return
	}
	if delta < math.MinInt16 || delta > math.MaxInt16 {
		c.fail("iinc delta %d does not fit in a signed 16-bit operand", delta)
		return
	}
	if slot < 256 && delta >= -128 && delta <= 127 {
		c.emitOp(op.Iinc)
		c.w.u1(uint8(slot))
		c.w.u1(uint8(int8(delta)))
		return
	}
	c.emitOp(op.Wide)
	c.w.u1(uint8(op.Iinc))
	c.w.u2(uint16(slot))
	c.w.u2(uint16(int16(delta)))
}

// Local records a row of the LocalVariableTable: the slot holds a variable of
// the given name and descriptor between start and end.
//
// Call it from inside the closure with labels the closure itself marked. The
// rows are converted to PCs after the branch fixpoint converges, which is why
// this takes labels rather than offsets — an offset captured mid-pass would be
// stale the moment an earlier branch widened.
func (c *CodeWriter) Local(slot int, name, descriptor string, start, end *Label) {
	if _, err := desc.ParseField(descriptor); err != nil {
		c.fail("local %s: %v", name, err)
		return
	}
	if slot < 0 || slot > MaxLocals {
		c.fail("local %s: slot %d out of range", name, slot)
		return
	}
	c.locals = append(c.locals, localRec{slot: slot, name: name, desc: descriptor,
		start: start, end: end})
}

// ---------------------------------------------------------------- constants

// Iconst pushes an int, picking the shortest of iconst_<i>, bipush, sipush
// and ldc.
func (c *CodeWriter) Iconst(v int32) {
	switch {
	case v >= -1 && v <= 5:
		c.emitOp(op.Iconst0 + op.Op(v))
	case v >= -128 && v <= 127:
		c.emitOp(op.Bipush)
		c.w.u1(uint8(int8(v)))
	case v >= -32768 && v <= 32767:
		c.emitOp(op.Sipush)
		c.w.u2(uint16(int16(v)))
	default:
		c.ldc(c.p.Int(v), false)
		return
	}
	c.push(1)
}

// Lconst pushes a long.
func (c *CodeWriter) Lconst(v int64) {
	if v == 0 || v == 1 {
		c.emitOp(op.Lconst0 + op.Op(v))
		c.push(2)
		return
	}
	c.ldc(c.p.Long(v), true)
}

// Fconst pushes a float.
//
// The shortcut is decided on the bit pattern, not the value: -0.0 == 0.0 under
// IEEE comparison, so a value test would emit fconst_0 for negative zero and
// push the wrong constant. This is the same hazard PoolBuilder.Float avoids on
// the interning side.
func (c *CodeWriter) Fconst(v float32) {
	switch math.Float32bits(v) {
	case math.Float32bits(0):
		c.emitOp(op.Fconst0)
	case math.Float32bits(1):
		c.emitOp(op.Fconst1)
	case math.Float32bits(2):
		c.emitOp(op.Fconst2)
	default:
		c.ldc(c.p.Float(v), false)
		return
	}
	c.push(1)
}

// Dconst pushes a double.
func (c *CodeWriter) Dconst(v float64) {
	switch math.Float64bits(v) {
	case math.Float64bits(0):
		c.emitOp(op.Dconst0)
	case math.Float64bits(1):
		c.emitOp(op.Dconst1)
	default:
		c.ldc(c.p.Double(v), true)
		return
	}
	c.push(2)
}

// Sconst pushes a String constant.
func (c *CodeWriter) Sconst(s string) { c.ldc(c.p.String(s), false) }

// Cconst pushes a Class constant: the operand of `Foo.class`, and of the
// desiredAssertionStatus call that initialises $assertionsDisabled.
//
// CONSTANT_Class is the one tag that became loadable later than it was
// defined — 49.0, not 45.0 — so this is gated where nothing else is. It is
// also the reason `assert` cannot be lowered against a 45.0 target.
func (c *CodeWriter) Cconst(internal string) {
	if !c.ver.AtLeast(Java5) {
		c.fail("CONSTANT_Class is not loadable before class file 49.0 (this class is %s)", c.ver)
		return
	}
	c.ldc(c.p.Class(internal), false)
}

// AconstNull pushes null.
func (c *CodeWriter) AconstNull() {
	c.emitOp(op.AconstNull)
	c.push(1)
}

func (c *CodeWriter) ldc(idx uint16, wide bool) {
	switch {
	case wide:
		c.emitOp(op.Ldc2W)
		c.w.u2(idx)
		c.push(2)
	case idx < 256:
		c.emitOp(op.Ldc)
		c.w.u1(uint8(idx))
		c.push(1)
	default:
		c.emitOp(op.LdcW)
		c.w.u2(idx)
		c.push(1)
	}
}

// ---------------------------------------------------------------- members

// GetStatic pushes a static field.
func (c *CodeWriter) GetStatic(owner, name, descriptor string) {
	c.field(op.Getstatic, owner, name, descriptor)
}

// PutStatic pops a value into a static field.
func (c *CodeWriter) PutStatic(owner, name, descriptor string) {
	c.field(op.Putstatic, owner, name, descriptor)
}

// GetField replaces an objectref with the named instance field.
func (c *CodeWriter) GetField(owner, name, descriptor string) {
	c.field(op.Getfield, owner, name, descriptor)
}

// PutField pops an objectref and a value into an instance field.
func (c *CodeWriter) PutField(owner, name, descriptor string) {
	c.field(op.Putfield, owner, name, descriptor)
}

func (c *CodeWriter) field(o op.Op, owner, name, descriptor string) {
	t, err := desc.ParseField(descriptor)
	if err != nil {
		c.fail("%v", err)
		return
	}
	c.emitOp(o)
	c.w.u2(c.p.FieldRef(owner, name, descriptor))
	width := int32(t.Slots())
	switch o {
	case op.Getstatic:
		c.push(width)
	case op.Putstatic:
		c.pop(width)
	case op.Getfield:
		c.pop(1)
		c.push(width)
	case op.Putfield:
		c.pop(width + 1)
	}
}

// InvokeVirtual calls an instance method.
func (c *CodeWriter) InvokeVirtual(owner, name, descriptor string) {
	c.invoke(op.Invokevirtual, owner, name, descriptor)
}

// InvokeSpecial calls a constructor, private method or superclass method.
func (c *CodeWriter) InvokeSpecial(owner, name, descriptor string) {
	c.invoke(op.Invokespecial, owner, name, descriptor)
}

// InvokeStatic calls a static method.
func (c *CodeWriter) InvokeStatic(owner, name, descriptor string) {
	c.invoke(op.Invokestatic, owner, name, descriptor)
}

// InvokeInterface calls an interface method. Unlike the other invokes it
// carries a redundant argument count and a zero byte.
func (c *CodeWriter) InvokeInterface(owner, name, descriptor string) {
	c.invoke(op.Invokeinterface, owner, name, descriptor)
}

func (c *CodeWriter) invoke(o op.Op, owner, name, descriptor string) {
	m, err := desc.ParseMethod(descriptor)
	if err != nil {
		c.fail("%v", err)
		return
	}
	receiver := o != op.Invokestatic
	// The count is a single byte in invokeinterface, so 255 is a hard ceiling
	// here rather than an abstract limit from §4.3.3.
	if err := m.CheckArgSlots(receiver); err != nil {
		c.fail("%v", err)
		return
	}
	args := int32(m.ArgSlots(receiver))

	c.emitOp(o)
	switch o {
	case op.Invokeinterface:
		c.w.u2(c.p.InterfaceMethodRef(owner, name, descriptor))
		c.w.u1(uint8(args)) // the count includes the receiver
		c.w.u1(0)
	default:
		c.w.u2(c.p.MethodRef(owner, name, descriptor))
	}
	c.pop(args)
	c.push(int32(m.Ret.Slots()))
}

// ---------------------------------------------------------------- objects

// New allocates an uninitialized instance. A matching InvokeSpecial of
// <init> must follow before the reference is used.
func (c *CodeWriter) New(internal string) {
	c.emitOp(op.New)
	c.w.u2(c.p.Class(internal))
	c.push(1)
}

// ANewArray creates a reference array with the given component type.
func (c *CodeWriter) ANewArray(component string) {
	c.emitOp(op.Anewarray)
	c.w.u2(c.p.Class(component))
	c.pop(1)
	c.push(1)
}

// NewArray creates a primitive array; typeCode is one of the op.T* constants.
func (c *CodeWriter) NewArray(typeCode uint8) {
	if op.ArrayTypeName(typeCode) == "" {
		c.fail("newarray type code %d is not one of the eight primitive codes", typeCode)
		return
	}
	c.emitOp(op.Newarray)
	c.w.u1(typeCode)
	c.pop(1)
	c.push(1)
}

// MultiANewArray creates a multidimensional array, consuming one int per
// dimension. The operand is an array descriptor rather than a class name —
// "[[I", not "I" — and it must name at least as many dimensions as dims.
func (c *CodeWriter) MultiANewArray(descriptor string, dims int) {
	if dims < 1 || dims > 255 {
		c.fail("multianewarray dimension count %d is outside 1..255", dims)
		return
	}
	t, err := desc.ParseField(descriptor)
	if err != nil {
		c.fail("multianewarray: %v", err)
		return
	}
	if t.Dims < dims {
		c.fail("multianewarray %s has %d dimensions but %d were requested",
			descriptor, t.Dims, dims)
		return
	}
	c.emitOp(op.Multianewarray)
	c.w.u2(c.p.Class(descriptor))
	c.w.u1(uint8(dims))
	c.pop(int32(dims))
	c.push(1)
}

// CheckCast narrows a reference, throwing ClassCastException on failure.
func (c *CodeWriter) CheckCast(internal string) {
	c.emitOp(op.Checkcast)
	c.w.u2(c.p.Class(internal))
}

// InstanceOf replaces a reference with an int 0 or 1.
func (c *CodeWriter) InstanceOf(internal string) {
	c.emitOp(op.Instanceof)
	c.w.u2(c.p.Class(internal))
}

// Throw raises the exception on the top of the stack.
func (c *CodeWriter) Throw() { c.Op(op.Athrow) }

// ---------------------------------------------------------------- simple ops

// Op emits an operand-free instruction such as iadd, dup or arraylength.
// Instructions taking operands have their own methods.
func (c *CodeWriter) Op(o op.Op) {
	if o.Kind() != op.None {
		c.fail("%s takes operands; use its dedicated method", o)
		return
	}
	d := simpleDelta[o]
	if d.pop < 0 {
		c.fail("%s is not permitted here", o)
		return
	}
	c.emitOp(o)
	c.pop(int32(d.pop))
	c.push(int32(d.push))
	if o.Terminates() {
		c.terminate()
	}
}

// Return emits the return instruction matching the enclosing method's
// descriptor: ireturn for the int-like types, lreturn, freturn, dreturn,
// areturn for any reference or array, and return for void.
func (c *CodeWriter) Return() {
	if c.ret.IsRef() {
		c.Op(op.Areturn)
		return
	}
	switch c.ret.Kind {
	case desc.Void:
		c.Op(op.Return)
	case desc.Long:
		c.Op(op.Lreturn)
	case desc.Float:
		c.Op(op.Freturn)
	case desc.Double:
		c.Op(op.Dreturn)
	case desc.Boolean, desc.Byte, desc.Char, desc.Short, desc.Int:
		c.Op(op.Ireturn)
	default:
		c.fail("cannot pick a return instruction for return type %v", c.ret.Kind)
	}
}

// ---------------------------------------------------------------- branches

// Goto jumps unconditionally.
func (c *CodeWriter) Goto(l *Label) {
	c.branch(op.Goto, l, 0)
	c.terminate()
}

// IfEq branches when the int on top of the stack is zero.
func (c *CodeWriter) IfEq(l *Label) { c.branch(op.Ifeq, l, 1) }

// IfNe branches when the int on top of the stack is non-zero.
func (c *CodeWriter) IfNe(l *Label) { c.branch(op.Ifne, l, 1) }

// IfLt, IfGe, IfGt and IfLe compare the top int against zero.
func (c *CodeWriter) IfLt(l *Label) { c.branch(op.Iflt, l, 1) }
func (c *CodeWriter) IfGe(l *Label) { c.branch(op.Ifge, l, 1) }
func (c *CodeWriter) IfGt(l *Label) { c.branch(op.Ifgt, l, 1) }
func (c *CodeWriter) IfLe(l *Label) { c.branch(op.Ifle, l, 1) }

// IfICmp* compare the top two ints.
func (c *CodeWriter) IfICmpEq(l *Label) { c.branch(op.IfIcmpeq, l, 2) }
func (c *CodeWriter) IfICmpNe(l *Label) { c.branch(op.IfIcmpne, l, 2) }
func (c *CodeWriter) IfICmpLt(l *Label) { c.branch(op.IfIcmplt, l, 2) }
func (c *CodeWriter) IfICmpGe(l *Label) { c.branch(op.IfIcmpge, l, 2) }
func (c *CodeWriter) IfICmpGt(l *Label) { c.branch(op.IfIcmpgt, l, 2) }
func (c *CodeWriter) IfICmpLe(l *Label) { c.branch(op.IfIcmple, l, 2) }

// IfACmpEq and IfACmpNe compare the top two references.
func (c *CodeWriter) IfACmpEq(l *Label) { c.branch(op.IfAcmpeq, l, 2) }
func (c *CodeWriter) IfACmpNe(l *Label) { c.branch(op.IfAcmpne, l, 2) }

// IfNull and IfNonNull test the top reference.
func (c *CodeWriter) IfNull(l *Label)    { c.branch(op.Ifnull, l, 1) }
func (c *CodeWriter) IfNonNull(l *Label) { c.branch(op.Ifnonnull, l, 1) }

// branch emits a branch, choosing the short or long encoding according to
// what the previous pass discovered.
func (c *CodeWriter) branch(o op.Op, l *Label, pops int32) {
	ord := len(c.branches)
	at := c.w.len()
	wide := c.widen[ord]

	c.pop(pops)
	c.noteDepth(l)

	if !wide {
		c.emitOp(o)
		c.w.u2(0) // patched after the pass
	} else if o == op.Goto {
		c.emitOp(op.GotoW)
		c.w.u4(0)
	} else {
		// No conditional branch has a wide form, so invert the test and jump
		// over a goto_w. This is why widening changes instruction lengths and
		// why the body has to be replayed rather than patched in place.
		c.emitOp(invert[o])
		c.w.u2(8) // skip the goto_w that follows
		c.w.u1(uint8(op.GotoW))
		c.w.u4(0)
	}
	c.branches = append(c.branches, branch{ord: ord, op: o, label: l, at: at, wide: wide})
}

var invert = map[op.Op]op.Op{
	op.Ifeq: op.Ifne, op.Ifne: op.Ifeq,
	op.Iflt: op.Ifge, op.Ifge: op.Iflt,
	op.Ifgt: op.Ifle, op.Ifle: op.Ifgt,
	op.IfIcmpeq: op.IfIcmpne, op.IfIcmpne: op.IfIcmpeq,
	op.IfIcmplt: op.IfIcmpge, op.IfIcmpge: op.IfIcmplt,
	op.IfIcmpgt: op.IfIcmple, op.IfIcmple: op.IfIcmpgt,
	op.IfAcmpeq: op.IfAcmpne, op.IfAcmpne: op.IfAcmpeq,
	op.Ifnull: op.Ifnonnull, op.Ifnonnull: op.Ifnull,
}

// ---------------------------------------------------------------- switches

// TableSwitch emits a tableswitch covering the contiguous range low..high,
// one target per value. It pops the index and does not fall through.
//
// The four-byte padding after the opcode is measured from the start of the
// code array, so this instruction's length depends on its own offset — which
// moves when an earlier branch widens. That is handled by re-emitting on each
// replay rather than by patching, and it is why the padding is remembered per
// pass. A switch offset is a signed 32-bit field and never needs widening
// itself, so a switch adds nothing to the fixpoint beyond shifting what
// follows it.
func (c *CodeWriter) TableSwitch(low, high int32, def *Label, targets []*Label) {
	if high < low {
		c.fail("tableswitch high %d is below low %d", high, low)
		return
	}
	if n := int64(high) - int64(low) + 1; n != int64(len(targets)) {
		c.fail("tableswitch covers %d..%d (%d entries) but was given %d targets",
			low, high, n, len(targets))
		return
	}

	at := c.w.len()
	c.pop(1)
	c.noteDepth(def)
	for _, t := range targets {
		c.noteDepth(t)
	}

	c.emitOp(op.Tableswitch)
	pad := (4 - (at+1)%4) % 4
	for i := 0; i < pad; i++ {
		c.w.u1(0)
	}
	c.w.u4(0) // default, patched
	c.w.u4(uint32(low))
	c.w.u4(uint32(high))
	for range targets {
		c.w.u4(0)
	}
	c.switches = append(c.switches, switchRec{
		op: op.Tableswitch, at: at, pad: pad, def: def, targets: targets,
	})
	c.terminate()
}

// LookupSwitch emits a lookupswitch over sparse match values. The pairs are
// sorted here: §6.5 requires ascending order, and the verifier enforces it.
func (c *CodeWriter) LookupSwitch(def *Label, matches []int32, targets []*Label) {
	if len(matches) != len(targets) {
		c.fail("lookupswitch has %d matches but %d targets", len(matches), len(targets))
		return
	}

	ord := make([]int, len(matches))
	for i := range ord {
		ord[i] = i
	}
	sort.Slice(ord, func(a, b int) bool { return matches[ord[a]] < matches[ord[b]] })

	sortedM := make([]int32, len(ord))
	sortedT := make([]*Label, len(ord))
	for i, j := range ord {
		if i > 0 && matches[j] == sortedM[i-1] {
			c.fail("lookupswitch has duplicate match value %d", matches[j])
			return
		}
		sortedM[i], sortedT[i] = matches[j], targets[j]
	}

	at := c.w.len()
	c.pop(1)
	c.noteDepth(def)
	for _, t := range sortedT {
		c.noteDepth(t)
	}

	c.emitOp(op.Lookupswitch)
	pad := (4 - (at+1)%4) % 4
	for i := 0; i < pad; i++ {
		c.w.u1(0)
	}
	c.w.u4(0) // default, patched
	c.w.u4(uint32(len(sortedM)))
	for _, m := range sortedM {
		c.w.u4(uint32(m))
		c.w.u4(0) // offset, patched
	}
	c.switches = append(c.switches, switchRec{
		op: op.Lookupswitch, at: at, pad: pad, def: def, targets: sortedT,
	})
	c.terminate()
}

// ---------------------------------------------------------------- handlers

// TryCatch registers an exception handler covering [start, end). Pass an
// empty catchType to catch everything, which is how finally is compiled.
// Call it before marking the handler label: a handler is entered with only
// the throwable on the stack, and this is what records that.
func (c *CodeWriter) TryCatch(start, end, handler *Label, catchType string) {
	c.handlers = append(c.handlers, handlerRec{start, end, handler, catchType})
	if handler.depth < 0 {
		handler.depth = 1
	} else if handler.depth != 1 {
		c.fail("handler label %d already has stack depth %d, but a handler entry is 1",
			handler.id, handler.depth)
	}
	if 1 > c.maxStack {
		c.maxStack = 1
	}
}

// Line records a source line for the current offset.
func (c *CodeWriter) Line(n int) {
	c.lines = append(c.lines, LineNumber{StartPC: uint16(c.w.len()), Line: uint16(n)})
}

// ---------------------------------------------------------------- resolve

// resolve patches branch and switch offsets, reporting whether another pass is
// needed. Switches are patched only once the branches have settled: a widened
// branch moves every label after it, so patching sooner would write offsets
// that are about to be discarded.
func (c *CodeWriter) resolve() (again bool) {
	for _, br := range c.branches {
		if br.label.pc < 0 {
			c.fail("label %d was never placed", br.label.id)
			return false
		}
		off := br.label.pc - int32(br.at)
		switch {
		case br.wide && br.op == op.Goto:
			c.w.patchU4(br.at+1, uint32(off))
		case br.wide:
			// The goto_w sits three bytes into the expanded sequence, so its
			// own offset is measured from there.
			c.w.patchU4(br.at+4, uint32(br.label.pc-int32(br.at)-3))
		case off < -32768 || off > 32767:
			c.widen[br.ord] = true
			again = true
		default:
			c.w.patchU2(br.at+1, uint16(int16(off)))
		}
	}
	if again {
		return true
	}

	for _, s := range c.switches {
		base := int32(s.at)
		body := s.at + 1 + s.pad
		if s.def.pc < 0 {
			c.fail("switch default label %d was never placed", s.def.id)
			return false
		}
		c.w.patchU4(body, uint32(s.def.pc-base))

		for i, t := range s.targets {
			if t.pc < 0 {
				c.fail("switch target label %d was never placed", t.id)
				return false
			}
			off := uint32(t.pc - base)
			if s.op == op.Tableswitch {
				c.w.patchU4(body+12+4*i, off)
			} else {
				c.w.patchU4(body+8+8*i+4, off)
			}
		}
	}
	return false
}

// localRows converts the recorded locals to LocalVariableTable rows. Call it
// only after resolve has converged; a zero-length range is dropped, since a
// variable whose scope contains no instruction names nothing.
func (c *CodeWriter) localRows() []LocalVariable {
	out := make([]LocalVariable, 0, len(c.locals))
	for _, l := range c.locals {
		if l.start.pc < 0 || l.end.pc < 0 {
			c.fail("local %s spans a label that was never placed", l.name)
			return nil
		}
		if l.end.pc <= l.start.pc {
			continue
		}
		out = append(out, LocalVariable{
			StartPC:    uint16(l.start.pc),
			Length:     uint16(l.end.pc - l.start.pc),
			Name:       l.name,
			Descriptor: l.desc,
			Slot:       uint16(l.slot),
		})
	}
	return out
}

// simpleDelta gives the stack effect of every operand-free opcode, counted in
// slots. A pop of -1 marks an opcode this writer will not emit.
//
// Slot arithmetic is what makes the dup family tractable without category
// tracking: dup2 on a long pops 2 and pushes 4, exactly as it does on two
// ints. The category distinction decides which opcode is correct, which is the
// caller's problem, not max_stack's.
var simpleDelta = func() [256]struct{ pop, push int8 } {
	var t [256]struct{ pop, push int8 }
	for i := range t {
		t[i].pop = -1
	}
	set := func(pop, push int8, ops ...op.Op) {
		for _, o := range ops {
			t[o] = struct{ pop, push int8 }{pop, push}
		}
	}
	set(0, 0, op.Nop)
	// monitorenter and monitorexit each pop the objectref they lock.
	set(1, 0, op.Monitorenter, op.Monitorexit)
	set(0, 1, op.Iconst0, op.Iconst1, op.Iconst2, op.Iconst3, op.Iconst4, op.Iconst5,
		op.IconstM1, op.Fconst0, op.Fconst1, op.Fconst2, op.AconstNull)
	set(0, 2, op.Lconst0, op.Lconst1, op.Dconst0, op.Dconst1)
	set(2, 1, op.Iaload, op.Faload, op.Aaload, op.Baload, op.Caload, op.Saload,
		op.Iadd, op.Isub, op.Imul, op.Idiv, op.Irem, op.Iand, op.Ior, op.Ixor,
		op.Ishl, op.Ishr, op.Iushr,
		op.Fadd, op.Fsub, op.Fmul, op.Fdiv, op.Frem)
	set(2, 2, op.Laload, op.Daload)
	set(4, 2, op.Ladd, op.Lsub, op.Lmul, op.Ldiv, op.Lrem, op.Land, op.Lor, op.Lxor,
		op.Dadd, op.Dsub, op.Dmul, op.Ddiv, op.Drem)
	set(3, 2, op.Lshl, op.Lshr, op.Lushr)
	set(3, 0, op.Iastore, op.Fastore, op.Aastore, op.Bastore, op.Castore, op.Sastore)
	set(4, 0, op.Lastore, op.Dastore)
	set(1, 1, op.Ineg, op.Fneg, op.I2f, op.F2i, op.I2b, op.I2c, op.I2s, op.Arraylength)
	set(2, 2, op.Lneg, op.Dneg, op.L2d, op.D2l)
	set(1, 2, op.I2l, op.I2d, op.F2l, op.F2d)
	set(2, 1, op.L2i, op.L2f, op.D2i, op.D2f)
	set(4, 1, op.Lcmp, op.Dcmpl, op.Dcmpg)
	set(2, 1, op.Fcmpl, op.Fcmpg)
	set(1, 0, op.Pop, op.Ireturn, op.Freturn, op.Areturn)
	set(2, 0, op.Pop2, op.Lreturn, op.Dreturn)
	set(0, 0, op.Return)
	// athrow clears the stack, but control cannot fall through it, so the depth
	// after it is never read: the next Mark restores it from the label.
	set(1, 0, op.Athrow)
	set(1, 2, op.Dup)
	set(2, 3, op.DupX1)
	set(3, 4, op.DupX2)
	set(2, 4, op.Dup2)
	set(3, 5, op.Dup2X1)
	set(4, 6, op.Dup2X2)
	set(2, 2, op.Swap)
	return t
}()