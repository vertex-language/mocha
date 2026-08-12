package lower

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/jvm/desc"
	"github.com/vertex-language/mocha/jvm/op"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// Pass two — emit. The closure. Post-order, one CodeWriter call per operation,
// nothing retained.

type emitter struct {
	*classCtx
	c     *classfile.CodeWriter
	m     *sym.MethodSym
	slots *slotMap

	lastLine int
	alive    bool // our mirror of the writer's reachability

	frames   []frame
	finallys []finallyRec
	retSlot  int // scratch for a return value crossing a finally, or -1
}

type frame struct {
	label      string
	brk, cont  *classfile.Label
	hasCont    bool
	finallyTop int
}

type finallyRec struct {
	block *ast.Block
	slots int // the slot mark to restore
}

func (cc *classCtx) emit() {
	cc.declareLambdas()

	for _, p := range cc.pending {
		p := p
		p.mb.Code(func(w *classfile.CodeWriter) {
			e := &emitter{
				classCtx: cc, c: w, m: p.m, slots: cc.slots[p.m],
				lastLine: -1, alive: true, retSlot: -1,
			}
			if e.slots == nil {
				e.slots = newSlotMap()
			}
			e.emitBody(p)
		})
	}
	cc.buildLambdaClasses()
}

func (e *emitter) emitBody(p *pendingBody) {
	switch {
	case p.bridge != nil:
		e.emitBridge(p.bridge)
		return
	case p.accessor != nil:
		e.emitAccessor(p.accessor, p.accDesc)
		return
	case p.clinit != nil:
		e.emitInits(p.clinit.inits, true)
		e.c.Return()
		return
	case p.ctor != nil:
		e.emitCtor(p)
		return
	}

	if p.lambdaExpr != nil && p.body == nil {
		// A concise lambda body is an expression, not a block.
		x, _ := p.lambdaExpr.Body.(ast.Expr)
		e.emitLambdaValue(x)
		return
	}
	e.block(p.body)
	if e.alive {
		e.c.Return() // §14.17: a void method's implicit return
	}
}

func (e *emitter) emitLambdaValue(x ast.Expr) {
	if x == nil {
		e.c.Return()
		return
	}
	t := e.in.Type(x)
	e.expr(x)
	if t.Kind() == types.KindVoid {
		e.c.Return()
		return
	}
	e.c.Return()
}

// emitCtor: captures, then the explicit or implicit super call, then the
// initialisers, then the body. §8.8.7 fixes that order and nothing may move.
func (e *emitter) emitCtor(p *pendingBody) {
	e.storeCaptures()

	switch {
	case p.ctor.explicit != nil:
		e.constructorCall(p.ctor.explicit)
	default:
		// §8.8.7: an implicit super() with no arguments.
		e.c.Aload(0)
		e.c.InvokeSpecial(e.superName(), sym.InitName, "()V")
	}

	e.emitInits(p.ctor.inits, false)

	if p.body != nil {
		for _, s := range p.body.Stmts[p.ctor.bodyFrom:] {
			e.stmt(s)
		}
	}
	if e.alive {
		e.c.Return()
	}
}

func (e *emitter) emitInits(inits []initEntry, static bool) {
	for _, in := range inits {
		e.line(in.pos)
		switch {
		case in.block != nil:
			e.block(in.block)
		case in.field != nil:
			t := e.tt.FieldType(in.field)
			d := types.Descriptor(t).String()
			if !static {
				e.c.Aload(0)
			}
			e.initValue(in.init, t)
			if static {
				e.c.PutStatic(e.sym.Binary, in.field.Name, d)
			} else {
				e.c.PutField(e.sym.Binary, in.field.Name, d)
			}
		}
		if !e.alive {
			return
		}
	}
}

func (e *emitter) initValue(n ast.Node, want types.Type) {
	switch x := n.(type) {
	case ast.Expr:
		e.exprAs(x, want)
	case *ast.ArrayInit:
		e.arrayInit(x, want)
	default:
		bug("initialiser is neither an expression nor an array initialiser")
	}
}

