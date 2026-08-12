package lower

import (
	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/jvm/op"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// An item is an addressable location: a local, a field, a static, or an array
// element. This is javac's Items, and it is why compound assignment is not
// desugared into a temporary — the JVM has the instructions, and spilling would
// generate worse code than javac for no reason.
//
// The protocol has four operations and one invariant: an lvalue is evaluated
// once. `a[i()] += f()` evaluates the arrayref and the index one time, dup2s
// them to reload, and dup_x2s the result out if the enclosing expression wants
// it.
type item interface {
	// addr pushes whatever the load and store need beneath the value: an
	// objectref, an arrayref and index, or nothing.
	addr(e *emitter)

	// dupAddr duplicates what addr pushed, so one evaluation serves a load and
	// a following store.
	dupAddr(e *emitter)

	// load consumes the operands addr pushed and leaves the value.
	load(e *emitter)

	// store consumes the operands addr pushed plus a value on top.
	store(e *emitter)

	// stash duplicates the value on top of the stack out from under the
	// operands addr pushed, leaving a copy below them for the enclosing
	// expression to consume after the store.
	stash(e *emitter)

	// typ is the item's type, which picks the opcode variant.
	typ() types.Type
}

// ---- local ----

type localItem struct {
	slot int
	t    types.Type
}

func (l localItem) addr(e *emitter)    {}
func (l localItem) dupAddr(e *emitter) {}
func (l localItem) typ() types.Type    { return l.t }

func (l localItem) load(e *emitter) { e.loadLocal(l.slot, l.t) }

func (l localItem) store(e *emitter) { e.storeLocal(l.slot, l.t) }

func (l localItem) stash(e *emitter) { e.dupValue(l.t) }

// ---- static field ----

type staticItem struct {
	owner, name, desc string
	t                 types.Type
}

func (s staticItem) addr(e *emitter)    {}
func (s staticItem) dupAddr(e *emitter) {}
func (s staticItem) typ() types.Type    { return s.t }

func (s staticItem) load(e *emitter)  { e.c.GetStatic(s.owner, s.name, s.desc) }
func (s staticItem) store(e *emitter) { e.c.PutStatic(s.owner, s.name, s.desc) }
func (s staticItem) stash(e *emitter) { e.dupValue(s.t) }

// ---- instance field ----

type fieldItem struct {
	owner, name, desc string
	t                 types.Type
}

func (f fieldItem) addr(e *emitter) {} // the objectref is pushed by the caller

func (f fieldItem) dupAddr(e *emitter) { e.c.Op(op.Dup) }

func (f fieldItem) typ() types.Type { return f.t }

func (f fieldItem) load(e *emitter)  { e.c.GetField(f.owner, f.name, f.desc) }
func (f fieldItem) store(e *emitter) { e.c.PutField(f.owner, f.name, f.desc) }

// stash moves the value under the objectref: dup_x1 for a one-slot value,
// dup2_x1 for a long or a double.
func (f fieldItem) stash(e *emitter) {
	if types.Slots(f.t) == 2 {
		e.c.Op(op.Dup2X1)
	} else {
		e.c.Op(op.DupX1)
	}
}

// ---- array element ----

type indexItem struct {
	t types.Type // the element type
}

func (a indexItem) addr(e *emitter) {} // arrayref and index pushed by the caller

func (a indexItem) dupAddr(e *emitter) { e.c.Op(op.Dup2) }

func (a indexItem) typ() types.Type { return a.t }

// An array element load is iaload or aaload by element type, not by anything
// syntactic.
func (a indexItem) load(e *emitter) { e.c.Op(arrayLoadOp(a.t)) }

func (a indexItem) store(e *emitter) { e.c.Op(arrayStoreOp(a.t)) }

func (a indexItem) stash(e *emitter) {
	if types.Slots(a.t) == 2 {
		e.c.Op(op.Dup2X2)
	} else {
		e.c.Op(op.DupX2)
	}
}

func arrayLoadOp(t types.Type) op.Op {
	switch t.Kind() {
	case types.KindBoolean, types.KindByte:
		return op.Baload
	case types.KindChar:
		return op.Caload
	case types.KindShort:
		return op.Saload
	case types.KindInt:
		return op.Iaload
	case types.KindLong:
		return op.Laload
	case types.KindFloat:
		return op.Faload
	case types.KindDouble:
		return op.Daload
	}
	return op.Aaload
}

func arrayStoreOp(t types.Type) op.Op {
	switch t.Kind() {
	case types.KindBoolean, types.KindByte:
		return op.Bastore
	case types.KindChar:
		return op.Castore
	case types.KindShort:
		return op.Sastore
	case types.KindInt:
		return op.Iastore
	case types.KindLong:
		return op.Lastore
	case types.KindFloat:
		return op.Fastore
	case types.KindDouble:
		return op.Dastore
	}
	return op.Aastore
}

// ---- the shared load/store helpers ----

// Opcodes are chosen by erased type. Every expression picks its i/l/f/d/a
// variant from the type attr recorded.
func (e *emitter) loadLocal(slot int, t types.Type) {
	switch t.Kind() {
	case types.KindBoolean, types.KindByte, types.KindChar,
		types.KindShort, types.KindInt:
		e.c.Iload(slot)
	case types.KindLong:
		e.c.Lload(slot)
	case types.KindFloat:
		e.c.Fload(slot)
	case types.KindDouble:
		e.c.Dload(slot)
	default:
		e.c.Aload(slot)
	}
}

func (e *emitter) storeLocal(slot int, t types.Type) {
	switch t.Kind() {
	case types.KindBoolean, types.KindByte, types.KindChar,
		types.KindShort, types.KindInt:
		e.c.Istore(slot)
	case types.KindLong:
		e.c.Lstore(slot)
	case types.KindFloat:
		e.c.Fstore(slot)
	case types.KindDouble:
		e.c.Dstore(slot)
	default:
		e.c.Astore(slot)
	}
}

func (e *emitter) dupValue(t types.Type) {
	if types.Slots(t) == 2 {
		e.c.Op(op.Dup2)
	} else {
		e.c.Op(op.Dup)
	}
}

// pop discards a value. A statement's value is discarded: the same node emits
// differently in expression and statement position, and the tree shape tells us
// which up front, so we never restart code generation the way ECJ does.
func (e *emitter) pop(t types.Type) {
	switch types.Slots(t) {
	case 0: // void
	case 2:
		e.c.Op(op.Pop2)
	default:
		e.c.Op(op.Pop)
	}
}

// fieldOwner is the internal name a field reference names.
func fieldOwner(v *sym.VarSym) string {
	if v.Class == nil {
		bug("field %s has no declaring class", v.Name)
	}
	return v.Class.Binary
}

// itemFor builds the item for a resolved variable, without pushing anything.
// The caller pushes the receiver for an instance field.
func (e *emitter) itemFor(v *sym.VarSym) item {
	t := e.tt.FieldType(v)
	switch v.Var {
	case sym.VarField:
		desc := types.Descriptor(t).String()
		if v.Flags.Has(sym.FlagStatic) {
			return staticItem{owner: fieldOwner(v), name: v.Name, desc: desc, t: t}
		}
		return fieldItem{owner: fieldOwner(v), name: v.Name, desc: desc, t: t}
	default:
		return localItem{slot: e.slots.slot(v), t: t}
	}
}

var _ = classfile.AccPublic // keep the import honest until expr.go lands