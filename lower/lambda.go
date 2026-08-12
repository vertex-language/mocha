package lower

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// §15.27 lambdas, without invokedynamic.
//
// LambdaMetafactory needs a BootstrapMethods attribute, which needs class file
// 51, which needs a StackMapTable. So a lambda becomes what an anonymous class
// becomes: a synthetic class implementing the functional interface, capturing
// by final field. One mechanism rather than two — and free for the Android
// path, since dex has no invoke-polymorphic below API 26 either, so neither
// target needs a desugaring step bolted on afterwards. That step is what d8
// exists to fold in.

type lambdaRec struct {
	expr    *ast.LambdaExpr
	ref     *ast.MethodRef
	binary  string          // Outer$$Lambda$1
	iface   *sym.ClassSym   // the functional interface
	sam     *sym.MethodSym  // its single abstract method
	hoisted string          // lambda$m$0, on the enclosing class
	hoisDsc string
	captures []*sym.VarSym  // what the body reads from the enclosing method
	inStatic bool
	owner    *sym.MethodSym
}

// declareLambdas hoists every lambda body in this class into a synthetic
// method and records the class that will implement the interface.
func (cc *classCtx) declareLambdas() {
	for _, p := range cc.pending {
		if p.body == nil {
			continue
		}
		m := p.m
		ast.Inspect(p.body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.LambdaExpr:
				cc.addLambda(m, x, nil)
			case *ast.MethodRef:
				cc.addLambda(m, nil, x)
			}
			return true
		})
	}
}

func (cc *classCtx) addLambda(owner *sym.MethodSym, x *ast.LambdaExpr, ref *ast.MethodRef) {
	var node ast.Node = x
	if x == nil {
		node = ref
	}
	ft := cc.in.Type(node)
	ct, ok := ft.(*types.ClassType)
	if !ok || ct.Sym == nil {
		cc.errorf(node.Pos(), node.End(), "lambda has no functional interface type")
		return
	}
	sam := samOf(ct.Sym)
	if sam == nil {
		cc.errorf(node.Pos(), node.End(),
			"%s declares no single abstract method", sym.Dotted(ct.Sym.Binary))
		return
	}

	n := len(cc.lambdas) + 1
	rec := &lambdaRec{
		expr:     x,
		ref:      ref,
		binary:   sprintf("%s$$Lambda$%d", cc.sym.Binary, n),
		iface:    ct.Sym,
		sam:      sam,
		owner:    owner,
		inStatic: owner == nil || owner.Flags.Has(sym.FlagStatic),
	}
	if x != nil {
		rec.hoisted = sprintf("lambda$%s$%d", methodTag(owner), n-1)
		rec.hoisDsc = cc.hoistedDesc(rec, x)
		rec.captures = cc.lambdaCaptures(owner)
		cc.declareHoisted(rec, x)
	}
	cc.lambdas = append(cc.lambdas, rec)
}

func methodTag(m *sym.MethodSym) string {
	if m == nil {
		return "static"
	}
	if m.IsConstructor() {
		return "new"
	}
	return m.Name
}

// declareHoisted adds the private synthetic method holding the body. The body
// is emitted exactly as a method body: it is one, by the time it runs.
func (cc *classCtx) declareHoisted(rec *lambdaRec, x *ast.LambdaExpr) {
	flags := classfile.AccPrivate | classfile.AccSynthetic
	if rec.inStatic {
		flags |= classfile.AccStatic
	}
	mb := cc.b.Method(flags, rec.hoisted, rec.hoisDsc)

	s := newSlotMap()
	if !rec.inStatic {
		s.reserve(1)
	}
	for _, v := range rec.captures {
		s.declare(v, cc.tt.FieldType(v))
	}
	for _, p := range x.Params {
		if v := cc.paramSym(p); v != nil {
			s.declare(v, cc.tt.FieldType(v))
		}
	}

	m := &sym.MethodSym{}
	m.Name = rec.hoisted
	m.Kind = sym.KindMethod
	m.Flags = sym.FlagPrivate | sym.FlagSynthetic
	if rec.inStatic {
		m.Flags |= sym.FlagStatic
	}
	m.Class = cc.sym
	m.Descriptor = rec.hoisDsc
	cc.slots[m] = s

	body, _ := x.Body.(*ast.Block)
	cc.pending = append(cc.pending, &pendingBody{
		mb: mb, m: m, body: body, lambdaExpr: x,
	})
}

