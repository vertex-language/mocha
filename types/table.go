package types

import (
	"sync"

	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/sym"
)

// Table resolves types for one compilation. It owns the completion cache and
// is safe for concurrent use.
//
// Completion never recurses into the same class. A class's type parameters are
// published as shells before their bounds are resolved, so an F-bounded
// declaration — enum Enum<E extends Enum<E>> — resolves without cycle
// detection; and building a ClassType for another class never completes it.
// The guard against a cyclic hierarchy lives in the supertype walk instead.
type Table struct {
	syms *sym.Table

	mu      sync.Mutex
	classes map[*sym.ClassSym]*classInfo
	methods map[*sym.MethodSym]*MethodType
	fields  map[*sym.VarSym]Type
	units   map[*sym.ClassSym]*sym.Unit
}

// NewTable returns a table resolving against st.
func NewTable(st *sym.Table) *Table {
	return &Table{
		syms:    st,
		classes: make(map[*sym.ClassSym]*classInfo),
		methods: make(map[*sym.MethodSym]*MethodType),
		fields:  make(map[*sym.VarSym]Type),
		units:   make(map[*sym.ClassSym]*sym.Unit),
	}
}

// Syms returns the symbol table this table resolves against.
func (t *Table) Syms() *sym.Table { return t.syms }

// Register records the unit a source class was entered from.
//
// It is required for source classes and harmless for anything else: a
// sym.ClassSym does not carry the *sym.Unit that entered it, and resolving a
// simple name in a declaration needs that unit's imports (§6.5.5). Member
// types inherit their top-level ancestor's registration, so only the unit's
// top-level types are recorded here.
func (t *Table) Register(u *sym.Unit) {
	if u == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range u.Types {
		t.units[c] = u
	}
}

// unitOf finds the unit a class was entered from, walking outward through
// enclosing classes.
func (t *Table) unitOf(c *sym.ClassSym) *sym.Unit {
	t.mu.Lock()
	defer t.mu.Unlock()
	for x := c; x != nil; x = x.Outer {
		if u, ok := t.units[x]; ok {
			return u
		}
	}
	return nil
}

// completionState tracks a lazily computed half of a classInfo.
type completionState uint8

const (
	notDone completionState = iota
	inProgress
	done
)

// classInfo caches one class's declared type: its own type parameters, its
// superclass, and its superinterfaces. The two halves complete separately
// because bounds may name the class's own parameters but supertypes may not
// name themselves.
type classInfo struct {
	mu sync.Mutex

	paramState completionState
	params     []*TypeVar

	superState completionState
	super      Type
	ifaces     []Type
}

func (t *Table) infoOf(c *sym.ClassSym) *classInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	ci, ok := t.classes[c]
	if !ok {
		ci = &classInfo{}
		t.classes[c] = ci
	}
	return ci
}

// TypeParams returns the type parameters a class declares, in order. It is
// empty for a non-generic class.
func (t *Table) TypeParams(c *sym.ClassSym) []*TypeVar {
	if c == nil {
		return nil
	}
	ci := t.infoOf(c)
	ci.mu.Lock()
	defer ci.mu.Unlock()
	if ci.paramState == done {
		return ci.params
	}
	if ci.paramState == inProgress {
		// A bound naming its own owner's parameters. The shells are already
		// published, so this is the F-bounded case and not an error.
		return ci.params
	}
	ci.paramState = inProgress
	t.completeParams(c, ci)
	ci.paramState = done
	return ci.params
}

// completeParams builds the shells, publishes them, then resolves the bounds.
// The caller holds ci.mu.
func (t *Table) completeParams(c *sym.ClassSym, ci *classInfo) {
	_ = c.Complete() // members are not needed, but sym.TypeParams are set by Enter

	if c.FromSource() {
		ci.params = make([]*TypeVar, len(c.TypeParams))
		for i, ps := range c.TypeParams {
			ci.params[i] = &TypeVar{Sym: ps}
		}
		e := &env{tparams: ci.params, unit: t.unitOf(c), class: c}
		for i, ps := range c.TypeParams {
			ci.params[i].Bound = t.boundOf(e, ps.Bounds)
		}
		return
	}

	sig, ok := parseClassSignature(classSignature(c))
	if !ok || len(sig.params) == 0 {
		return
	}
	ci.params = make([]*TypeVar, len(sig.params))
	for i, p := range sig.params {
		ci.params[i] = &TypeVar{Sym: &sym.TypeParamSym{
			Sym: sym.Sym{
				Name:  p.name,
				Kind:  sym.KindTypeParam,
				Owner: c,
			},
			Index: i,
		}}
	}
	e := &env{tparams: ci.params}
	for i, p := range sig.params {
		var bounds []Type
		for _, b := range p.bounds {
			bounds = append(bounds, t.fromSig(e, b))
		}
		ci.params[i].Bound = t.orObject(intersect(bounds))
	}
}

