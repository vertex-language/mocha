package classfile

import (
	"fmt"
	"math"

	"github.com/vertex-language/mocha/jvm/mutf8"
)

// A Tag identifies a constant pool entry kind (JVMS Table 4.4-A). Tags 2, 13
// and 14 are unassigned and must never appear.
type Tag uint8

const (
	TagUtf8               Tag = 1
	TagInteger            Tag = 3
	TagFloat              Tag = 4
	TagLong               Tag = 5
	TagDouble             Tag = 6
	TagClass              Tag = 7
	TagString             Tag = 8
	TagFieldref           Tag = 9
	TagMethodref          Tag = 10
	TagInterfaceMethodref Tag = 11
	TagNameAndType        Tag = 12
	TagMethodHandle       Tag = 15
	TagMethodType         Tag = 16
	TagDynamic            Tag = 17
	TagInvokeDynamic      Tag = 18
	TagModule             Tag = 19
	TagPackage            Tag = 20
)

// tagSince records the first class file major version defining each tag, so a
// v45 file claiming a CONSTANT_Dynamic is rejected rather than silently read.
var tagSince = map[Tag]uint16{
	TagUtf8: Java1_0, TagInteger: Java1_0, TagFloat: Java1_0,
	TagLong: Java1_0, TagDouble: Java1_0, TagClass: Java1_0,
	TagString: Java1_0, TagFieldref: Java1_0, TagMethodref: Java1_0,
	TagInterfaceMethodref: Java1_0, TagNameAndType: Java1_0,
	TagMethodHandle: Java7, TagMethodType: Java7, TagInvokeDynamic: Java7,
	TagModule: Java9, TagPackage: Java9, TagDynamic: Java11,
}

var tagName = map[Tag]string{
	TagUtf8: "Utf8", TagInteger: "Integer", TagFloat: "Float", TagLong: "Long",
	TagDouble: "Double", TagClass: "Class", TagString: "String",
	TagFieldref: "Fieldref", TagMethodref: "Methodref",
	TagInterfaceMethodref: "InterfaceMethodref", TagNameAndType: "NameAndType",
	TagMethodHandle: "MethodHandle", TagMethodType: "MethodType",
	TagDynamic: "Dynamic", TagInvokeDynamic: "InvokeDynamic",
	TagModule: "Module", TagPackage: "Package",
}

func (t Tag) String() string {
	if n, ok := tagName[t]; ok {
		return "CONSTANT_" + n
	}
	return fmt.Sprintf("CONSTANT_?(%d)", uint8(t))
}

// Method handle reference kinds (JVMS §4.4.8, values 1 through 9).
const (
	RefGetField         = 1
	RefGetStatic        = 2
	RefPutField         = 3
	RefPutStatic        = 4
	RefInvokeVirtual    = 5
	RefInvokeStatic     = 6
	RefInvokeSpecial    = 7
	RefNewInvokeSpecial = 8
	RefInvokeInterface  = 9
)

// entry is the undecoded form of one pool slot. For TagUtf8, a and b hold the
// offset and length of the raw modified UTF-8 bytes within the class file; for
// everything else they hold the structure's one or two u2 operands, or the
// 4-byte halves of an 8-byte numeric constant.
type entry struct {
	tag  Tag
	a, b uint32
}

// A Pool is a class file constant pool together with the bootstrap method
// table, which the pool's Dynamic entries index into (JVMS §4.4.10). The two
// are kept in one type because they refer to each other; separating them
// would make BootstrapMethods and the pool mutually dependent.
//
// Entries are inflated lazily: reading android.jar touches a small fraction of
// its Utf8 entries, so decoding them all up front is wasted work.
type Pool struct {
	entries []entry  // index 0 is a placeholder and is never valid
	src     []byte   // the class file bytes; Utf8 entries alias this
	strs    []string // memoised Utf8 decodings, parallel to entries
	strOK   []bool   // whether strs[i] has been computed; "" is a valid value
	bsms    []BootstrapMethod
	file    string
}

// Len returns constant_pool_count, one more than the highest valid index.
func (p *Pool) Len() int { return len(p.entries) }

// Tag returns the kind of entry i, or 0 if i is out of range or unusable.
func (p *Pool) Tag(i uint16) Tag {
	if int(i) >= len(p.entries) {
		return 0
	}
	return p.entries[i].tag
}

func (p *Pool) errf(i uint16, format string, args ...any) error {
	return fmt.Errorf("%s: constant pool index %d: %s", p.name(), i, fmt.Sprintf(format, args...))
}

func (p *Pool) name() string {
	if p.file == "" {
		return "class file"
	}
	return p.file
}

