// Package desc implements the field and method descriptor grammar of
// JVMS §4.3. Dalvik reuses this grammar verbatim, so this package is shared
// with target/dalvik and must not import classfile.
package desc

import (
	"fmt"
	"strings"
)

// Kind is the base type of a descriptor.
type Kind uint8

const (
	Invalid Kind = iota
	Void         // V
	Boolean      // Z
	Byte         // B
	Char         // C
	Short        // S
	Int          // I
	Long         // J
	Float        // F
	Double       // D
	Object       // Lname;
)

// MaxDims is the array dimension limit (JVMS §4.3.2).
const MaxDims = 255

// MaxArgSlots is the method parameter budget, counting `this` for instance
// methods and two units for each long or double (JVMS §4.3.3).
const MaxArgSlots = 255

// A Type is a parsed field descriptor. An array is represented by Dims > 0
// together with the element Kind, so [[Ljava/lang/String; is
// {Kind: Object, Dims: 2, Name: "java/lang/String"}.
type Type struct {
	Kind Kind
	Dims int
	Name string // internal form, set only when Kind == Object
}

// Slots is the number of local variable or operand stack slots the type
// occupies: two for long and double, zero for void, one otherwise. An array
// is a reference and therefore always one.
func (t Type) Slots() int {
	if t.Dims > 0 {
		return 1
	}
	switch t.Kind {
	case Void:
		return 0
	case Long, Double:
		return 2
	default:
		return 1
	}
}

// IsRef reports whether the type is a reference type.
func (t Type) IsRef() bool { return t.Dims > 0 || t.Kind == Object }

func (t Type) String() string {
	var sb strings.Builder
	for i := 0; i < t.Dims; i++ {
		sb.WriteByte('[')
	}
	if t.Kind == Object {
		sb.WriteByte('L')
		sb.WriteString(t.Name)
		sb.WriteByte(';')
	} else {
		sb.WriteByte(baseChar[t.Kind])
	}
	return sb.String()
}

var baseChar = [...]byte{Void: 'V', Boolean: 'Z', Byte: 'B', Char: 'C',
	Short: 'S', Int: 'I', Long: 'J', Float: 'F', Double: 'D'}

// ParseField parses a field descriptor. Void is rejected; use ParseReturn for
// positions where V is legal.
func ParseField(s string) (Type, error) {
	t, n, err := parseType(s, false)
	if err != nil {
		return Type{}, err
	}
	if n != len(s) {
		return Type{}, fmt.Errorf("desc: trailing bytes in field descriptor %q", s)
	}
	return t, nil
}

// ParseReturn parses a return descriptor, which additionally admits V.
func ParseReturn(s string) (Type, error) {
	t, n, err := parseType(s, true)
	if err != nil {
		return Type{}, err
	}
	if n != len(s) {
		return Type{}, fmt.Errorf("desc: trailing bytes in return descriptor %q", s)
	}
	return t, nil
}

func parseType(s string, allowVoid bool) (Type, int, error) {
	i := 0
	for i < len(s) && s[i] == '[' {
		i++
	}
	dims := i
	if dims > MaxDims {
		return Type{}, 0, fmt.Errorf("desc: %d array dimensions exceeds the limit of %d", dims, MaxDims)
	}
	if i >= len(s) {
		return Type{}, 0, fmt.Errorf("desc: descriptor %q ends after '['", s)
	}

	var k Kind
	switch s[i] {
	case 'V':
		if !allowVoid || dims > 0 {
			return Type{}, 0, fmt.Errorf("desc: 'V' is not a valid field type in %q", s)
		}
		k = Void
	case 'Z':
		k = Boolean
	case 'B':
		k = Byte
	case 'C':
		k = Char
	case 'S':
		k = Short
	case 'I':
		k = Int
	case 'J':
		k = Long
	case 'F':
		k = Float
	case 'D':
		k = Double
	case 'L':
		end := strings.IndexByte(s[i:], ';')
		if end < 0 {
			return Type{}, 0, fmt.Errorf("desc: unterminated class type in %q", s)
		}
		name := s[i+1 : i+end]
		if name == "" {
			return Type{}, 0, fmt.Errorf("desc: empty class name in %q", s)
		}
		return Type{Kind: Object, Dims: dims, Name: name}, i + end + 1, nil
	default:
		return Type{}, 0, fmt.Errorf("desc: unknown type character %q in %q", s[i], s)
	}
	return Type{Kind: k, Dims: dims}, i + 1, nil
}

// A Method is a parsed method descriptor.
type Method struct {
	Params []Type
	Ret    Type
}

// ParseMethod parses a method descriptor of the form (params)ret.
//
// The closing paren is found by walking the parameter types, not by scanning
// for ')'. An unqualified name may not contain '.', ';', '[' or '/' (§4.2.2),
// but ')' is perfectly legal in one, so a pre-scan mis-splits any descriptor
// whose parameter class name contains a right paren. javac never emits one;
// obfuscators do.
func ParseMethod(s string) (Method, error) {
	if len(s) < 3 || s[0] != '(' {
		return Method{}, fmt.Errorf("desc: %q is not a method descriptor", s)
	}
	var m Method
	i := 1
	for i < len(s) && s[i] != ')' {
		t, n, err := parseType(s[i:], false)
		if err != nil {
			return Method{}, err
		}
		m.Params = append(m.Params, t)
		i += n
	}
	if i >= len(s) {
		return Method{}, fmt.Errorf("desc: unterminated parameter list in %q", s)
	}
	ret, err := ParseReturn(s[i+1:])
	if err != nil {
		return Method{}, err
	}
	m.Ret = ret
	return m, nil
}

func (m Method) String() string {
	var sb strings.Builder
	sb.WriteByte('(')
	for _, p := range m.Params {
		sb.WriteString(p.String())
	}
	sb.WriteByte(')')
	sb.WriteString(m.Ret.String())
	return sb.String()
}

// ArgSlots is the number of local slots the arguments occupy. Pass
// receiver=true for instance and interface methods to count `this`. This is
// the lower bound on a method's max_locals.
func (m Method) ArgSlots(receiver bool) int {
	n := 0
	if receiver {
		n = 1
	}
	for _, p := range m.Params {
		n += p.Slots()
	}
	return n
}

// CheckArgSlots reports whether the argument list fits the 255-slot budget of
// §4.3.3. Writers must call it: the count is also the operand of
// invokeinterface, where it is a single byte.
func (m Method) CheckArgSlots(receiver bool) error {
	if n := m.ArgSlots(receiver); n > MaxArgSlots {
		return fmt.Errorf("desc: %s takes %d argument slots, over the limit of %d",
			m, n, MaxArgSlots)
	}
	return nil
}

// Shorty renders the dex ShortyDescriptor: the return character followed by
// one character per parameter, with every reference type collapsed to 'L'.
func (m Method) Shorty() string {
	var sb strings.Builder
	sb.Grow(len(m.Params) + 1)
	sb.WriteByte(shortyChar(m.Ret))
	for _, p := range m.Params {
		sb.WriteByte(shortyChar(p))
	}
	return sb.String()
}

func shortyChar(t Type) byte {
	if t.IsRef() {
		return 'L'
	}
	return baseChar[t.Kind]
}

// Internal converts a binary name (java.lang.Thread) to internal form
// (java/lang/Thread). See JVMS §4.2.1.
func Internal(binary string) string { return strings.ReplaceAll(binary, ".", "/") }

// Binary is the inverse of Internal.
func Binary(internal string) string { return strings.ReplaceAll(internal, "/", ".") }