// boundOf resolves a source type parameter's bound list.
func (t *Table) boundOf(e *env, exprs []ast.Type) Type {
	var bounds []Type
	for _, b := range exprs {
		bounds = append(bounds, t.fromAST(e, b))
	}
	return t.orObject(intersect(bounds))
}

// orObject substitutes java.lang.Object for an absent bound, so TypeVar.Bound
// is never nil after completion and Erase never has to reach for a table.
func (t *Table) orObject(b Type) Type {
	if b != nil {
		return b
	}
	return t.Object()
}

// Supertype returns a class's direct superclass. It is nil for
// java.lang.Object and for an interface, matching sym.ClassSym.Super.
func (t *Table) Supertype(c *sym.ClassSym) Type {
	t.completeSupers(c)
	ci := t.infoOf(c)
	ci.mu.Lock()
	defer ci.mu.Unlock()
	return ci.super
}

// Interfaces returns a class's direct superinterfaces, in declaration order.
func (t *Table) Interfaces(c *sym.ClassSym) []Type {
	t.completeSupers(c)
	ci := t.infoOf(c)
	ci.mu.Lock()
	defer ci.mu.Unlock()
	return ci.ifaces
}

func (t *Table) completeSupers(c *sym.ClassSym) {
	if c == nil {
		return
	}
	params := t.TypeParams(c) // outside ci.mu: its own lock, and ordered first

	ci := t.infoOf(c)
	ci.mu.Lock()
	defer ci.mu.Unlock()
	if ci.superState != notDone {
		return
	}
	ci.superState = inProgress

	if c.FromSource() {
		e := &env{tparams: params, unit: t.unitOf(c), class: c}
		ci.super, ci.ifaces = t.sourceSupers(c, e)
	} else {
		e := &env{tparams: params}
		ci.super, ci.ifaces = t.binarySupers(c, e)
	}
	ci.superState = done
}

// binarySupers reads the class signature when there is one, and falls back to
// the plain internal names sym already carries when there is not — which is
// the common case, since only a generic class needs a Signature at all.
func (t *Table) binarySupers(c *sym.ClassSym, e *env) (Type, []Type) {
	if sig, ok := parseClassSignature(classSignature(c)); ok && sig.super != nil {
		super := t.fromSig(e, sig.super)
		ifaces := make([]Type, 0, len(sig.ifaces))
		for _, i := range sig.ifaces {
			ifaces = append(ifaces, t.fromSig(e, i))
		}
		return super, ifaces
	}
	var super Type
	if c.Super != "" {
		super = t.named(c.Super)
	}
	ifaces := make([]Type, 0, len(c.Interfaces))
	for _, i := range c.Interfaces {
		ifaces = append(ifaces, t.named(i))
	}
	return super, ifaces
}

// MethodType returns a method's signature, filling sym.MethodSym.Descriptor as
// a side effect for a symbol entered from source. sym defers that here
// deliberately: building a descriptor needs erasure, and erasure needs types.
func (t *Table) MethodType(m *sym.MethodSym) *MethodType {
	if m == nil {
		return nil
	}
	t.mu.Lock()
	if mt, ok := t.methods[m]; ok {
		t.mu.Unlock()
		return mt
	}
	t.mu.Unlock()

	mt := t.buildMethodType(m)

	t.mu.Lock()
	if prev, ok := t.methods[m]; ok { // lost a race; one answer wins
		t.mu.Unlock()
		return prev
	}
	t.methods[m] = mt
	t.mu.Unlock()

	if m.Descriptor == "" {
		m.Descriptor = MethodDescriptor(mt)
	}
	return mt
}

func (t *Table) buildMethodType(m *sym.MethodSym) *MethodType {
	owner := t.TypeParams(m.Class)

	if m.FromSource() {
		e := &env{tparams: owner, unit: t.unitOf(m.Class), class: m.Class}
		mt := &MethodType{}
		// The method's own parameters shadow the class's, so they go first
		// and are published before their bounds resolve, exactly as a class's
		// are.
		for i, ps := range m.TypeParams {
			tv := &TypeVar{Sym: ps}
			mt.TypeParams = append(mt.TypeParams, tv)
			_ = i
		}
		e = e.with(mt.TypeParams)
		for i, ps := range m.TypeParams {
			mt.TypeParams[i].Bound = t.boundOf(e, ps.Bounds)
		}

		for _, p := range m.Params {
			pt := t.fromAST(e, p.TypeExpr)
			if p.Decl != nil {
				if fp, ok := p.Decl.(*ast.Param); ok && fp.Ellipsis.IsValid() {
					pt = &ArrayType{Elem: pt}
				}
			}
			mt.Params = append(mt.Params, pt)
		}
		mt.Result = Void
		if m.Result != nil {
			mt.Result = t.fromAST(e, m.Result)
		}
		for _, x := range m.ThrowsExpr {
			mt.Throws = append(mt.Throws, t.fromAST(e, x))
		}
		return mt
	}

	// Binary. The signature carries generics when present; the descriptor is
	// always there and is the fallback.
	if sig, ok := parseMethodSignature(methodSignature(m)); ok {
		mt := &MethodType{}
		for i, p := range sig.params {
			mt.TypeParams = append(mt.TypeParams, &TypeVar{Sym: &sym.TypeParamSym{
				Sym:   sym.Sym{Name: p.name, Kind: sym.KindTypeParam, Owner: m},
				Index: i,
			}})
		}
		e := (&env{tparams: owner}).with(mt.TypeParams)
		for i, p := range sig.params {
			var bounds []Type
			for _, b := range p.bounds {
				bounds = append(bounds, t.fromSig(e, b))
			}
			mt.TypeParams[i].Bound = t.orObject(intersect(bounds))
		}
		for _, p := range sig.args {
			mt.Params = append(mt.Params, t.fromSig(e, p))
		}
		mt.Result = t.fromSig(e, sig.result)
		for _, x := range sig.throws {
			mt.Throws = append(mt.Throws, t.fromSig(e, x))
		}
		if len(mt.Throws) == 0 {
			for _, x := range m.Throws {
				mt.Throws = append(mt.Throws, t.named(x))
			}
		}
		return mt
	}
	return t.fromMethodDescriptor(m)
}

