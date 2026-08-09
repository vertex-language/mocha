// Package op enumerates the JVM instruction set (JVMS chapter 6) and the
// operand shape of each opcode. It is a leaf package so that ir/builder and
// the disassembler can switch on opcodes without importing classfile.
package op

// An Op is a one-byte JVM opcode.
type Op uint8

const (
	Nop             Op = 0x00
	AconstNull      Op = 0x01
	IconstM1        Op = 0x02
	Iconst0         Op = 0x03
	Iconst1         Op = 0x04
	Iconst2         Op = 0x05
	Iconst3         Op = 0x06
	Iconst4         Op = 0x07
	Iconst5         Op = 0x08
	Lconst0         Op = 0x09
	Lconst1         Op = 0x0a
	Fconst0         Op = 0x0b
	Fconst1         Op = 0x0c
	Fconst2         Op = 0x0d
	Dconst0         Op = 0x0e
	Dconst1         Op = 0x0f
	Bipush          Op = 0x10
	Sipush          Op = 0x11
	Ldc             Op = 0x12
	LdcW            Op = 0x13
	Ldc2W           Op = 0x14
	Iload           Op = 0x15
	Lload           Op = 0x16
	Fload           Op = 0x17
	Dload           Op = 0x18
	Aload           Op = 0x19
	Iload0          Op = 0x1a
	Iload1          Op = 0x1b
	Iload2          Op = 0x1c
	Iload3          Op = 0x1d
	Lload0          Op = 0x1e
	Lload1          Op = 0x1f
	Lload2          Op = 0x20
	Lload3          Op = 0x21
	Fload0          Op = 0x22
	Fload1          Op = 0x23
	Fload2          Op = 0x24
	Fload3          Op = 0x25
	Dload0          Op = 0x26
	Dload1          Op = 0x27
	Dload2          Op = 0x28
	Dload3          Op = 0x29
	Aload0          Op = 0x2a
	Aload1          Op = 0x2b
	Aload2          Op = 0x2c
	Aload3          Op = 0x2d
	Iaload          Op = 0x2e
	Laload          Op = 0x2f
	Faload          Op = 0x30
	Daload          Op = 0x31
	Aaload          Op = 0x32
	Baload          Op = 0x33
	Caload          Op = 0x34
	Saload          Op = 0x35
	Istore          Op = 0x36
	Lstore          Op = 0x37
	Fstore          Op = 0x38
	Dstore          Op = 0x39
	Astore          Op = 0x3a
	Istore0         Op = 0x3b
	Istore1         Op = 0x3c
	Istore2         Op = 0x3d
	Istore3         Op = 0x3e
	Lstore0         Op = 0x3f
	Lstore1         Op = 0x40
	Lstore2         Op = 0x41
	Lstore3         Op = 0x42
	Fstore0         Op = 0x43
	Fstore1         Op = 0x44
	Fstore2         Op = 0x45
	Fstore3         Op = 0x46
	Dstore0         Op = 0x47
	Dstore1         Op = 0x48
	Dstore2         Op = 0x49
	Dstore3         Op = 0x4a
	Astore0         Op = 0x4b
	Astore1         Op = 0x4c
	Astore2         Op = 0x4d
	Astore3         Op = 0x4e
	Iastore         Op = 0x4f
	Lastore         Op = 0x50
	Fastore         Op = 0x51
	Dastore         Op = 0x52
	Aastore         Op = 0x53
	Bastore         Op = 0x54
	Castore         Op = 0x55
	Sastore         Op = 0x56
	Pop             Op = 0x57
	Pop2            Op = 0x58
	Dup             Op = 0x59
	DupX1           Op = 0x5a
	DupX2           Op = 0x5b
	Dup2            Op = 0x5c
	Dup2X1          Op = 0x5d
	Dup2X2          Op = 0x5e
	Swap            Op = 0x5f
	Iadd            Op = 0x60
	Ladd            Op = 0x61
	Fadd            Op = 0x62
	Dadd            Op = 0x63
	Isub            Op = 0x64
	Lsub            Op = 0x65
	Fsub            Op = 0x66
	Dsub            Op = 0x67
	Imul            Op = 0x68
	Lmul            Op = 0x69
	Fmul            Op = 0x6a
	Dmul            Op = 0x6b
	Idiv            Op = 0x6c
	Ldiv            Op = 0x6d
	Fdiv            Op = 0x6e
	Ddiv            Op = 0x6f
	Irem            Op = 0x70
	Lrem            Op = 0x71
	Frem            Op = 0x72
	Drem            Op = 0x73
	Ineg            Op = 0x74
	Lneg            Op = 0x75
	Fneg            Op = 0x76
	Dneg            Op = 0x77
	Ishl            Op = 0x78
	Lshl            Op = 0x79
	Ishr            Op = 0x7a
	Lshr            Op = 0x7b
	Iushr           Op = 0x7c
	Lushr           Op = 0x7d
	Iand            Op = 0x7e
	Land            Op = 0x7f
	Ior             Op = 0x80
	Lor             Op = 0x81
	Ixor            Op = 0x82
	Lxor            Op = 0x83
	Iinc            Op = 0x84
	I2l             Op = 0x85
	I2f             Op = 0x86
	I2d             Op = 0x87
	L2i             Op = 0x88
	L2f             Op = 0x89
	L2d             Op = 0x8a
	F2i             Op = 0x8b
	F2l             Op = 0x8c
	F2d             Op = 0x8d
	D2i             Op = 0x8e
	D2l             Op = 0x8f
	D2f             Op = 0x90
	I2b             Op = 0x91
	I2c             Op = 0x92
	I2s             Op = 0x93
	Lcmp            Op = 0x94
	Fcmpl           Op = 0x95
	Fcmpg           Op = 0x96
	Dcmpl           Op = 0x97
	Dcmpg           Op = 0x98
	Ifeq            Op = 0x99
	Ifne            Op = 0x9a
	Iflt            Op = 0x9b
	Ifge            Op = 0x9c
	Ifgt            Op = 0x9d
	Ifle            Op = 0x9e
	IfIcmpeq        Op = 0x9f
	IfIcmpne        Op = 0xa0
	IfIcmplt        Op = 0xa1
	IfIcmpge        Op = 0xa2
	IfIcmpgt        Op = 0xa3
	IfIcmple        Op = 0xa4
	IfAcmpeq        Op = 0xa5
	IfAcmpne        Op = 0xa6
	Goto            Op = 0xa7
	Jsr             Op = 0xa8 // deprecated in class version 51
	Ret             Op = 0xa9 // deprecated in class version 51
	Tableswitch     Op = 0xaa
	Lookupswitch    Op = 0xab
	Ireturn         Op = 0xac
	Lreturn         Op = 0xad
	Freturn         Op = 0xae
	Dreturn         Op = 0xaf
	Areturn         Op = 0xb0
	Return          Op = 0xb1
	Getstatic       Op = 0xb2
	Putstatic       Op = 0xb3
	Getfield        Op = 0xb4
	Putfield        Op = 0xb5
	Invokevirtual   Op = 0xb6
	Invokespecial   Op = 0xb7
	Invokestatic    Op = 0xb8
	Invokeinterface Op = 0xb9
	Invokedynamic   Op = 0xba
	New             Op = 0xbb
	Newarray        Op = 0xbc
	Anewarray       Op = 0xbd
	Arraylength     Op = 0xbe
	Athrow          Op = 0xbf
	Checkcast       Op = 0xc0
	Instanceof      Op = 0xc1
	Monitorenter    Op = 0xc2
	Monitorexit     Op = 0xc3
	Wide            Op = 0xc4
	Multianewarray  Op = 0xc5
	Ifnull          Op = 0xc6
	Ifnonnull       Op = 0xc7
	GotoW           Op = 0xc8
	JsrW            Op = 0xc9 // deprecated in class version 51
	Breakpoint      Op = 0xca // reserved; must not appear in a class file
	Impdep1         Op = 0xfe // reserved
	Impdep2         Op = 0xff // reserved
)

