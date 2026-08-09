package classfile

import "strings"

// Flags is an access_flags mask. The same bit means different things in
// different structures (0x0020 is ACC_SUPER on a class and ACC_SYNCHRONIZED
// on a method), so always interpret a mask alongside its location.
type Flags uint16

const (
	AccPublic       Flags = 0x0001
	AccPrivate      Flags = 0x0002
	AccProtected    Flags = 0x0004
	AccStatic       Flags = 0x0008
	AccFinal        Flags = 0x0010
	AccSuper        Flags = 0x0020 // class only
	AccSynchronized Flags = 0x0020 // method only
	AccVolatile     Flags = 0x0040 // field only
	AccBridge       Flags = 0x0040 // method only
	AccTransient    Flags = 0x0080 // field only
	AccVarargs      Flags = 0x0080 // method only
	AccNative       Flags = 0x0100 // method only
	AccInterface    Flags = 0x0200 // class only
	AccAbstract     Flags = 0x0400
	AccStrict       Flags = 0x0800 // method only, and only in majors 46..60
	AccSynthetic    Flags = 0x1000
	AccAnnotation   Flags = 0x2000 // class only
	AccEnum         Flags = 0x4000 // class and field
	AccModule       Flags = 0x8000 // class only
)

// StrictMinMajor and StrictMaxMajor bound the window in which 0x0800 is
// interpreted as ACC_STRICT (JVMS §4.6). Outside it the bit is unassigned.
// Java SE 17 (major 61) does not honour it: JEP 306 made every method
// FP-strict, so the flag stopped being meaningful after major 60.
const (
	StrictMinMajor = 46
	StrictMaxMajor = 60
)

// Has reports whether every bit in mask is set.
func (f Flags) Has(mask Flags) bool { return f&mask == mask }

// Strict reports whether ACC_STRICT is meaningful and set, which it can be
// only in a class file whose major version is at least 46 and at most 60.
func (f Flags) Strict(v Version) bool {
	return v.Major >= StrictMinMajor && v.Major <= StrictMaxMajor && f&AccStrict != 0
}

// A Field is one field_info structure.
type Field struct {
	Flags      Flags
	Name       string
	Descriptor string
	Attrs      Attrs

	// Hoisted from Attrs for convenience.
	ConstantValue *Const // nil unless the field has a ConstantValue attribute
	Signature     string // generic signature, "" if absent
	Deprecated    bool
	Synthetic     bool
}

func (f *Field) IsStatic() bool { return f.Flags.Has(AccStatic) }
func (f *Field) IsFinal() bool  { return f.Flags.Has(AccFinal) }

// A Method is one method_info structure.
type Method struct {
	Flags      Flags
	Name       string
	Descriptor string
	Attrs      Attrs

	// Hoisted from Attrs for convenience.
	Code       *Code    // nil for abstract and native methods, or under SkipCode
	Exceptions []string // checked exceptions, internal form
	Signature  string
	Deprecated bool
	Synthetic  bool
}

func (m *Method) IsStatic() bool   { return m.Flags.Has(AccStatic) }
func (m *Method) IsAbstract() bool { return m.Flags.Has(AccAbstract) }
func (m *Method) IsNative() bool   { return m.Flags.Has(AccNative) }

// IsConstructor reports whether this is an instance initialization method.
func (m *Method) IsConstructor() bool { return m.Name == "<init>" }

// IsClassInit reports whether this is the class initialization method. No
// invocation instruction may reference it; the VM calls it implicitly.
func (m *Method) IsClassInit() bool { return m.Name == "<clinit>" }

// HasBody reports whether the method should carry a Code attribute.
func (m *Method) HasBody() bool { return !m.IsAbstract() && !m.IsNative() }

func (m *Method) String() string {
	var sb strings.Builder
	sb.WriteString(m.Name)
	sb.WriteString(m.Descriptor)
	return sb.String()
}