package lower

import (
	"sort"

	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// §4.12.4 and §8.1.3: what an inner class closes over.
//
// A capture is legal only for an effectively final local, which flow already
// decided, so this package copies by value into a final synthetic field and
// never worries about aliasing. Every constructor gains a parameter per
// capture, plus one for the enclosing instance, and pass two stores them
// before anything else runs.

type captureField struct {
	local *sym.VarSym // the enclosing method's local
	name  string      // val$x
	desc  string
	slot  int // the constructor parameter slot, assigned in declareCtor
}

// declareCaptures adds this$0 and the capture fields. It runs before any
// constructor is declared, because it changes their descriptors.
func (cc *classCtx) declareCaptures() {
	c := cc.sym

	// §8.1.3: an inner class — a member class that is not static, or any local
	// or anonymous class in an instance context — holds its enclosing instance.
	if c.Outer != nil && !c.Flags.Has(sym.FlagStatic) {
		cc.thisField = "this$0"
		cc.b.Field(fieldFlags(sym.FlagFinal|sym.FlagSynthetic),
			cc.thisField, "L"+c.Outer.Binary+";")
	}

	for _, v := range cc.capturedLocals() {
		t := cc.tt.FieldType(v)
		f := &captureField{
			local: v,
			name:  "val$" + v.Name,
			desc:  types.Descriptor(t).String(),
		}
		cc.b.Field(fieldFlags(sym.FlagFinal|sym.FlagSynthetic), f.name, f.desc)
		cc.captures = append(cc.captures, f)
	}
}

// capturedLocals is flow.Captured, restricted to the method that encloses this
// class and ordered deterministically. flow keys the map by the *enclosing*
// method, so the entry to read is the one whose body declares this class.
func (cc *classCtx) capturedLocals() []*sym.VarSym {
	if cc.fl == nil {
		return nil
	}
	owner, _ := cc.sym.Owner.(*sym.MethodSym)
	if owner == nil {
		return nil // a member class captures nothing
	}
	list := append([]*sym.VarSym(nil), cc.fl.Captured[owner]...)

	// Map iteration is not the source of this slice, but flow's own collection
	// order is not part of its contract, and the descriptor a capture produces
	// is part of the artifact. Sort by name, then by position.
	sort.Slice(list, func(i, j int) bool {
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].Pos < list[j].Pos
	})

	out := list[:0]
	for _, v := range list {
		if cc.fl.EffectivelyFinal[v] {
			out = append(out, v)
		}
		// A capture flow rejected was already reported there; emitting a field
		// for it would turn one diagnostic into a broken class.
	}
	return out
}

// ctorDesc rewrites a constructor's descriptor: the enclosing instance first,
// then the declared parameters, then one per capture. javac's order, so a
// disassembly diff lines up.
func (cc *classCtx) ctorDesc(m *sym.MethodSym) string {
	params := make([]types.Type, 0, len(m.Params)+len(cc.captures)+1)
	if cc.thisField != "" {
		params = append(params, cc.outerType())
	}
	mt := cc.tt.MethodType(m)
	params = append(params, mt.Params...)
	for _, cap := range cc.captures {
		params = append(params, cc.tt.FieldType(cap.local))
	}
	return types.MethodDescriptor(&types.MethodType{Params: params, Result: types.Void})
}

func (cc *classCtx) defaultCtorDesc() string {
	params := make([]types.Type, 0, len(cc.captures)+1)
	if cc.thisField != "" {
		params = append(params, cc.outerType())
	}
	for _, cap := range cc.captures {
		params = append(params, cc.tt.FieldType(cap.local))
	}
	return types.MethodDescriptor(&types.MethodType{Params: params, Result: types.Void})
}

func (cc *classCtx) outerType() types.Type {
	if cc.sym.Outer == nil {
		return cc.tt.Object()
	}
	return &types.ClassType{Sym: cc.sym.Outer}
}

// storeCaptures emits the prologue every constructor of a capturing class
// begins with: this$0 and each val$x, copied from parameter to field.
//
// javac stores them before the super() call, which is illegal for ordinary
// code and legal here because §8.8.7's prologue rule exempts assignments to
// the fields of the class being constructed. Storing after super() would let a
// superclass constructor calling an overridden method see a null capture.
func (e *emitter) storeCaptures() {
	if e.thisField != "" {
		e.c.Aload(0)
		e.c.Aload(1)
		e.c.PutField(e.sym.Binary, e.thisField, "L"+e.sym.Outer.Binary+";")
	}
	for _, cap := range e.captures {
		e.c.Aload(0)
		e.loadLocal(cap.slot, e.tt.FieldType(cap.local))
		e.c.PutField(e.sym.Binary, cap.name, cap.desc)
	}
}

// captureOf reports the field a name resolves to when it names a captured
// local from inside the capturing class, rather than a local of this method.
func (e *emitter) captureOf(v *sym.VarSym) *captureField {
	for _, cap := range e.captures {
		if cap.local == v {
			return cap
		}
	}
	return nil
}