// Kind describes the operand shape that follows an opcode in the code array.
type Kind uint8

const (
	Unused          Kind = iota // opcode is not assigned
	None                        // no operands
	Local                       // u1 local index (u2 under wide)
	ConstU1                     // u1 constant pool index (ldc)
	ConstU2                     // u2 constant pool index
	Byte1                       // s1 immediate
	Short2                      // s2 immediate
	Branch2                     // s2 branch offset from the opcode
	Branch4                     // s4 branch offset from the opcode
	IincK                       // u1 index + s1 delta (u2 + s2 under wide)
	NewarrayK                   // u1 primitive array type code
	MultiK                      // u2 class index + u1 dimensions
	InterfaceK                  // u2 index + u1 count + u1 zero
	DynamicK                    // u2 index + u1 zero + u1 zero
	TableswitchK                // 4-byte aligned; variable length
	LookupswitchK               // 4-byte aligned; variable length
	WideK                       // prefix; the next opcode's operands widen
)

// Info describes one opcode.
type Info struct {
	Name string
	Kind Kind
	Len  int8 // total instruction length in bytes, or -1 if variable
}

// Name returns the mnemonic, or "" for an unassigned opcode.
func (o Op) Name() string { return table[o].Name }

// Kind returns the operand shape.
func (o Op) Kind() Kind { return table[o].Kind }