// at validates an index and its tag in one step.
func (p *Pool) at(i uint16, want Tag) (entry, error) {
	if i == 0 {
		return entry{}, p.errf(i, "index 0 is not a valid constant pool index")
	}
	if int(i) >= len(p.entries) {
		return entry{}, p.errf(i, "out of range (pool holds %d entries)", len(p.entries)-1)
	}
	e := p.entries[i]
	if e.tag == 0 {
		return entry{}, p.errf(i, "unusable slot (second half of an 8-byte constant)")
	}
	if want != 0 && e.tag != want {
		return entry{}, p.errf(i, "is %s, want %s", e.tag, want)
	}
	return e, nil
}

// UTF8 returns the decoded contents of a CONSTANT_Utf8_info entry.
func (p *Pool) UTF8(i uint16) (string, error) {
	e, err := p.at(i, TagUtf8)
	if err != nil {
		return "", err
	}
	// Memoisation is keyed on strOK, not on strs[i] != "": the empty string is
	// a perfectly ordinary Utf8 constant and would otherwise re-decode forever.
	if p.strOK[i] {
		return p.strs[i], nil
	}
	s, err := mutf8.Decode(p.src[e.a : e.a+e.b])
	if err != nil {
		return "", p.errf(i, "%v", err)
	}
	p.strs[i], p.strOK[i] = s, true
	return s, nil
}

// Class returns the internal-form name of a CONSTANT_Class_info entry. For an
// array class the name is the array's descriptor, e.g. "[[I".
func (p *Pool) Class(i uint16) (string, error) {
	e, err := p.at(i, TagClass)
	if err != nil {
		return "", err
	}
	return p.UTF8(uint16(e.a))
}

// Module returns the name of a CONSTANT_Module_info entry. Module names are
// not in internal form: their dots are not replaced by slashes.
func (p *Pool) Module(i uint16) (string, error) {
	e, err := p.at(i, TagModule)
	if err != nil {
		return "", err
	}
	return p.UTF8(uint16(e.a))
}

// Package returns the internal-form name of a CONSTANT_Package_info entry.
func (p *Pool) Package(i uint16) (string, error) {
	e, err := p.at(i, TagPackage)
	if err != nil {
		return "", err
	}
	return p.UTF8(uint16(e.a))
}

// NameAndType returns the name and descriptor of a CONSTANT_NameAndType_info.
func (p *Pool) NameAndType(i uint16) (name, descriptor string, err error) {
	e, err := p.at(i, TagNameAndType)
	if err != nil {
		return "", "", err
	}
	if name, err = p.UTF8(uint16(e.a)); err != nil {
		return "", "", err
	}
	if descriptor, err = p.UTF8(uint16(e.b)); err != nil {
		return "", "", err
	}
	return name, descriptor, nil
}

// A Ref is a resolved symbolic reference to a field or method. Consumers get
// this rather than raw indices, so ir/builder never walks the pool itself.
type Ref struct {
	Tag        Tag    // Fieldref, Methodref or InterfaceMethodref
	Class      string // internal form
	Name       string
	Descriptor string
}

func (r Ref) String() string { return r.Class + "." + r.Name + r.Descriptor }

// IsInterface reports whether the reference came from an interface method
// reference, which invokeinterface and some method handles require.
func (r Ref) IsInterface() bool { return r.Tag == TagInterfaceMethodref }

// Ref resolves a Fieldref, Methodref or InterfaceMethodref entry. Use it for
// the invoke opcodes, which accept either method form depending on the
// instruction and class file version.
func (p *Pool) Ref(i uint16) (Ref, error) {
	e, err := p.at(i, 0)
	if err != nil {
		return Ref{}, err
	}
	switch e.tag {
	case TagFieldref, TagMethodref, TagInterfaceMethodref:
	default:
		return Ref{}, p.errf(i, "is %s, want a field or method reference", e.tag)
	}
	class, err := p.Class(uint16(e.a))
	if err != nil {
		return Ref{}, err
	}
	name, descriptor, err := p.NameAndType(uint16(e.b))
	if err != nil {
		return Ref{}, err
	}
	return Ref{Tag: e.tag, Class: class, Name: name, Descriptor: descriptor}, nil
}

// FieldRef is Ref restricted to CONSTANT_Fieldref_info.
func (p *Pool) FieldRef(i uint16) (Ref, error) {
	r, err := p.Ref(i)
	if err == nil && r.Tag != TagFieldref {
		return Ref{}, p.errf(i, "is %s, want CONSTANT_Fieldref", r.Tag)
	}
	return r, err
}

// MethodRef is Ref restricted to the two method reference forms.
func (p *Pool) MethodRef(i uint16) (Ref, error) {
	r, err := p.Ref(i)
	if err == nil && r.Tag == TagFieldref {
		return Ref{}, p.errf(i, "is CONSTANT_Fieldref, want a method reference")
	}
	return r, err
}

