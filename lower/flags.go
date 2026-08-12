package lower

import (
	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/sym"
)

// The frontend's vocabulary ends here, and this is one of its three
// translations. sealed, non-sealed and default have no class-file bit and are
// gone by this point: sealedness was warn's check and is over, and 49.0 admits
// no default method. Nothing below sees a source modifier.
//
// The mapping is per-location because the same bit means different things by
// location: 0x0020 is ACC_SUPER on a class and ACC_SYNCHRONIZED on a method,
// 0x0040 is ACC_VOLATILE on a field and ACC_BRIDGE on a method.

func classFlags(f sym.Flags) classfile.Flags {
	// ACC_SUPER stays set: every VM since Java 8 treats it as set regardless,
	// and clearing it only confuses older tools.
	out := classfile.AccSuper
	out |= pick(f, sym.FlagPublic, classfile.AccPublic)
	out |= pick(f, sym.FlagFinal, classfile.AccFinal)
	out |= pick(f, sym.FlagAbstract, classfile.AccAbstract)
	out |= pick(f, sym.FlagInterface, classfile.AccInterface)
	out |= pick(f, sym.FlagAnnotation, classfile.AccAnnotation)
	out |= pick(f, sym.FlagEnum, classfile.AccEnum)
	out |= pick(f, sym.FlagSynthetic, classfile.AccSynthetic)

	// An interface is abstract whether or not it was written so.
	if f.Has(sym.FlagInterface) {
		out |= classfile.AccAbstract
	}
	// A record is a plain final class here: java/lang/Record does not exist on
	// a runtime that loads 49.0. Records stay a compile-time construct.
	return out
}

func fieldFlags(f sym.Flags) classfile.Flags {
	var out classfile.Flags
	out |= pick(f, sym.FlagPublic, classfile.AccPublic)
	out |= pick(f, sym.FlagPrivate, classfile.AccPrivate)
	out |= pick(f, sym.FlagProtected, classfile.AccProtected)
	out |= pick(f, sym.FlagStatic, classfile.AccStatic)
	out |= pick(f, sym.FlagFinal, classfile.AccFinal)
	out |= pick(f, sym.FlagVolatile, classfile.AccVolatile)
	out |= pick(f, sym.FlagTransient, classfile.AccTransient)
	out |= pick(f, sym.FlagEnum, classfile.AccEnum)
	out |= pick(f, sym.FlagSynthetic, classfile.AccSynthetic)
	return out
}

func methodFlags(f sym.Flags) classfile.Flags {
	var out classfile.Flags
	out |= pick(f, sym.FlagPublic, classfile.AccPublic)
	out |= pick(f, sym.FlagPrivate, classfile.AccPrivate)
	out |= pick(f, sym.FlagProtected, classfile.AccProtected)
	out |= pick(f, sym.FlagStatic, classfile.AccStatic)
	out |= pick(f, sym.FlagFinal, classfile.AccFinal)
	out |= pick(f, sym.FlagSynchronized, classfile.AccSynchronized)
	out |= pick(f, sym.FlagNative, classfile.AccNative)
	out |= pick(f, sym.FlagAbstract, classfile.AccAbstract)
	out |= pick(f, sym.FlagBridge, classfile.AccBridge)
	out |= pick(f, sym.FlagVarargs, classfile.AccVarargs)
	out |= pick(f, sym.FlagSynthetic, classfile.AccSynthetic)

	// ACC_STRICT is deliberately not mapped. It is only a flag in majors 46
	// through 60, and JEP 306 made every method FP-strict anyway; warn already
	// accepts strictfp silently rather than warning about it.
	return out
}

func pick(f sym.Flags, from sym.Flags, to classfile.Flags) classfile.Flags {
	if f.Has(from) {
		return to
	}
	return 0
}