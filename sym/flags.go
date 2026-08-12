package sym

import (
	"strings"

	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/token"
)

// Flags is a modifier set. It is deliberately not classfile.Flags: the same bit
// means different things at different locations there, and three source
// modifiers (sealed, non-sealed, default) have no bit at all. Mapping in both
// directions is explicit, below.
type Flags uint32

const (
	FlagPublic Flags = 1 << iota
	FlagPrivate
	FlagProtected
	FlagStatic
	FlagFinal
	FlagAbstract
	FlagNative
	FlagSynchronized
	FlagTransient
	FlagVolatile
	FlagStrictfp
	FlagDefault // an interface method with a body
	FlagSealed
	FlagNonSealed
	FlagInterface
	FlagAnnotation
	FlagEnum
	FlagRecord
	FlagVarargs
	FlagBridge
	FlagSynthetic
	FlagDeprecated
	FlagModule

	// FlagImplicit marks a member the language declares on your behalf: a
	// record's accessors and canonical constructor, an enum's values and
	// valueOf. They are real members and resolution must find them, but no
	// declaration in the source produced them.
	FlagImplicit
)

// AccessFlags is the three-way access modifier group. Absent means package
// access, which has no keyword and therefore no bit.
const AccessFlags = FlagPublic | FlagPrivate | FlagProtected

// Has reports whether every bit in mask is set.
func (f Flags) Has(mask Flags) bool { return f&mask == mask }

// HasAny reports whether any bit in mask is set.
func (f Flags) HasAny(mask Flags) bool { return f&mask != 0 }

var flagNames = []struct {
	f Flags
	s string
}{
	{FlagPublic, "public"}, {FlagPrivate, "private"}, {FlagProtected, "protected"},
	{FlagAbstract, "abstract"}, {FlagStatic, "static"}, {FlagFinal, "final"},
	{FlagSealed, "sealed"}, {FlagNonSealed, "non-sealed"}, {FlagStrictfp, "strictfp"},
	{FlagTransient, "transient"}, {FlagVolatile, "volatile"},
	{FlagSynchronized, "synchronized"}, {FlagNative, "native"}, {FlagDefault, "default"},
}

// String renders the flags in the JLS's canonical order, which is a style rule
// — the tree keeps the order actually written, and a formatter should use that.
func (f Flags) String() string {
	var sb strings.Builder
	for _, n := range flagNames {
		if f.Has(n.f) {
			if sb.Len() > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(n.s)
		}
	}
	return sb.String()
}

// modifierFlags maps a written modifier list.
//
// One wart, inherited from the parser: `sealed` is contextual, so it is stored
// as a Modifier with Kind == token.IDENT and its Ctx left on the token that is
// no longer reachable. No other IDENT can appear in a modifier list, so
// Kind == IDENT means sealed and nothing else.
func modifierFlags(m *ast.Modifiers) Flags {
	var f Flags
	if m == nil {
		return f
	}
	for _, x := range m.List {
		if x.Annotation != nil {
			continue
		}
		switch x.Kind {
		case token.PUBLIC:
			f |= FlagPublic
		case token.PRIVATE:
			f |= FlagPrivate
		case token.PROTECTED:
			f |= FlagProtected
		case token.STATIC:
			f |= FlagStatic
		case token.FINAL:
			f |= FlagFinal
		case token.ABSTRACT:
			f |= FlagAbstract
		case token.NATIVE:
			f |= FlagNative
		case token.SYNCHRONIZED:
			f |= FlagSynchronized
		case token.TRANSIENT:
			f |= FlagTransient
		case token.VOLATILE:
			f |= FlagVolatile
		case token.STRICTFP:
			f |= FlagStrictfp
		case token.DEFAULT:
			f |= FlagDefault
		case token.NON_SEALED:
			f |= FlagNonSealed
		case token.IDENT:
			f |= FlagSealed
		}
	}
	return f
}

// annotationFlags picks up the annotations that carry meaning for resolution.
// Everything else is left in the tree for a later phase.
func annotationFlags(m *ast.Modifiers, unit *token.File) Flags {
	var f Flags
	if m == nil {
		return f
	}
	for _, x := range m.List {
		a := x.Annotation
		if a == nil || a.Name == nil || len(a.Name.Parts) == 0 {
			continue
		}
		switch a.Name.Parts[len(a.Name.Parts)-1].Name(unit) {
		case "Deprecated":
			f |= FlagDeprecated
		case "SafeVarargs":
			f |= FlagVarargs
		}
	}
	return f
}

// classFileClassFlags maps a class's access_flags. ACC_SUPER (0x0020) is not
// mapped: it means nothing to resolution, every VM since Java 8 treats it as
// set regardless, and on a method the same bit is ACC_SYNCHRONIZED.
func classFileClassFlags(a classfile.Flags) Flags {
	var f Flags
	f |= mapBit(a, classfile.AccPublic, FlagPublic)
	f |= mapBit(a, classfile.AccFinal, FlagFinal)
	f |= mapBit(a, classfile.AccInterface, FlagInterface)
	f |= mapBit(a, classfile.AccAbstract, FlagAbstract)
	f |= mapBit(a, classfile.AccSynthetic, FlagSynthetic)
	f |= mapBit(a, classfile.AccAnnotation, FlagAnnotation)
	f |= mapBit(a, classfile.AccEnum, FlagEnum)
	f |= mapBit(a, classfile.AccModule, FlagModule)
	return f
}

func classFileFieldFlags(a classfile.Flags) Flags {
	var f Flags
	f |= mapBit(a, classfile.AccPublic, FlagPublic)
	f |= mapBit(a, classfile.AccPrivate, FlagPrivate)
	f |= mapBit(a, classfile.AccProtected, FlagProtected)
	f |= mapBit(a, classfile.AccStatic, FlagStatic)
	f |= mapBit(a, classfile.AccFinal, FlagFinal)
	f |= mapBit(a, classfile.AccVolatile, FlagVolatile)
	f |= mapBit(a, classfile.AccTransient, FlagTransient)
	f |= mapBit(a, classfile.AccSynthetic, FlagSynthetic)
	f |= mapBit(a, classfile.AccEnum, FlagEnum)
	return f
}

func classFileMethodFlags(a classfile.Flags) Flags {
	var f Flags
	f |= mapBit(a, classfile.AccPublic, FlagPublic)
	f |= mapBit(a, classfile.AccPrivate, FlagPrivate)
	f |= mapBit(a, classfile.AccProtected, FlagProtected)
	f |= mapBit(a, classfile.AccStatic, FlagStatic)
	f |= mapBit(a, classfile.AccFinal, FlagFinal)
	f |= mapBit(a, classfile.AccSynchronized, FlagSynchronized)
	f |= mapBit(a, classfile.AccBridge, FlagBridge)
	f |= mapBit(a, classfile.AccVarargs, FlagVarargs)
	f |= mapBit(a, classfile.AccNative, FlagNative)
	f |= mapBit(a, classfile.AccAbstract, FlagAbstract)
	f |= mapBit(a, classfile.AccSynthetic, FlagSynthetic)
	return f
}

func mapBit(a, bit classfile.Flags, out Flags) Flags {
	if a&bit != 0 {
		return out
	}
	return 0
}