// A Handle is a resolved CONSTANT_MethodHandle_info.
type Handle struct {
	Kind uint8 // one of the Ref* constants
	Ref  Ref
}

// Handle resolves a method handle entry.
//
// The version is required, not incidental: §4.4.8 lets REF_invokeStatic and
// REF_invokeSpecial name an InterfaceMethodref only from class file 52.0 on.
// Every other kind is pinned to a single reference form regardless of version.
func (p *Pool) Handle(i uint16, v Version) (Handle, error) {
	e, err := p.at(i, TagMethodHandle)
	if err != nil {
		return Handle{}, err
	}
	kind := uint8(e.a)
	if kind < RefGetField || kind > RefInvokeInterface {
		return Handle{}, p.errf(i, "reference_kind %d is outside the range 1..9", kind)
	}
	ref, err := p.Ref(uint16(e.b))
	if err != nil {
		return Handle{}, err
	}

	switch kind {
	case RefGetField, RefGetStatic, RefPutField, RefPutStatic:
		if ref.Tag != TagFieldref {
			return Handle{}, p.errf(i, "reference_kind %d requires a field reference, got %s", kind, ref.Tag)
		}

	case RefInvokeVirtual, RefNewInvokeSpecial:
		// No interface form, at any version.
		if ref.Tag != TagMethodref {
			return Handle{}, p.errf(i, "reference_kind %d requires CONSTANT_Methodref, got %s", kind, ref.Tag)
		}

	case RefInvokeStatic, RefInvokeSpecial:
		switch ref.Tag {
		case TagMethodref:
		case TagInterfaceMethodref:
			if !v.AtLeast(Java8) {
				return Handle{}, p.errf(i,
					"reference_kind %d may name an interface method reference only in class file 52.0 or later (this file is %s)",
					kind, v)
			}
		default:
			return Handle{}, p.errf(i, "reference_kind %d requires a method reference, got %s", kind, ref.Tag)
		}

	case RefInvokeInterface:
		if ref.Tag != TagInterfaceMethodref {
			return Handle{}, p.errf(i, "REF_invokeInterface requires an interface method reference, got %s", ref.Tag)
		}
	}

	// Name constraints, orthogonal to the reference form.
	if kind == RefNewInvokeSpecial {
		if ref.Name != "<init>" {
			return Handle{}, p.errf(i, "REF_newInvokeSpecial must name <init>, not %s", ref.Name)
		}
	} else if kind >= RefInvokeVirtual {
		if ref.Name == "<init>" || ref.Name == "<clinit>" {
			return Handle{}, p.errf(i, "reference_kind %d must not name %s", kind, ref.Name)
		}
	}
	return Handle{Kind: kind, Ref: ref}, nil
}

// MethodType returns the descriptor of a CONSTANT_MethodType_info.
func (p *Pool) MethodType(i uint16) (string, error) {
	e, err := p.at(i, TagMethodType)
	if err != nil {
		return "", err
	}
	return p.UTF8(uint16(e.a))
}

// A Dynamic is a resolved CONSTANT_Dynamic_info or CONSTANT_InvokeDynamic_info.
type Dynamic struct {
	Tag        Tag
	Bootstrap  uint16 // index into the bootstrap method table
	Name       string
	Descriptor string // a field descriptor for Dynamic, a method descriptor for InvokeDynamic
}

// Dynamic resolves either dynamic entry form.
func (p *Pool) Dynamic(i uint16) (Dynamic, error) {
	e, err := p.at(i, 0)
	if err != nil {
		return Dynamic{}, err
	}
	if e.tag != TagDynamic && e.tag != TagInvokeDynamic {
		return Dynamic{}, p.errf(i, "is %s, want a dynamic constant", e.tag)
	}
	name, descriptor, err := p.NameAndType(uint16(e.b))
	if err != nil {
		return Dynamic{}, err
	}
	return Dynamic{Tag: e.tag, Bootstrap: uint16(e.a), Name: name, Descriptor: descriptor}, nil
}

// A BootstrapMethod is one entry of the BootstrapMethods attribute.
type BootstrapMethod struct {
	Method    Handle
	Arguments []uint16 // pool indices, each a loadable constant
}

// Bootstraps returns the bootstrap method table. It is empty until the class
// level attributes have been read.
func (p *Pool) Bootstraps() []BootstrapMethod { return p.bsms }

// Bootstrap returns the bootstrap method a Dynamic entry names.
func (p *Pool) Bootstrap(d Dynamic) (BootstrapMethod, error) {
	if int(d.Bootstrap) >= len(p.bsms) {
		return BootstrapMethod{}, fmt.Errorf("%s: bootstrap method index %d out of range (table holds %d)",
			p.name(), d.Bootstrap, len(p.bsms))
	}
	return p.bsms[d.Bootstrap], nil
}