func (e *emitter) superName() string {
	if e.sym.Super != "" {
		return e.sym.Super
	}
	return sym.ObjectName
}

// ---------------------------------------------------------------- statements

func (e *emitter) block(b *ast.Block) {
	if b == nil {
		return
	}
	mark := e.slots.mark()
	for _, s := range b.Stmts {
		e.stmt(s)
	}
	e.slots.release(mark) // slots are reused across disjoint scopes
}

func (e *emitter) stmt(s ast.Stmt) {
	if s == nil || !e.alive {
		return
	}
	// A statement flow marked unreachable emits nothing. §14.22 makes it an
	// error and flow already reported it; emitting would only produce code the
	// writer rejects.
	if e.fl != nil && e.fl.Unreachable[s] {
		return
	}
	e.line(s.Pos())

	switch n := s.(type) {
	case *ast.Block:
		e.block(n)
	case *ast.EmptyStmt:
	case *ast.ExprStmt:
		e.exprStmt(n.X)
	case *ast.DeclStmt:
		e.declStmt(n)
	case *ast.IfStmt:
		e.ifStmt(n)
	case *ast.WhileStmt:
		e.whileStmt(n, "")
	case *ast.DoStmt:
		e.doStmt(n, "")
	case *ast.ForStmt:
		e.forStmt(n, "")
	case *ast.RangeStmt:
		e.rangeStmt(n, "")
	case *ast.ReturnStmt:
		e.returnStmt(n)
	case *ast.BreakStmt:
		e.jump(labelName(n.Label, e.src), false)
	case *ast.ContinueStmt:
		e.jump(labelName(n.Label, e.src), true)
	case *ast.LabeledStmt:
		e.labeled(n)
	case *ast.ThrowStmt:
		e.expr(n.X)
		e.c.Throw()
		e.alive = false
	case *ast.SyncStmt:
		e.syncStmt(n)
	case *ast.TryStmt:
		e.tryStmt(n)
	case *ast.SwitchStmt:
		e.switchStmt(n, "")
	case *ast.AssertStmt:
		e.assertStmt(n)
	case *ast.ConstructorCall:
		e.constructorCall(n)
	case *ast.YieldStmt:
		bug("yield outside a switch expression")
	case *ast.BadStmt:
		bug("BadStmt reached pass two")
	default:
		bug("unhandled statement %T", s)
	}
}

// A statement's value is discarded. The same node emits differently in
// expression and statement position — i++ as a statement leaves nothing on the
// stack, as an expression leaves the old value. ECJ restarts code generation
// when it gets this wrong; the tree shape tells us up front, so we do not.
func (e *emitter) exprStmt(x ast.Expr) {
	switch n := x.(type) {
	case *ast.AssignExpr:
		e.assign(n, false)
		return
	case *ast.UnaryExpr:
		if n.Op == token.INC || n.Op == token.DEC {
			e.incdec(n.X, n.Op, false, false)
			return
		}
	case *ast.PostfixExpr:
		e.incdec(n.X, n.Op, false, false)
		return
	}
	t := e.in.Type(x)
	e.expr(x)
	e.pop(t)
}

func (e *emitter) declStmt(n *ast.DeclStmt) {
	switch d := n.Decl.(type) {
	case *ast.VarDecl:
		for _, decl := range d.Names {
			v, _ := e.in.Use(decl.Name).(*sym.VarSym)
			if v == nil {
				continue
			}
			t := e.tt.FieldType(v)
			slot := e.slots.declare(v, t)
			if decl.Init == nil {
				continue
			}
			e.initValue(decl.Init, t)
			e.storeLocal(slot, t)
		}
	default:
		// A local class was entered by attr and is emitted as its own class.
		// Nothing runs here.
	}
}