// Len returns the total instruction length in bytes, or -1 when the length
// depends on the instruction's own operands (wide and the two switches).
func (o Op) Len() int { return int(table[o].Len) }

// Valid reports whether the opcode is assigned by the specification and may
// appear in a class file. breakpoint, impdep1 and impdep2 are reserved for
// debugger and implementation use and are excluded: §6.2 forbids them from
// appearing in a class file, so a decoder that accepts them is wrong. Name()
// and String() still resolve them, so a diagnostic can say which one it saw.
func (o Op) Valid() bool { return table[o].Kind != Unused }

func (o Op) String() string {
	if n := table[o].Name; n != "" {
		return n
	}
	return "op(" + itoa(int(o)) + ")"
}

// IsReturn reports whether the opcode ends a method normally.
func (o Op) IsReturn() bool { return o >= Ireturn && o <= Return }

// IsInvoke reports whether the opcode is a method invocation.
func (o Op) IsInvoke() bool { return o >= Invokevirtual && o <= Invokedynamic }

// IsFieldAccess reports whether the opcode reads or writes a field.
func (o Op) IsFieldAccess() bool { return o >= Getstatic && o <= Putfield }

// IsBranch reports whether the opcode transfers control conditionally or
// unconditionally to a single offset. The switches and jsr/ret are excluded.
func (o Op) IsBranch() bool {
	return (o >= Ifeq && o <= Goto) || o == Ifnull || o == Ifnonnull || o == GotoW
}

// Terminates reports whether control cannot fall through the instruction.
func (o Op) Terminates() bool {
	return o.IsReturn() || o == Athrow || o == Goto || o == GotoW ||
		o == Tableswitch || o == Lookupswitch || o == Ret
}

// Primitive array type codes for the newarray operand (JVMS §6.5 newarray).
const (
	TBoolean = 4
	TChar    = 5
	TFloat   = 6
	TDouble  = 7
	TByte    = 8
	TShort   = 9
	TInt     = 10
	TLong    = 11
)