// A Const is a loadable constant, the operand of ldc, ldc_w and ldc2_w
// (JVMS Table 4.4-C).
type Const struct {
	Tag    Tag
	bits   uint64  // Integer, Float, Long and Double payloads
	Str    string  // String contents, Class internal name, or MethodType descriptor
	Handle Handle  // set when Tag == TagMethodHandle
	Dyn    Dynamic // set when Tag == TagDynamic
}

func (c Const) Int() int32      { return int32(uint32(c.bits)) }
func (c Const) Long() int64     { return int64(c.bits) }
func (c Const) Float() float32  { return math.Float32frombits(uint32(c.bits)) }
func (c Const) Double() float64 { return math.Float64frombits(c.bits) }

// Wide reports whether the constant occupies two operand stack slots, which
// is exactly the set that ldc2_w may load.
func (c Const) Wide() bool { return c.Tag == TagLong || c.Tag == TagDouble }

// Const resolves a loadable constant. It enforces the version rule that
// CONSTANT_Class only became loadable in class file 49.0 — the one tag that
// became loadable later than it was defined. Every other loadable tag became
// loadable in the version that introduced it, and readPool has already gated
// those.
func (p *Pool) Const(i uint16, v Version) (Const, error) {
	e, err := p.at(i, 0)
	if err != nil {
		return Const{}, err
	}
	c := Const{Tag: e.tag}
	switch e.tag {
	case TagInteger, TagFloat:
		c.bits = uint64(e.a)
	case TagLong, TagDouble:
		c.bits = uint64(e.a)<<32 | uint64(e.b)
	case TagClass:
		if !v.AtLeast(Java5) {
			return Const{}, p.errf(i, "CONSTANT_Class is not loadable before class file 49.0 (this file is %s)", v)
		}
		c.Str, err = p.Class(i)
	case TagString:
		c.Str, err = p.UTF8(uint16(e.a))
	case TagMethodHandle:
		c.Handle, err = p.Handle(i, v)
	case TagMethodType:
		c.Str, err = p.MethodType(i)
	case TagDynamic:
		c.Dyn, err = p.Dynamic(i)
	default:
		return Const{}, p.errf(i, "%s is not a loadable constant", e.tag)
	}
	if err != nil {
		return Const{}, err
	}
	return c, nil
}

// readPool decodes constant_pool_count and the entries that follow.
func readPool(r *reader, v Version) *Pool {
	count := r.u2()
	if count == 0 {
		r.fail("constant_pool_count is 0 (must be at least 1)")
		return nil
	}
	p := &Pool{
		entries: make([]entry, count),
		strs:    make([]string, count),
		strOK:   make([]bool, count),
		src:     r.b,
		file:    r.file,
	}

	for i := uint16(1); i < count; i++ {
		if r.done() {
			return p
		}
		tagOff := r.off
		tag := Tag(r.u1())
		since, known := tagSince[tag]
		if !known {
			r.off = tagOff
			r.fail("unknown constant pool tag %d at index %d", uint8(tag), i)
			return p
		}
		if v.Major < since {
			r.off = tagOff
			r.fail("%s at index %d requires class file %d.0 or later, but this file is %s",
				tag, i, since, v)
			return p
		}

		e := entry{tag: tag}
		switch tag {
		case TagUtf8:
			n := r.u2()
			off := r.off
			b := r.bytes(int(n))
			for j, c := range b {
				if c == 0 || c >= 0xF0 {
					r.off = off + j
					r.fail("byte 0x%02x is not permitted in a Utf8 constant", c)
					return p
				}
			}
			e.a, e.b = uint32(off), uint32(n)

		case TagInteger, TagFloat:
			e.a = r.u4()

		case TagLong, TagDouble:
			e.a, e.b = r.u4(), r.u4()

		case TagClass, TagString, TagMethodType, TagModule, TagPackage:
			e.a = uint32(r.u2())

		case TagFieldref, TagMethodref, TagInterfaceMethodref,
			TagNameAndType, TagDynamic, TagInvokeDynamic:
			e.a, e.b = uint32(r.u2()), uint32(r.u2())

		case TagMethodHandle:
			e.a, e.b = uint32(r.u1()), uint32(r.u2())
		}

		p.entries[i] = e

		// An 8-byte constant consumes the following slot, which stays valid
		// as an index but is permanently unusable. Leaving tag == 0 makes
		// at() reject it.
		if tag == TagLong || tag == TagDouble {
			if i+1 >= count {
				r.fail("%s at index %d runs past the end of the constant pool", tag, i)
				return p
			}
			i++
		}
	}
	return p
}