func (e *emitter) ifStmt(n *ast.IfStmt) {
	els, end := e.c.NewLabel(), e.c.NewLabel()

	if n.Else == nil {
		e.cond(n.Cond, els, false)
		e.stmt(n.Then)
		e.mark(els)
		return
	}
	e.cond(n.Cond, els, false)
	e.stmt(n.Then)
	thenAlive := e.alive
	if e.alive {
		e.c.Goto(end)
		e.alive = false
	}
	e.mark(els)
	e.stmt(n.Else)
	if e.alive || thenAlive {
		e.mark(end)
	}
}

func (e *emitter) whileStmt(n *ast.WhileStmt, label string) {
	top, cont, brk := e.c.NewLabel(), e.c.NewLabel(), e.c.NewLabel()
	e.c.Goto(cont)
	e.alive = false

	e.mark(top)
	e.push(frame{label: label, brk: brk, cont: cont, hasCont: true})
	e.stmt(n.Body)
	e.popFrame()

	e.mark(cont)
	e.cond(n.Cond, top, true)
	e.mark(brk)
}

func (e *emitter) doStmt(n *ast.DoStmt, label string) {
	top, cont, brk := e.c.NewLabel(), e.c.NewLabel(), e.c.NewLabel()
	e.mark(top)
	e.push(frame{label: label, brk: brk, cont: cont, hasCont: true})
	e.stmt(n.Body)
	e.popFrame()
	e.mark(cont)
	e.cond(n.Cond, top, true)
	e.mark(brk)
}

func (e *emitter) forStmt(n *ast.ForStmt, label string) {
	mark := e.slots.mark()
	for _, s := range n.Init {
		e.stmt(s)
	}
	top, cont, brk := e.c.NewLabel(), e.c.NewLabel(), e.c.NewLabel()

	e.mark(top)
	if n.Cond != nil {
		e.cond(n.Cond, brk, false)
	}
	e.push(frame{label: label, brk: brk, cont: cont, hasCont: true})
	e.stmt(n.Body)
	e.popFrame()

	e.mark(cont)
	for _, x := range n.Post {
		e.exprStmt(x)
	}
	if e.alive {
		e.c.Goto(top)
		e.alive = false
	}
	e.mark(brk)
	e.slots.release(mark)
}

// An enhanced for becomes an Iterator loop, or an indexed loop over an array.
// It does not *become* a tree something can look at; it emits the opcodes the
// loop would have emitted.
func (e *emitter) rangeStmt(n *ast.RangeStmt, label string) {
	xt := e.in.Type(n.X)
	if xt.Kind() == types.KindArray {
		e.rangeArray(n, label, xt.(*types.ArrayType))
		return
	}
	e.rangeIterator(n, label)
}

func (e *emitter) rangeArray(n *ast.RangeStmt, label string, at *types.ArrayType) {
	mark := e.slots.mark()
	arr := e.slots.reserve(1)
	idx := e.slots.reserve(1)
	lim := e.slots.reserve(1)

	e.expr(n.X)
	e.c.Astore(arr)
	e.c.Aload(arr)
	e.c.Op(op.Arraylength)
	e.c.Istore(lim)
	e.c.Iconst(0)
	e.c.Istore(idx)

	top, cont, brk := e.c.NewLabel(), e.c.NewLabel(), e.c.NewLabel()
	e.mark(top)
	e.c.Iload(idx)
	e.c.Iload(lim)
	e.c.IfICmpGe(brk)

	v := e.rangeVar(n)
	vt := e.tt.FieldType(v)
	slot := e.slots.declare(v, vt)
	e.c.Aload(arr)
	e.c.Iload(idx)
	e.c.Op(arrayLoadOp(at.Elem))
	e.convert(at.Elem, vt)
	e.storeLocal(slot, vt)

	e.push(frame{label: label, brk: brk, cont: cont, hasCont: true})
	e.stmt(n.Body)
	e.popFrame()

	e.mark(cont)
	e.c.Iinc(idx, 1)
	if e.alive {
		e.c.Goto(top)
		e.alive = false
	}
	e.mark(brk)
	e.slots.release(mark)
}