// ArrayTypeName maps a newarray type code to its descriptor character.
func ArrayTypeName(code uint8) string {
	switch code {
	case TBoolean:
		return "Z"
	case TChar:
		return "C"
	case TFloat:
		return "F"
	case TDouble:
		return "D"
	case TByte:
		return "B"
	case TShort:
		return "S"
	case TInt:
		return "I"
	case TLong:
		return "J"
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

var table = [256]Info{
	Nop: {"nop", None, 1}, AconstNull: {"aconst_null", None, 1},
	IconstM1: {"iconst_m1", None, 1}, Iconst0: {"iconst_0", None, 1},
	Iconst1: {"iconst_1", None, 1}, Iconst2: {"iconst_2", None, 1},
	Iconst3: {"iconst_3", None, 1}, Iconst4: {"iconst_4", None, 1},
	Iconst5: {"iconst_5", None, 1}, Lconst0: {"lconst_0", None, 1},
	Lconst1: {"lconst_1", None, 1}, Fconst0: {"fconst_0", None, 1},
	Fconst1: {"fconst_1", None, 1}, Fconst2: {"fconst_2", None, 1},
	Dconst0: {"dconst_0", None, 1}, Dconst1: {"dconst_1", None, 1},

	Bipush: {"bipush", Byte1, 2}, Sipush: {"sipush", Short2, 3},
	Ldc: {"ldc", ConstU1, 2}, LdcW: {"ldc_w", ConstU2, 3}, Ldc2W: {"ldc2_w", ConstU2, 3},

	Iload: {"iload", Local, 2}, Lload: {"lload", Local, 2},
	Fload: {"fload", Local, 2}, Dload: {"dload", Local, 2}, Aload: {"aload", Local, 2},
	Iload0: {"iload_0", None, 1}, Iload1: {"iload_1", None, 1},
	Iload2: {"iload_2", None, 1}, Iload3: {"iload_3", None, 1},
	Lload0: {"lload_0", None, 1}, Lload1: {"lload_1", None, 1},
	Lload2: {"lload_2", None, 1}, Lload3: {"lload_3", None, 1},
	Fload0: {"fload_0", None, 1}, Fload1: {"fload_1", None, 1},
	Fload2: {"fload_2", None, 1}, Fload3: {"fload_3", None, 1},
	Dload0: {"dload_0", None, 1}, Dload1: {"dload_1", None, 1},
	Dload2: {"dload_2", None, 1}, Dload3: {"dload_3", None, 1},
	Aload0: {"aload_0", None, 1}, Aload1: {"aload_1", None, 1},
	Aload2: {"aload_2", None, 1}, Aload3: {"aload_3", None, 1},

	Iaload: {"iaload", None, 1}, Laload: {"laload", None, 1},
	Faload: {"faload", None, 1}, Daload: {"daload", None, 1},
	Aaload: {"aaload", None, 1}, Baload: {"baload", None, 1},
	Caload: {"caload", None, 1}, Saload: {"saload", None, 1},

	Istore: {"istore", Local, 2}, Lstore: {"lstore", Local, 2},
	Fstore: {"fstore", Local, 2}, Dstore: {"dstore", Local, 2}, Astore: {"astore", Local, 2},
	Istore0: {"istore_0", None, 1}, Istore1: {"istore_1", None, 1},
	Istore2: {"istore_2", None, 1}, Istore3: {"istore_3", None, 1},
	Lstore0: {"lstore_0", None, 1}, Lstore1: {"lstore_1", None, 1},
	Lstore2: {"lstore_2", None, 1}, Lstore3: {"lstore_3", None, 1},
	Fstore0: {"fstore_0", None, 1}, Fstore1: {"fstore_1", None, 1},
	Fstore2: {"fstore_2", None, 1}, Fstore3: {"fstore_3", None, 1},
	Dstore0: {"dstore_0", None, 1}, Dstore1: {"dstore_1", None, 1},
	Dstore2: {"dstore_2", None, 1}, Dstore3: {"dstore_3", None, 1},
	Astore0: {"astore_0", None, 1}, Astore1: {"astore_1", None, 1},
	Astore2: {"astore_2", None, 1}, Astore3: {"astore_3", None, 1},

	Iastore: {"iastore", None, 1}, Lastore: {"lastore", None, 1},
	Fastore: {"fastore", None, 1}, Dastore: {"dastore", None, 1},
	Aastore: {"aastore", None, 1}, Bastore: {"bastore", None, 1},
	Castore: {"castore", None, 1}, Sastore: {"sastore", None, 1},

	Pop: {"pop", None, 1}, Pop2: {"pop2", None, 1}, Dup: {"dup", None, 1},
	DupX1: {"dup_x1", None, 1}, DupX2: {"dup_x2", None, 1}, Dup2: {"dup2", None, 1},
	Dup2X1: {"dup2_x1", None, 1}, Dup2X2: {"dup2_x2", None, 1}, Swap: {"swap", None, 1},

	Iadd: {"iadd", None, 1}, Ladd: {"ladd", None, 1}, Fadd: {"fadd", None, 1}, Dadd: {"dadd", None, 1},
	Isub: {"isub", None, 1}, Lsub: {"lsub", None, 1}, Fsub: {"fsub", None, 1}, Dsub: {"dsub", None, 1},
	Imul: {"imul", None, 1}, Lmul: {"lmul", None, 1}, Fmul: {"fmul", None, 1}, Dmul: {"dmul", None, 1},
	Idiv: {"idiv", None, 1}, Ldiv: {"ldiv", None, 1}, Fdiv: {"fdiv", None, 1}, Ddiv: {"ddiv", None, 1},
	Irem: {"irem", None, 1}, Lrem: {"lrem", None, 1}, Frem: {"frem", None, 1}, Drem: {"drem", None, 1},
	Ineg: {"ineg", None, 1}, Lneg: {"lneg", None, 1}, Fneg: {"fneg", None, 1}, Dneg: {"dneg", None, 1},
	Ishl: {"ishl", None, 1}, Lshl: {"lshl", None, 1}, Ishr: {"ishr", None, 1}, Lshr: {"lshr", None, 1},
	Iushr: {"iushr", None, 1}, Lushr: {"lushr", None, 1},
	Iand: {"iand", None, 1}, Land: {"land", None, 1}, Ior: {"ior", None, 1}, Lor: {"lor", None, 1},
	Ixor: {"ixor", None, 1}, Lxor: {"lxor", None, 1},

	Iinc: {"iinc", IincK, 3},

	I2l: {"i2l", None, 1}, I2f: {"i2f", None, 1}, I2d: {"i2d", None, 1},
	L2i: {"l2i", None, 1}, L2f: {"l2f", None, 1}, L2d: {"l2d", None, 1},
	F2i: {"f2i", None, 1}, F2l: {"f2l", None, 1}, F2d: {"f2d", None, 1},
	D2i: {"d2i", None, 1}, D2l: {"d2l", None, 1}, D2f: {"d2f", None, 1},
	I2b: {"i2b", None, 1}, I2c: {"i2c", None, 1}, I2s: {"i2s", None, 1},

	Lcmp: {"lcmp", None, 1}, Fcmpl: {"fcmpl", None, 1}, Fcmpg: {"fcmpg", None, 1},
	Dcmpl: {"dcmpl", None, 1}, Dcmpg: {"dcmpg", None, 1},

	Ifeq: {"ifeq", Branch2, 3}, Ifne: {"ifne", Branch2, 3},
	Iflt: {"iflt", Branch2, 3}, Ifge: {"ifge", Branch2, 3},
	Ifgt: {"ifgt", Branch2, 3}, Ifle: {"ifle", Branch2, 3},
	IfIcmpeq: {"if_icmpeq", Branch2, 3}, IfIcmpne: {"if_icmpne", Branch2, 3},
	IfIcmplt: {"if_icmplt", Branch2, 3}, IfIcmpge: {"if_icmpge", Branch2, 3},
	IfIcmpgt: {"if_icmpgt", Branch2, 3}, IfIcmple: {"if_icmple", Branch2, 3},
	IfAcmpeq: {"if_acmpeq", Branch2, 3}, IfAcmpne: {"if_acmpne", Branch2, 3},
	Goto: {"goto", Branch2, 3}, Jsr: {"jsr", Branch2, 3}, Ret: {"ret", Local, 2},

	Tableswitch: {"tableswitch", TableswitchK, -1},
	Lookupswitch: {"lookupswitch", LookupswitchK, -1},

	Ireturn: {"ireturn", None, 1}, Lreturn: {"lreturn", None, 1},
	Freturn: {"freturn", None, 1}, Dreturn: {"dreturn", None, 1},
	Areturn: {"areturn", None, 1}, Return: {"return", None, 1},

	Getstatic: {"getstatic", ConstU2, 3}, Putstatic: {"putstatic", ConstU2, 3},
	Getfield: {"getfield", ConstU2, 3}, Putfield: {"putfield", ConstU2, 3},
	Invokevirtual: {"invokevirtual", ConstU2, 3},
	Invokespecial: {"invokespecial", ConstU2, 3},
	Invokestatic:  {"invokestatic", ConstU2, 3},
	Invokeinterface: {"invokeinterface", InterfaceK, 5},
	Invokedynamic:   {"invokedynamic", DynamicK, 5},

	New: {"new", ConstU2, 3}, Newarray: {"newarray", NewarrayK, 2},
	Anewarray: {"anewarray", ConstU2, 3}, Arraylength: {"arraylength", None, 1},
	Athrow: {"athrow", None, 1},
	Checkcast: {"checkcast", ConstU2, 3}, Instanceof: {"instanceof", ConstU2, 3},
	Monitorenter: {"monitorenter", None, 1}, Monitorexit: {"monitorexit", None, 1},

	Wide: {"wide", WideK, -1},
	Multianewarray: {"multianewarray", MultiK, 4},
	Ifnull: {"ifnull", Branch2, 3}, Ifnonnull: {"ifnonnull", Branch2, 3},
	GotoW: {"goto_w", Branch4, 5}, JsrW: {"jsr_w", Branch4, 5},

	// Reserved (§6.2): named for diagnostics, Unused so Valid() rejects them.
	Breakpoint: {"breakpoint", Unused, 1},
	Impdep1:    {"impdep1", Unused, 1},
	Impdep2:    {"impdep2", Unused, 1},
}