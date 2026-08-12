package lower

import (
	"fmt"

	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// Pass one — declare.
//
// Runs once per class, mutates the Builder, and is where every member the
// language adds on your behalf comes from. Everything with a side effect
// happens here, outside the closure: slot assignment, member creation, pool
// interning of names. Everything inside the closure is a pure function of the
// tree, because the closure may be replayed.

// initEntry is one thing folded into a constructor or into <clinit>: a field
// initialiser, or an initialiser block. Source order is the only order that
// matters, so the two live in one list.
type initEntry struct {
	field *sym.VarSym // nil for a block
	init  ast.Node    // an Expr or an *ArrayInit; nil for a block
	block *ast.Block  // nil for a field
	pos   token.Pos
}

// ctorPlan is what pass two folds into one constructor.
type ctorPlan struct {
	// inits are the instance field initialisers and instance blocks, in source
	// order. They are emitted after the super() call and before the body,
	// and only for a constructor that does not chain to this(...).
	inits []initEntry

	// explicit is the this(...) or super(...) the body opens with, or nil when
	// §8.8.7 supplies an implicit super().
	explicit *ast.ConstructorCall

	// bodyFrom is the index into the body's statement list at which the real
	// body begins — one past an explicit constructor invocation.
	bodyFrom int
}

type clinitPlan struct{ inits []initEntry }

func (cc *classCtx) declare() {
	c := cc.sym

	// Captures first: this$0 and the capture fields change every constructor's
	// descriptor, and nothing else can be declared until that is known.
	cc.declareCaptures()

	var ctors, instInits, staticInits []initEntry
	sawCtor := false

	for _, d := range cc.members() {
		switch n := d.(type) {
		case *ast.VarDecl:
			for _, decl := range n.Names {
				v := cc.fieldSym(decl)
				if v == nil {
					continue
				}
				cc.declareField(v)
				if decl.Init == nil {
					continue
				}
				// A static final with a folded constant becomes a
				// ConstantValue attribute and emits no code at all.
				if v.Flags.Has(sym.FlagStatic|sym.FlagFinal) && cc.constField(v, decl) {
					continue
				}
				e := initEntry{field: v, init: decl.Init, pos: decl.Pos()}
				if v.Flags.Has(sym.FlagStatic) {
					staticInits = append(staticInits, e)
				} else {
					instInits = append(instInits, e)
				}
			}

		case *ast.InitializerDecl:
			e := initEntry{block: n.Body, pos: n.Pos()}
			if n.Static {
				staticInits = append(staticInits, e)
			} else {
				instInits = append(instInits, e)
			}

		case *ast.MethodDecl:
			if m := cc.methodSym(n); m != nil {
				cc.declareMethod(m, n.Body)
			}

		case *ast.ConstructorDecl:
			if m := cc.methodSym(n); m != nil {
				sawCtor = true
				ctors = append(ctors, initEntry{}) // presence marker only
				cc.declareCtor(m, n)
			}
		}
	}
	_ = ctors

	// §8.8.9: a class with no constructor written gets a default one.
	if !sawCtor && !c.IsInterface() && !c.Flags.Has(sym.FlagAbstract|sym.FlagInterface) {
		cc.declareDefaultCtor()
	}

	// The instance initialisers are folded into every constructor that does
	// not chain to this(...), so they are attached after all of them exist.
	for _, p := range cc.pending {
		if p.ctor != nil && p.ctor.explicit.isSuperOrImplicit() {
			p.ctor.inits = instInits
		}
	}

	// §8.7: static initialisers and static field initialisers become <clinit>,
	// in source order. Anything a desugaring needs there is appended by the
	// phase that needed it — the enum $VALUES array, an enum switch map — so
	// this runs after the member walk.
	staticInits = append(staticInits, cc.enumStatics()...)
	staticInits = append(staticInits, cc.switchMapStatics()...)
	if len(staticInits) > 0 {
		cc.declareClinit(staticInits)
	}

	cc.declareRecordMembers()
	cc.declareBridges()   // bridge.go
	cc.scanAccessors()    // bridge.go
	cc.scanUnsupported()  // below
}

// members returns the class body's declarations in source order. An anonymous
// class entered by attr may carry no ast.Decl of its own, in which case its
// members were already reached through the NewExpr that declared it.
func (cc *classCtx) members() []ast.Decl {
	switch d := cc.sym.Decl.(type) {
	case *ast.ClassDecl:
		return d.Members
	case *ast.InterfaceDecl:
		return d.Members
	case *ast.EnumDecl:
		return d.Members
	case *ast.RecordDecl:
		return d.Members
	case *ast.AnnotationDecl:
		return d.Members
	}
	return nil
}

func (cc *classCtx) fieldSym(d *ast.VarDeclarator) *sym.VarSym {
	if d.Name == nil || d.Name.Underscore {
		return nil // §6.1: an unnamed field cannot exist, but a broken tree can
	}
	return cc.sym.Field(d.Name.Name(cc.src))
}

// methodSym finds the symbol a declaration produced. sym entered it; matching
// on the node is exact, where matching on the name would not be.
func (cc *classCtx) methodSym(d ast.Decl) *sym.MethodSym {
	var found *sym.MethodSym
	cc.sym.Members.Each(func(s sym.Symbol) bool {
		if m, ok := s.(*sym.MethodSym); ok && m.Decl == d {
			found = m
			return false
		}
		return true
	})
	return found
}

func (cc *classCtx) declareField(v *sym.VarSym) {
	t := cc.tt.FieldType(v)
	f := cc.b.Field(fieldFlags(v.Flags), v.Name, types.Descriptor(t).String())
	_ = f
}

// constField attaches a ConstantValue when attr folded the initialiser. §4.7.2
// admits only the five tags, which is exactly the set attr can fold to.
func (cc *classCtx) constField(v *sym.VarSym, d *ast.VarDeclarator) bool {
	init, ok := d.Init.(ast.Expr)
	if !ok {
		return false
	}
	k, ok := cc.in.Const(init)
	if !ok {
		return false
	}
	t := cc.tt.FieldType(v)
	fb := cc.field(v.Name, types.Descriptor(t).String())
	if fb == nil {
		return false
	}
	switch val := k.Value.(type) {
	case bool:
		n := int32(0)
		if val {
			n = 1
		}
		fb.ConstantInt(n)
	case int32:
		fb.ConstantInt(val)
	case int64:
		fb.ConstantLong(val)
	case float32:
		fb.ConstantFloat(val)
	case float64:
		fb.ConstantDouble(val)
	case string:
		fb.ConstantString(val)
	default:
		return false
	}
	return true
}

// field re-finds a FieldBuilder by name. Builder does not expose a lookup, so
// declareField hands them back through this map rather than the package
// keeping two lists.
func (cc *classCtx) field(name, descriptor string) *classfile.FieldBuilder {
	if cc.fields == nil {
		return nil
	}
	return cc.fields[name]
}

func (cc *classCtx) declareMethod(m *sym.MethodSym, body *ast.Block) {
	desc := cc.methodDesc(m)
	flags := methodFlags(m.Flags)
	if cc.sym.IsInterface() && body == nil {
		flags |= classfile.AccAbstract | classfile.AccPublic // §9.4
	}
	mb := cc.b.Method(flags, m.Name, desc)

	for _, t := range m.Throws {
		mb.Throws(t)
	}
	if body == nil {
		return // abstract or native: no closure, no slots
	}

	s := newSlotMap()
	if !m.Flags.Has(sym.FlagStatic) {
		s.reserve(1) // this
	}
	for _, p := range m.Params {
		s.declare(p, cc.tt.FieldType(p))
	}
	cc.slots[m] = s
	cc.pending = append(cc.pending, &pendingBody{mb: mb, m: m, body: body})
}

func (cc *classCtx) declareCtor(m *sym.MethodSym, d *ast.ConstructorDecl) {
	desc := cc.ctorDesc(m) // capture.go: adds this$0 and the capture parameters
	mb := cc.b.Method(methodFlags(m.Flags), sym.InitName, desc)
	for _, t := range m.Throws {
		mb.Throws(t)
	}

	s := newSlotMap()
	s.reserve(1) // this
	if cc.thisField != "" {
		s.reserve(1) // the enclosing instance parameter
	}
	for _, p := range m.Params {
		s.declare(p, cc.tt.FieldType(p))
	}
	for _, cap := range cc.captures {
		cap.slot = s.declare(cap.local, cc.tt.FieldType(cap.local))
	}
	cc.slots[m] = s

	plan := &ctorPlan{}
	if d.Body != nil {
		plan.explicit, plan.bodyFrom = explicitCall(d.Body)
	}
	cc.pending = append(cc.pending, &pendingBody{mb: mb, m: m, body: d.Body, ctor: plan})
}

// explicitCall finds a leading this(...) or super(...). A flexible constructor
// body may place statements before it, which is why ast makes ConstructorCall
// a statement rather than splitting the list — but at 49.0 a prologue has
// nowhere to run, so anything before the call is left where it is and the
// index is what pass two skips.
func explicitCall(b *ast.Block) (*ast.ConstructorCall, int) {
	for i, s := range b.Stmts {
		if cc, ok := s.(*ast.ConstructorCall); ok {
			return cc, i + 1
		}
	}
	return nil, 0
}

// isSuperOrImplicit reports whether field initialisers belong in this
// constructor: they do unless it chains to this(...), which will run them.
func (p *ast.ConstructorCall) isSuperOrImplicit() bool {
	return p == nil || p.Kind == token.SUPER
}

func (cc *classCtx) declareDefaultCtor() {
	desc := cc.defaultCtorDesc()
	mb := cc.b.Method(defaultCtorFlags(cc.sym.Flags), sym.InitName, desc)

	s := newSlotMap()
	s.reserve(1)
	if cc.thisField != "" {
		s.reserve(1)
	}
	for _, cap := range cc.captures {
		cap.slot = s.declare(cap.local, cc.tt.FieldType(cap.local))
	}

	m := &sym.MethodSym{}
	m.Name = sym.InitName
	m.Kind = sym.KindMethod
	m.Flags = sym.FlagImplicit
	m.Class = cc.sym
	cc.slots[m] = s
	cc.pending = append(cc.pending, &pendingBody{mb: mb, m: m, body: nil, ctor: &ctorPlan{}})
}

// §8.8.9: the default constructor takes the class's access modifier, except in
// an enum, where it is private.
func defaultCtorFlags(f sym.Flags) classfile.Flags {
	if f.Has(sym.FlagEnum) {
		return classfile.AccPrivate
	}
	return classFlags(f) &^ (classfile.AccSuper | classfile.AccAbstract |
		classfile.AccInterface | classfile.AccEnum | classfile.AccFinal)
}

func (cc *classCtx) declareClinit(inits []initEntry) {
	mb := cc.b.Method(classfile.AccStatic, sym.ClinitName, "()V")

	m := &sym.MethodSym{}
	m.Name = sym.ClinitName
	m.Kind = sym.KindMethod
	m.Flags = sym.FlagStatic | sym.FlagImplicit
	m.Class = cc.sym

	cc.slots[m] = newSlotMap() // static: no receiver
	cc.pending = append(cc.pending, &pendingBody{
		mb: mb, m: m, clinit: &clinitPlan{inits: inits},
	})
}

// methodDesc is the erased descriptor. types already filled MethodSym
// .Descriptor for a source symbol as a side effect of MethodType; pass one
// copies it and adjusts only what it changed.
func (cc *classCtx) methodDesc(m *sym.MethodSym) string {
	if m.Descriptor != "" {
		return m.Descriptor
	}
	return types.MethodDescriptor(cc.tt.MethodType(m))
}

// scanUnsupported reports the one class of failure no earlier phase can see: a
// construct the encoder cannot express at 49.0. Reported here, against a source
// position, rather than panicking from inside a replayed closure.
func (cc *classCtx) scanUnsupported() {
	for _, p := range cc.pending {
		if p.body == nil {
			continue
		}
		ast.Inspect(p.body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SwitchBlock:
				for _, l := range labelsOf(x) {
					for _, cse := range l.Cases {
						if _, isPat := cse.(ast.Pattern); isPat {
							cc.errorf(l.Pos(), l.End(),
								"pattern switch is not supported by this encoder")
							return false
						}
					}
					if l.Guard != nil {
						cc.errorf(l.WhenPos, l.Guard.End(),
							"a guarded switch label is not supported by this encoder")
						return false
					}
				}
			case *ast.SwitchExpr:
				cc.errorf(x.Pos(), x.End(),
					"a switch expression is not supported by this encoder")
				return false
			}
			return true
		})
	}
}

func labelsOf(b *ast.SwitchBlock) []*ast.SwitchLabel {
	out := append([]*ast.SwitchLabel(nil), b.Labels...)
	for _, r := range b.Rules {
		out = append(out, r.Label)
	}
	for _, g := range b.Groups {
		out = append(out, g.Labels...)
	}
	return out
}