func (e *emitter) rangeIterator(n *ast.RangeStmt, label string) {
	mark := e.slots.mark()
	it := e.slots.reserve(1)

	e.expr(n.X)
	xt := e.in.Type(n.X)
	if ct, ok := xt.(*types.ClassType); ok && ct.Sym != nil && ct.Sym.IsInterface() {
		e.c.InvokeInterface(ct.Binary(), "iterator", "()Ljava/util/Iterator;")
	} else {
		e.c.InvokeVirtual(castTarget(xt), "iterator", "()Ljava/util/Iterator;")
	}
	e.c.Astore(it)

	top, cont, brk := e.c.NewLabel(), e.c.NewLabel(), e.c.NewLabel()
	e.mark(top)
	e.c.Aload(it)
	e.c.InvokeInterface("java/util/Iterator", "hasNext", "()Z")
	e.c.IfEq(brk)

	v := e.rangeVar(n)
	vt := e.tt.FieldType(v)
	slot := e.slots.declare(v, vt)
	e.c.Aload(it)
	e.c.InvokeInterface("java/util/Iterator", "next", "()Ljava/lang/Object;")
	// next() is erased to Object, so the cast is what recovers the element
	// type — the same cast a generic call site always needs.
	e.convert(e.tt.Object(), vt)
	e.storeLocal(slot, vt)

	e.push(frame{label: label, brk: brk, cont: cont, hasCont: true})
	e.stmt(n.Body)
	e.popFrame()

	e.mark(cont)
	if e.alive {
		e.c.Goto(top)
		e.alive = false
	}
	e.mark(brk)
	e.slots.release(mark)
}

func (e *emitter) rangeVar(n *ast.RangeStmt) *sym.VarSym {
	if n.Decl == nil || len(n.Decl.Names) != 1 {
		bug("enhanced for declares %d variables", len(n.Decl.Names))
	}
	v, _ := e.in.Use(n.Decl.Names[0].Name).(*sym.VarSym)
	if v == nil {
		bug("enhanced for variable did not resolve")
	}
	return v
}

func (e *emitter) labeled(n *ast.LabeledStmt) {
	name := labelName(n.Label, e.src)
	switch inner := n.Stmt.(type) {
	case *ast.WhileStmt:
		e.whileStmt(inner, name)
	case *ast.DoStmt:
		e.doStmt(inner, name)
	case *ast.ForStmt:
		e.forStmt(inner, name)
	case *ast.RangeStmt:
		e.rangeStmt(inner, name)
	case *ast.SwitchStmt:
		e.switchStmt(inner, name)
	default:
		// §14.7 lets any statement carry a label; break targets its end.
		brk := e.c.NewLabel()
		e.push(frame{label: name, brk: brk})
		e.stmt(n.Stmt)
		e.popFrame()
		e.mark(brk)
	}
}

func (e *emitter) returnStmt(n *ast.ReturnStmt) {
	if n.Result == nil {
		e.runFinallys(0)
		e.c.Return()
		e.alive = false
		return
	}
	rt := e.returnType()
	e.exprAs(n.Result, rt)

	// finally is duplicated onto every path out of the block, including a
	// return crossing it — so the value is parked in a slot while it runs.
	if len(e.finallys) > 0 {
		slot := e.slots.reserve(types.Slots(rt))
		e.storeLocal(slot, rt)
		e.runFinallys(0)
		e.loadLocal(slot, rt)
	}
	e.c.Return()
	e.alive = false
}

func (e *emitter) returnType() types.Type {
	if e.m == nil {
		return types.Void
	}
	return e.tt.MethodType(e.m).Result
}

func (e *emitter) jump(label string, cont bool) {
	for i := len(e.frames) - 1; i >= 0; i-- {
		f := e.frames[i]
		if label != "" && f.label != label {
			continue
		}
		if cont && !f.hasCont {
			continue
		}
		e.runFinallys(f.finallyTop)
		if cont {
			e.c.Goto(f.cont)
		} else {
			e.c.Goto(f.brk)
		}
		e.alive = false
		return
	}
	bug("break or continue with no enclosing target")
}