// hoistedDesc: the captured variables first, then the lambda's own parameters,
// and the SAM's erased return.
func (cc *classCtx) hoistedDesc(rec *lambdaRec, x *ast.LambdaExpr) string {
	mt := cc.tt.MethodType(rec.sam)
	params := make([]types.Type, 0, len(x.Params)+4)
	for _, v := range cc.lambdaCaptures(rec.owner) {
		params = append(params, cc.tt.FieldType(v))
	}
	for i := range x.Params {
		if i < len(mt.Params) {
			params = append(params, types.Erase(mt.Params[i]))
		}
	}
	return types.MethodDescriptor(&types.MethodType{
		Params: params, Result: types.Erase(mt.Result),
	})
}

func (cc *classCtx) lambdaCaptures(owner *sym.MethodSym) []*sym.VarSym {
	if cc.fl == nil || owner == nil {
		return nil
	}
	return cc.fl.Captured[owner]
}

func (cc *classCtx) paramSym(p *ast.Param) *sym.VarSym {
	if s, ok := cc.in.Use(p.Name).(*sym.VarSym); ok {
		return s
	}
	return nil
}

// buildLambdaClasses emits the synthetic implementors. Each is an independent
// class — Outer$$Lambda$1 is a sibling of Outer, not a child — with one final
// field per capture, a constructor that fills them, and the SAM forwarding to
// the hoisted body.
func (cc *classCtx) buildLambdaClasses() {
	for _, rec := range cc.lambdas {
		b := classfile.NewBuilder(rec.binary)
		b.SetFlags(classfile.AccSuper | classfile.AccFinal | classfile.AccSynthetic)
		b.SetSuper(sym.ObjectName)
		b.AddInterface(rec.iface.Binary)

		// A lambda in an instance context captures the enclosing instance too.
		var fields []struct{ name, desc string }
		if !rec.inStatic {
			fields = append(fields, struct{ name, desc string }{
				"this$0", "L" + cc.sym.Binary + ";"})
		}
		for i, v := range rec.captures {
			fields = append(fields, struct{ name, desc string }{
				sprintf("arg$%d", i+1), types.Descriptor(cc.tt.FieldType(v)).String()})
		}
		for _, f := range fields {
			b.Field(classfile.AccPrivate|classfile.AccFinal|classfile.AccSynthetic,
				f.name, f.desc)
		}

		ctorDesc := "("
		for _, f := range fields {
			ctorDesc += f.desc
		}
		ctorDesc += ")V"

		b.Method(classfile.AccPrivate, sym.InitName, ctorDesc).
			Code(func(w *classfile.CodeWriter) {
				w.Aload(0)
				w.InvokeSpecial(sym.ObjectName, sym.InitName, "()V")
				slot := 1
				for _, f := range fields {
					w.Aload(0)
					loadRaw(w, slot, f.desc)
					w.PutField(rec.binary, f.name, f.desc)
					slot += slotsOf(f.desc)
				}
				w.Return()
			})

		samDesc := types.MethodDescriptor(types.EraseMethod(cc.tt.MethodType(rec.sam)))
		owner, binary := cc.sym.Binary, rec.binary
		b.Method(classfile.AccPublic, rec.sam.Name, samDesc).
			Code(func(w *classfile.CodeWriter) {
				for _, f := range fields {
					w.Aload(0)
					w.GetField(binary, f.name, f.desc)
				}
				sig := mustParse(samDesc)
				slot := 1
				for _, p := range sig.Params {
					loadDesc(w, slot, p)
					slot += p.Slots()
				}
				if rec.inStatic {
					w.InvokeStatic(owner, rec.hoisted, rec.hoisDsc)
				} else {
					// The first field loaded was this$0, so the receiver is
					// already beneath the arguments.
					w.InvokeSpecial(owner, rec.hoisted, rec.hoisDsc)
				}
				w.Return()
			})

		cc.out = append(cc.out, b)
	}
}

// samOf finds the single abstract method a functional interface declares.
// Object's public methods do not count (§9.8), which is why equals and
// toString on a Comparator do not make it non-functional.
func samOf(c *sym.ClassSym) *sym.MethodSym {
	if err := c.Complete(); err != nil {
		return nil
	}
	var found *sym.MethodSym
	c.Members.Each(func(s sym.Symbol) bool {
		m, ok := s.(*sym.MethodSym)
		if !ok || !m.Flags.Has(sym.FlagAbstract) || isObjectMethod(m) {
			return true
		}
		if found != nil {
			found = nil
			return false
		}
		found = m
		return true
	})
	return found
}

func isObjectMethod(m *sym.MethodSym) bool {
	switch m.Name {
	case "equals", "hashCode", "toString":
		return true
	}
	return false
}