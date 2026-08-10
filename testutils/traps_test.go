package classfile_test

import (
	"math"
	"testing"

	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/jvm/op"
)

// The traps this package's documentation names, checked. None of these needs
// a JDK, so they run everywhere.

func TestSwitchPaddingIsMeasuredFromTheCodeArray(t *testing.T) {
	// A tableswitch at pc=1 pads two bytes: the alignment is to the next
	// four-byte boundary measured from the start of the code array, not from
	// the instruction. Getting this wrong misparses every switch at an odd
	// offset, which is most of them.
	code := make([]byte, 25)
	code[0] = byte(op.Nop)
	code[1] = byte(op.Tableswitch)
	// code[2], code[3] are the padding.
	put32(code[4:], 23)  // default → pc 24
	put32(code[8:], 0)   // low
	put32(code[12:], 1)  // high
	put32(code[16:], 23) // case 0 → pc 24
	put32(code[20:], 23) // case 1 → pc 24
	code[24] = byte(op.Return)

	c := &classfile.Code{MaxStack: 1, MaxLocals: 1, Bytes: code}
	it := c.Iter()

	if !it.Next() || it.Instr().Op != op.Nop {
		t.Fatalf("first instruction: %v (err %v)", it.Instr(), it.Err())
	}
	if !it.Next() {
		t.Fatalf("tableswitch did not decode: %v", it.Err())
	}
	sw := it.Instr()
	if sw.Op != op.Tableswitch {
		t.Fatalf("second instruction is %s", sw.Op)
	}
	if sw.Len() != 23 {
		t.Errorf("tableswitch length = %d, want 23", sw.Len())
	}

	def, cases := sw.Switch()
	if def != 24 {
		t.Errorf("default target = %d, want 24", def)
	}
	if len(cases) != 2 {
		t.Fatalf("decoded %d cases, want 2", len(cases))
	}
	for i, want := range []int32{0, 1} {
		if cases[i].Match != want {
			t.Errorf("case %d matches %d, want %d", i, cases[i].Match, want)
		}
		if cases[i].Target != 24 {
			t.Errorf("case %d targets %d, want 24", i, cases[i].Target)
		}
	}

	if !it.Next() || it.Instr().Op != op.Return {
		t.Errorf("instruction after the switch: %v (err %v)", it.Instr(), it.Err())
	}
	if it.Next() {
		t.Error("iteration continued past the end of the code array")
	}
	if err := it.Err(); err != nil {
		t.Errorf("Err after the loop: %v", err)
	}
}

func TestWideIincWidensBothOperands(t *testing.T) {
	// wide applied to iinc gives six bytes rather than four, because it
	// widens the delta as well as the index.
	code := []byte{
		byte(op.Wide), byte(op.Iinc),
		0x01, 0x00, // index 256
		0xFF, 0xFB, // delta -5
		byte(op.Return),
	}
	c := &classfile.Code{MaxStack: 0, MaxLocals: 258, Bytes: code}

	it := c.Iter()
	if !it.Next() {
		t.Fatalf("wide iinc did not decode: %v", it.Err())
	}
	in := it.Instr()
	if !in.Wide {
		t.Error("Wide is false")
	}
	if in.Len() != 6 {
		t.Errorf("length = %d, want 6", in.Len())
	}
	if got := in.Local(); got != 256 {
		t.Errorf("Local = %d, want 256", got)
	}
	if got := in.Increment(); got != -5 {
		t.Errorf("Increment = %d, want -5", got)
	}
}

func TestWideOnAnOpcodeWithNoLocalIndexIsAnError(t *testing.T) {
	c := &classfile.Code{Bytes: []byte{byte(op.Wide), byte(op.Iadd), byte(op.Return)}}
	it := c.Iter()
	if it.Next() {
		t.Fatalf("decoded %v, want a failure", it.Instr())
	}
	if it.Err() == nil {
		t.Error("Err is nil; Next returning false means stopped, not finished")
	}
}

func TestStrictFlagWindow(t *testing.T) {
	// 0x0800 is ACC_STRICT only in majors 46 through 60. Outside that window
	// the bit is unassigned and must be ignored, not reported.
	f := classfile.AccStrict
	for _, tc := range []struct {
		major uint16
		want  bool
	}{
		{45, false}, {46, true}, {52, true}, {60, true}, {61, false}, {69, false},
	} {
		if got := f.Strict(classfile.Version{Major: tc.major}); got != tc.want {
			t.Errorf("Strict(%d.0) = %v, want %v", tc.major, got, tc.want)
		}
	}
}

func TestFloatsInternByBitPattern(t *testing.T) {
	// +0.0 and -0.0 compare equal but are distinct constants; two NaNs with
	// different payloads compare unequal but are also distinct constants.
	// Keying the intern map on the value merges or duplicates the wrong ones.
	b := classfile.NewBuilder("com/example/F")
	p := b.Pool()

	posZero := p.Float(0)
	negZero := p.Float(float32(math.Copysign(0, -1)))
	if posZero == negZero {
		t.Error("+0.0f and -0.0f interned to the same index")
	}
	if again := p.Float(0); again != posZero {
		t.Errorf("+0.0f interned twice: %d then %d", posZero, again)
	}

	dPos := p.Double(0)
	dNeg := p.Double(math.Copysign(0, -1))
	if dPos == dNeg {
		t.Error("+0.0 and -0.0 interned to the same index")
	}

	nanA := p.Double(math.Float64frombits(0x7FF8000000000001))
	nanB := p.Double(math.Float64frombits(0x7FF8000000000002))
	if nanA == nanB {
		t.Error("two NaNs with different payloads interned to the same index")
	}
}

func TestLongTakesTwoPoolSlots(t *testing.T) {
	b := classfile.NewBuilder("com/example/L")
	p := b.Pool()

	first := p.Long(1)
	next := p.Int(7)
	if next != first+2 {
		t.Errorf("index after a Long is %d, want %d: the phantom slot was not reserved",
			next, first+2)
	}
}

func TestVersionCeiling(t *testing.T) {
	// Three separate rules, not one rule with cases.
	for _, tc := range []struct {
		name string
		v    classfile.Version
	}{
		{"major 50 needs a StackMapTable", classfile.Version{Major: classfile.Java6}},
		{"major 69", classfile.Version{Major: classfile.Java25}},
		{"below the minimum", classfile.Version{Major: 44}},
		{"preview minor", classfile.Version{Major: classfile.Java5, Minor: classfile.PreviewMinor}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := classfile.NewBuilder("com/example/V")
			b.SetVersion(tc.v)
			b.Method(classfile.AccPublic, "<init>", "()V").Code(func(c *classfile.CodeWriter) {
				c.Aload(0)
				c.InvokeSpecial("java/lang/Object", "<init>", "()V")
				c.Return()
			})
			if _, err := b.Bytes(); err == nil {
				t.Errorf("SetVersion(%s) was accepted", tc.v)
			}
		})
	}
}

func TestFallingOffTheEndIsRefused(t *testing.T) {
	b := classfile.NewBuilder("com/example/E")
	b.Method(classfile.AccPublic|classfile.AccStatic, "f", "()V").
		Code(func(c *classfile.CodeWriter) {
			c.Op(op.Nop) // no return
		})
	if _, err := b.Bytes(); err == nil {
		t.Error("a body that falls off the end was accepted")
	}
}

func put32(b []byte, v int32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}