func (e *emitter) syncStmt(n *ast.SyncStmt) {
	mark := e.slots.mark()
	mon := e.slots.reserve(1)

	e.expr(n.X)
	e.c.Op(op.Dup)
	e.c.Astore(mon)
	e.c.Op(op.Monitorenter)

	start, end, handler, done := e.c.NewLabel(), e.c.NewLabel(), e.c.NewLabel(), e.c.NewLabel()
	e.mark(start)
	e.block(n.Body)
	if e.alive {
		e.c.Aload(mon)
		e.c.Op(op.Monitorexit)
		e.c.Goto(done)
		e.alive = false
	}
	e.markAt(end)

	// §14.19: the monitor is released on the exceptional path too.
	e.c.TryCatch(start, end, handler, "")
	e.markHandler(handler)
	e.c.Aload(mon)
	e.c.Op(op.Monitorexit)
	e.c.Throw()
	e.alive = false

	e.mark(done)
	e.slots.release(mark)
}

// try bodies produce exception table entries from the PCs the walk passes
// through, which is why control flow stays structured rather than flattened:
// the lexical extent is the range.
func (e *emitter) tryStmt(n *ast.TryStmt) {
	if len(n.Resources) > 0 {
		e.tryWithResources(n)
		return
	}

	start, end, done := e.c.NewLabel(), e.c.NewLabel(), e.c.NewLabel()
	hasFinally := n.Finally != nil
	if hasFinally {
		e.finallys = append(e.finallys, finallyRec{block: n.Finally, slots: e.slots.mark()})
	}

	e.mark(start)
	e.block(n.Body)
	bodyAlive := e.alive
	if e.alive {
		if hasFinally {
			e.inlineFinally(n.Finally)
		}
		e.c.Goto(done)
		e.alive = false
	}
	e.markAt(end)

	anyAlive := bodyAlive
	for _, cl := range n.Catches {
		h := e.c.NewLabel()
		for _, ct := range cl.Types {
			e.c.TryCatch(start, end, h, castTarget(e.in.Type(ct)))
		}
		e.markHandler(h)

		mark := e.slots.mark()
		if v, _ := e.in.Use(cl.Name).(*sym.VarSym); v != nil {
			slot := e.slots.declare(v, e.tt.FieldType(v))
			e.c.Astore(slot)
		} else {
			e.c.Op(op.Pop)
		}
		e.block(cl.Body)
		if e.alive {
			anyAlive = true
			if hasFinally {
				e.inlineFinally(n.Finally)
			}
			e.c.Goto(done)
			e.alive = false
		}
		e.slots.release(mark)
	}

	if hasFinally {
		e.finallys = e.finallys[:len(e.finallys)-1]

		// The catch-all handler: run the finally, rethrow. catchType "" is what
		// makes this a finally rather than a catch of Throwable.
		h := e.c.NewLabel()
		e.c.TryCatch(start, end, h, "")
		e.markHandler(h)
		mark := e.slots.mark()
		exc := e.slots.reserve(1)
		e.c.Astore(exc)
		e.inlineFinally(n.Finally)
		e.c.Aload(exc)
		e.c.Throw()
		e.alive = false
		e.slots.release(mark)
	}

	if anyAlive {
		e.mark(done)
	}
}

// inlineFinally emits one copy of a finally block. Duplication rather than jsr:
// jsr and ret are deprecated from 51 and were never worth the subroutine
// merging they force on a verifier.
func (e *emitter) inlineFinally(b *ast.Block) {
	saved := e.finallys
	e.finallys = nil // a finally does not re-enter itself
	e.block(b)
	e.finallys = saved
}

func (e *emitter) runFinallys(down int) {
	for i := len(e.finallys) - 1; i >= down; i-- {
		e.inlineFinally(e.finallys[i].block)
	}
}