// FieldType returns a variable's declared type, filling
// sym.VarSym.Descriptor as a side effect for a source field.
func (t *Table) FieldType(v *sym.VarSym) Type {
	if v == nil {
		return errorType("")
	}
	t.mu.Lock()
	if ft, ok := t.fields[v]; ok {
		t.mu.Unlock()
		return ft
	}
	t.mu.Unlock()

	ft := t.buildFieldType(v)

	t.mu.Lock()
	if prev, ok := t.fields[v]; ok {
		t.mu.Unlock()
		return prev
	}
	t.fields[v] = ft
	t.mu.Unlock()

	if v.Descriptor == "" && ft.Kind() != KindError {
		v.Descriptor = Descriptor(ft).String()
	}
	return ft
}

func (t *Table) buildFieldType(v *sym.VarSym) Type {
	if v.FromSource() {
		owner := v.Class
		if owner == nil && v.Method != nil {
			owner = v.Method.Class
		}
		e := &env{
			tparams: t.TypeParams(owner),
			unit:    t.unitOf(owner),
			class:   owner,
		}
		if v.Method != nil {
			if mt := t.MethodType(v.Method); mt != nil {
				e = e.with(mt.TypeParams)
			}
		}
		return t.fromAST(e, v.TypeExpr)
	}

	e := &env{tparams: t.TypeParams(v.Class)}
	if s, ok := parseFieldSignature(fieldSignature(v)); ok {
		return t.fromSig(e, s)
	}
	return t.fromDescriptor(v.Descriptor)
}

// --- well-known types -------------------------------------------------------

// Object returns java.lang.Object as a raw class type, or an error type when
// the path has none.
func (t *Table) Object() Type { return t.named(sym.ObjectName) }

// String returns java.lang.String.
func (t *Table) String_() Type { return t.named(sym.StringName) }

// named resolves an internal-form binary name to a raw class type.
func (t *Table) named(binary string) Type {
	if binary == "" {
		return errorType("")
	}
	// An array class appears in a class file as its own descriptor.
	if binary[0] == '[' {
		return t.fromDescriptor(binary)
	}
	c := t.syms.Class(binary)
	if c == nil {
		return errorType(binary)
	}
	return &ClassType{Sym: c}
}

// classOf builds a class type, dropping arguments that do not match the
// declaration's arity — a mismatch is attr's to report, not a reason to
// produce something unusable here.
func (t *Table) classOf(c *sym.ClassSym, args []Type, outer *ClassType) Type {
	if c == nil {
		return errorType("")
	}
	if len(args) > 0 && len(args) != len(t.TypeParams(c)) {
		args = nil
	}
	return &ClassType{Sym: c, Args: args, Outer: outer}
}

// --- resolution environment -------------------------------------------------

// env is the scope a type reference resolves against: the type parameters in
// scope, innermost first, plus the unit and enclosing class for a source
// reference. It is passed down rather than looked up so that resolving a
// class's own bounds never re-enters the table for that class.
type env struct {
	tparams []*TypeVar
	unit    *sym.Unit
	class   *sym.ClassSym
}

// with returns a copy of e with more type parameters in scope. The new ones
// shadow the old, so they are searched first.
func (e *env) with(tps []*TypeVar) *env {
	if len(tps) == 0 {
		return e
	}
	n := &env{unit: e.unit, class: e.class}
	n.tparams = append(append([]*TypeVar{}, tps...), e.tparams...)
	return n
}

// lookupVar finds a type variable by name.
func (e *env) lookupVar(name string) *TypeVar {
	if e == nil {
		return nil
	}
	for _, tv := range e.tparams {
		if tv.Sym != nil && tv.Sym.Name == name {
			return tv
		}
	}
	return nil
}