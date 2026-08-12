package lower

import (
	"sort"

	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// §8.4.8.3 bridges, and the synthetic accessors 49.0 forces.

type bridgeRec struct {
	name       string
	from, to   string // the erased descriptor the caller invokes, and the real one
	target     *sym.MethodSym
	iface      bool
}

type accessorRec struct {
	name   string // access$000
	target sym.Symbol
	kind   accessorKind
	owner  string
	desc   string
}

type accessorKind uint8

const (
	accGet accessorKind = iota
	accSet
	accInvoke
)

// declareBridges adds one bridge per override whose erased signature differs
// from the supertype method the caller will invoke. Covariant returns and
// generic overrides both produce one: the JVM dispatches on the erased
// descriptor, so without a bridge the override is a different method.
func (cc *classCtx) declareBridges() {
	if cc.sym.IsInterface() {
		return // 49.0 admits no default method, so an interface bridges nothing
	}
	seen := map[string]bool{}

	for _, sup := range cc.tt.Supers(cc.sym) {
		if sup.Sym == nil || sup.Sym == cc.sym {
			continue
		}
		if err := sup.Sym.Complete(); err != nil {
			continue
		}
		sup.Sym.Members.Each(func(s sym.Symbol) bool {
			sm, ok := s.(*sym.MethodSym)
			if !ok || sm.IsConstructor() || sm.Flags.Has(sym.FlagStatic|sym.FlagPrivate) {
				return true
			}
			ours := cc.overrideOf(sm)
			if ours == nil {
				return true
			}
			supDesc := types.MethodDescriptor(types.EraseMethod(cc.tt.MethodType(sm)))
			ourDesc := cc.methodDesc(ours)
			if supDesc == ourDesc || seen[sm.Name+supDesc] {
				return true
			}
			seen[sm.Name+supDesc] = true
			cc.bridges = append(cc.bridges, bridgeRec{
				name:   sm.Name,
				from:   supDesc,
				to:     ourDesc,
				target: ours,
				iface:  sup.Sym.IsInterface(),
			})
			return true
		})
	}

	for _, br := range cc.bridges {
		flags := classfile.AccPublic | classfile.AccBridge | classfile.AccSynthetic
		mb := cc.b.Method(flags, br.name, br.from)
		cc.pending = append(cc.pending, &pendingBody{mb: mb, bridge: &br})
	}
}

// overrideOf finds this class's method that overrides sm. Erased signatures
// are the matching key throughout — two methods override each other exactly
// when the JVM would consider one to replace the other — which is the same key
// warn.override and attr.checkOverloads use.
func (cc *classCtx) overrideOf(sm *sym.MethodSym) *sym.MethodSym {
	for _, m := range cc.sym.Methods(sm.Name) {
		if m.Flags.Has(sym.FlagStatic) {
			continue
		}
		a := cc.tt.MethodType(m)
		b := cc.tt.MethodType(sm)
		if len(a.Params) != len(b.Params) {
			continue
		}
		same := true
		for i := range a.Params {
			if !types.Identical(types.Erase(a.Params[i]), types.Erase(b.Params[i])) {
				same = false
				break
			}
		}
		if same {
			return m
		}
	}
	return nil
}

// emitBridge is the whole body: load the receiver and every argument, cast
// each to what the real method takes, invoke it, and return. The casts are the
// point — the erased supertype signature says Object where the override says
// String, and checkcast is what makes the dispatch type-safe.
func (e *emitter) emitBridge(br *bridgeRec) {
	from := mustParse(br.from)
	to := mustParse(br.to)

	e.c.Aload(0)
	slot := 1
	for i, p := range from.Params {
		e.loadSlot(slot, p)
		slot += p.Slots()
		if i < len(to.Params) && p.String() != to.Params[i].String() {
			if to.Params[i].IsRef() {
				e.c.CheckCast(castName(to.Params[i]))
			}
		}
	}
	e.c.InvokeVirtual(e.sym.Binary, br.name, br.to)
	e.c.Return()
}

// scanAccessors walks bodies looking for cross-class private access.
//
// You learn that access$000 is required while emitting a body that reads a
// private member of an enclosing class — by which time pass one is over and
// the method must already exist. javac hits this exactly and says so: some
// checks during lowering require that all synthetic members have already been
// added to the class and its supertypes. So the scan happens here, the way
// flow already walks bodies for captures.
//
// There is no NestHost at 49.0, so this is not optional.
func (cc *classCtx) scanAccessors() {
	if cc.sym.Outer == nil && !cc.hasNested() {
		return
	}
	for _, p := range cc.pending {
		if p.body == nil {
			continue
		}
		ast.Inspect(p.body, func(n ast.Node) bool {
			s := cc.in.Use(n)
			if s == nil {
				return true
			}
			base := s.Base()
			if !base.Flags.Has(sym.FlagPrivate) {
				return true
			}
			owner := ownerClass(s)
			if owner == nil || owner == cc.sym {
				return true
			}
			if !nestmates(owner, cc.sym) {
				return true // not our business; attr already checked access
			}
			cc.needAccessor(owner, s, n)
			return true
		})
	}
	cc.declareAccessors()
}

func (cc *classCtx) needAccessor(owner *sym.ClassSym, s sym.Symbol, use ast.Node) {
	if cc.accessors == nil {
		cc.accessors = map[string]*accessorRec{}
	}
	key := owner.Binary + "." + s.Base().Name
	if _, ok := cc.accessors[key]; ok {
		return
	}

	rec := &accessorRec{owner: owner.Binary, target: s}
	switch t := s.(type) {
	case *sym.MethodSym:
		rec.kind = accInvoke
		rec.desc = cc.methodDesc(t)
	case *sym.VarSym:
		rec.kind = accGet
		rec.desc = types.Descriptor(cc.tt.FieldType(t)).String()
	default:
		return
	}
	cc.accessors[key] = rec
}

// declareAccessors names and adds them. The number is javac's three-digit
// counter, assigned in sorted key order so the artifact is reproducible.
func (cc *classCtx) declareAccessors() {
	keys := make([]string, 0, len(cc.accessors))
	for k := range cc.accessors {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for i, k := range keys {
		rec := cc.accessors[k]
		rec.name = sprintf("access$%03d", (i+1)*100)

		var desc string
		switch rec.kind {
		case accGet:
			v := rec.target.(*sym.VarSym)
			if v.Flags.Has(sym.FlagStatic) {
				desc = "()" + rec.desc
			} else {
				desc = "(L" + rec.owner + ";)" + rec.desc
			}
		case accInvoke:
			m := rec.target.(*sym.MethodSym)
			if m.Flags.Has(sym.FlagStatic) {
				desc = rec.desc
			} else {
				desc = prependParam(rec.desc, "L"+rec.owner+";")
			}
		}
		mb := cc.b.Method(classfile.AccStatic|classfile.AccSynthetic, rec.name, desc)
		cc.pending = append(cc.pending, &pendingBody{mb: mb, accessor: rec, accDesc: desc})
	}
}

func (e *emitter) emitAccessor(rec *accessorRec, descriptor string) {
	sig := mustParse(descriptor)
	slot := 0
	for _, p := range sig.Params {
		e.loadSlot(slot, p)
		slot += p.Slots()
	}
	switch rec.kind {
	case accGet:
		v := rec.target.(*sym.VarSym)
		if v.Flags.Has(sym.FlagStatic) {
			e.c.GetStatic(rec.owner, v.Name, rec.desc)
		} else {
			e.c.GetField(rec.owner, v.Name, rec.desc)
		}
	case accInvoke:
		m := rec.target.(*sym.MethodSym)
		if m.Flags.Has(sym.FlagStatic) {
			e.c.InvokeStatic(rec.owner, m.Name, rec.desc)
		} else {
			// invokespecial, not invokevirtual: a private method is not
			// virtually dispatched, and the accessor must not become one.
			e.c.InvokeSpecial(rec.owner, m.Name, rec.desc)
		}
	}
	e.c.Return()
}

func ownerClass(s sym.Symbol) *sym.ClassSym {
	switch t := s.(type) {
	case *sym.MethodSym:
		return t.Class
	case *sym.VarSym:
		return t.Class
	}
	return nil
}

// nestmates reports whether two classes share a top-level ancestor, which is
// the access relation §6.6.1 grants and the class file cannot express at 49.0.
func nestmates(a, b *sym.ClassSym) bool { return topLevel(a) == topLevel(b) }

func topLevel(c *sym.ClassSym) *sym.ClassSym {
	for c != nil && c.Outer != nil {
		c = c.Outer
	}
	return c
}

func (cc *classCtx) hasNested() bool {
	found := false
	cc.sym.Members.Each(func(s sym.Symbol) bool {
		if _, ok := s.(*sym.ClassSym); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

func prependParam(descriptor, param string) string {
	return "(" + param + descriptor[1:]
}