// try-with-resources, §14.20.3: try/finally with addSuppressed. The resources
// close in reverse order, each in its own nested try, which is what makes a
// throw from close() suppressed rather than masking the body's exception.
func (e *emitter) tryWithResources(n *ast.TryStmt) {
	rs := n.Resources
	inner := &ast.TryStmt{
		Span: n.Span, TryPos: n.TryPos, Body: n.Body,
		Catches: n.Catches, FinallyPos: n.FinallyPos, Finally: n.Finally,
	}
	e.resource(rs, 0, inner)
}

func (e *emitter) resource(rs []*ast.Resource, i int, inner *ast.TryStmt) {
	if i == len(rs) {
		e.tryStmt(inner)
		return
	}
	mark := e.slots.mark()
	r := rs[i]

	var res int
	var rt types.Type
	if r.Decl != nil {
		v, _ := e.in.Use(r.Decl.Names[0].Name).(*sym.VarSym)
		rt = e.tt.FieldType(v)
		res = e.slots.declare(v, rt)
		e.initValue(r.Decl.Names[0].Init, rt)
	} else {
		rt = e.in.Type(r.X)
		res = e.slots.reserve(1)
		e.expr(r.X)
	}
	e.c.Astore(res)

	primary := e.slots.reserve(1)
	e.c.AconstNull()
	e.c.Astore(primary)

	start, end, done := e.c.NewLabel(), e.c.NewLabel(), e.c.NewLabel()
	e.mark(start)
	e.resource(rs, i+1, inner)
	bodyAlive := e.alive
	if e.alive {
		e.closeResource(res, primary, rt)
		e.c.Goto(done)
		e.alive = false
	}
	e.markAt(end)

	h := e.c.NewLabel()
	e.c.TryCatch(start, end, h, "")
	e.markHandler(h)
	e.c.Astore(primary)
	e.closeResource(res, primary, rt)
	e.c.Aload(primary)
	e.c.Throw()
	e.alive = false

	if bodyAlive {
		e.mark(done)
	}
	e.slots.release(mark)
}

// closeResource is §14.20.3.2's close: skipped for a null resource, and a
// throw from it is suppressed onto the primary exception if there is one.
func (e *emitter) closeResource(res, primary int, rt types.Type) {
	skip := e.c.NewLabel()
	e.c.Aload(res)
	e.c.IfNull(skip)

	noPrimary, after := e.c.NewLabel(), e.c.NewLabel()
	e.c.Aload(primary)
	e.c.IfNull(noPrimary)

	// try { close(); } catch (Throwable t) { primary.addSuppressed(t); }
	s, en, h := e.c.NewLabel(), e.c.NewLabel(), e.c.NewLabel()
	e.mark(s)
	e.invokeClose(res, rt)
	e.c.Goto(after)
	e.alive = false
	e.markAt(en)
	e.c.TryCatch(s, en, h, "java/lang/Throwable")
	e.markHandler(h)
	e.c.Aload(primary)
	e.c.Op(op.Swap)
	e.c.InvokeVirtual("java/lang/Throwable", "addSuppressed", "(Ljava/lang/Throwable;)V")
	e.c.Goto(after)
	e.alive = false

	e.mark(noPrimary)
	e.invokeClose(res, rt)
	e.mark(after)
	e.mark(skip)
}

func (e *emitter) invokeClose(res int, rt types.Type) {
	e.c.Aload(res)
	if ct, ok := rt.(*types.ClassType); ok && ct.Sym != nil && ct.Sym.IsInterface() {
		e.c.InvokeInterface(ct.Binary(), "close", "()V")
		return
	}
	e.c.InvokeVirtual(castTarget(rt), "close", "()V")
}

// assert becomes a $assertionsDisabled guard and a throw. The guard is a static
// final boolean the class initialiser fills from desiredAssertionStatus, which
// is why this needs ldc of a class constant.
func (e *emitter) assertStmt(n *ast.AssertStmt) {
	e.needAssertions()
	skip := e.c.NewLabel()
	e.c.GetStatic(e.sym.Binary, assertField, "Z")
	e.c.IfNe(skip)
	e.cond(n.X, skip, true)

	e.c.New("java/lang/AssertionError")
	e.c.Op(op.Dup)
	if n.Msg == nil {
		e.c.InvokeSpecial("java/lang/AssertionError", sym.InitName, "()V")
	} else {
		mt := e.in.Type(n.Msg)
		e.expr(n.Msg)
		e.c.InvokeSpecial("java/lang/AssertionError", sym.InitName,
			"("+assertArg(mt)+")V")
	}
	e.c.Throw()
	e.alive = false
	e.mark(skip)
}

func assertArg(t types.Type) string {
	switch t.Kind() {
	case types.KindBoolean:
		return "Z"
	case types.KindChar:
		return "C"
	case types.KindByte, types.KindShort, types.KindInt:
		return "I"
	case types.KindLong:
		return "J"
	case types.KindFloat:
		return "F"
	case types.KindDouble:
		return "D"
	}
	return "Ljava/lang/Object;"
}

func (e *emitter) constructorCall(n *ast.ConstructorCall) {
	e.c.Aload(0)

	m, _ := e.in.Use(n).(*sym.MethodSym)
	owner := e.superName()
	if n.Kind == token.THIS {
		owner = e.sym.Binary
	}

	d := "()V"
	if m != nil {
		d = e.methodDesc(m)
		if n.Kind == token.THIS {
			// A this(...) call must pass the enclosing instance and the
			// captures on, since the target constructor expects them.
			d = e.ctorDesc(m)
			if e.thisField != "" {
				e.c.Aload(1)
			}
		}
		e.args(n.Args, e.tt.MethodType(m), m.Flags.Has(sym.FlagVarargs))
		if n.Kind == token.THIS {
			for _, cap := range e.captures {
				e.loadLocal(cap.slot, e.tt.FieldType(cap.local))
			}
		}
	}
	e.c.InvokeSpecial(owner, sym.InitName, d)
}

// ---------------------------------------------------------------- helpers

func (e *emitter) push(f frame) {
	f.finallyTop = len(e.finallys)
	e.frames = append(e.frames, f)
}

func (e *emitter) popFrame() { e.frames = e.frames[:len(e.frames)-1] }

// mark places a label and revives the walk: a label is only marked where a
// branch targets it, so control can arrive even after a terminator.
func (e *emitter) mark(l *classfile.Label) {
	e.c.Mark(l)
	e.alive = true
}

// markAt places a label without reviving — the end of a try range, which is a
// position rather than a target.
func (e *emitter) markAt(l *classfile.Label) {
	if e.alive {
		e.c.Mark(l)
		return
	}
	e.c.Mark(l)
	e.alive = false
}

func (e *emitter) markHandler(l *classfile.Label) {
	e.c.Mark(l)
	e.alive = true
}

func labelName(id *ast.Ident, f *token.File) string {
	if id == nil {
		return ""
	}
	return id.Name(f)
}

func (e *emitter) loadSlot(slot int, t desc.Type) { loadDesc(e.c, slot, t) }

func loadDesc(w *classfile.CodeWriter, slot int, t desc.Type) {
	if t.IsRef() {
		w.Aload(slot)
		return
	}
	switch t.Kind {
	case desc.Long:
		w.Lload(slot)
	case desc.Float:
		w.Fload(slot)
	case desc.Double:
		w.Dload(slot)
	default:
		w.Iload(slot)
	}
}

func loadRaw(w *classfile.CodeWriter, slot int, descriptor string) {
	loadDesc(w, slot, mustParseField(descriptor))
}

func mustParse(descriptor string) desc.Method {
	m, err := desc.ParseMethod(descriptor)
	if err != nil {
		bug("bad descriptor %q: %v", descriptor, err)
	}
	return m
}

func mustParseField(descriptor string) desc.Type {
	t, err := desc.ParseField(descriptor)
	if err != nil {
		bug("bad descriptor %q: %v", descriptor, err)
	}
	return t
}

func slotsOf(descriptor string) int { return mustParseField(descriptor).Slots() }

func castName(t desc.Type) string {
	if t.Dims > 0 {
		return t.String()
	}
	return t.Name
}