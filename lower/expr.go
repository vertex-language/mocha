package lower

import (
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/jvm/op"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// Expression emission, §15. Opcodes are chosen by erased type: every
// expression picks its i/l/f/d/a variant from the type attr recorded.

// exprAs emits x and converts it to what the context wants.
func (e *emitter) exprAs(x ast.Expr, want types.Type) {
	if x == nil {
		return
	}
	e.expr(x)
	e.convert(e.in.Type(x), want)
}

func (e *emitter) expr(x ast.Expr) {
	if x == nil {
		return
	}
	// A folded constant emits its literal, from attr.Info.Consts. This is also
	// how every literal reaches the class file: ast holds no text, and
	// decoding one is a phase above the tree.
	if k, ok := e.in.Const(x); ok {
		if e.constant(k) {
			return
		}
	}

	switch n := x.(type) {
	case *ast.ParenExpr:
		e.expr(n.X)
	case *ast.BasicLit:
		e.literal(n)
	case *ast.Ident, *ast.Name:
		e.name(x)
	case *ast.SelectorExpr:
		e.selector(n)
	case *ast.IndexExpr:
		e.index(n)
	case *ast.CallExpr:
		e.call(n)
	case *ast.NewExpr:
		e.newExpr(n)
	case *ast.NewArrayExpr:
		e.newArray(n)
	case *ast.AssignExpr:
		e.assign(n, true)
	case *ast.BinaryExpr:
		e.binary(n)
	case *ast.UnaryExpr:
		e.unary(n)
	case *ast.PostfixExpr:
		e.incdec(n.X, n.Op, true, true)
	case *ast.CastExpr:
		e.cast(n)
	case *ast.CondExpr:
		e.condExpr(n)
	case *ast.InstanceOfExpr:
		e.instanceOf(n)
	case *ast.This:
		e.thisRef(n)
	case *ast.ClassLit:
		e.classLit(n)
	case *ast.LambdaExpr:
		e.lambdaValue(x)
	case *ast.MethodRef:
		e.lambdaValue(x)
	case *ast.Super:
		e.c.Aload(0)
	case *ast.BadExpr:
		bug("BadExpr reached pass two")
	default:
		bug("unhandled expression %T", x)
	}
}

func (e *emitter) constant(k types.Constant) bool {
	switch v := k.Value.(type) {
	case bool:
		if v {
			e.c.Iconst(1)
		} else {
			e.c.Iconst(0)
		}
	case int32:
		e.c.Iconst(v)
	case int64:
		e.c.Lconst(v)
	case float32:
		e.c.Fconst(v)
	case float64:
		e.c.Dconst(v)
	case string:
		e.c.Sconst(v)
	default:
		return false
	}
	return true
}

func (e *emitter) literal(n *ast.BasicLit) {
	switch n.Kind {
	case token.NULL:
		e.c.AconstNull()
	default:
		// Everything else is a constant expression and attr folded it, so
		// reaching here means Consts is missing an entry.
		bug("literal %s was not folded by attr", n.Kind)
	}
}

// name emits a simple or dotted name. A capture is read from its synthetic
// field, not from the enclosing method's slot — by here the local is gone.
func (e *emitter) name(x ast.Expr) {
	s := e.in.Use(x)
	switch t := s.(type) {
	case *sym.VarSym:
		e.loadVar(t, x)
	case *sym.ClassSym:
		// A type name in expression position is the qualifier of a static
		// access; the selector that owns it emits the reference.
	default:
		bug("name resolved to %T", s)
	}
}

func (e *emitter) loadVar(v *sym.VarSym, at ast.Expr) {
	if cap := e.captureOf(v); cap != nil {
		e.c.Aload(0)
		e.c.GetField(e.sym.Binary, cap.name, cap.desc)
		return
	}
	if v.Var == sym.VarField {
		t := e.tt.FieldType(v)
		d := types.Descriptor(t).String()
		if v.Flags.Has(sym.FlagStatic) {
			e.c.GetStatic(fieldOwner(v), v.Name, d)
			return
		}
		e.loadEnclosing(v.Class)
		if acc := e.accessorFor(v); acc != nil {
			e.c.InvokeStatic(e.sym.Binary, acc.name, "(L"+acc.owner+";)"+acc.desc)
			return
		}
		e.c.GetField(fieldOwner(v), v.Name, d)
		return
	}
	if !e.slots.has(v) {
		bug("local %s has no slot", v.Name)
	}
	e.loadLocal(e.slots.slot(v), e.tt.FieldType(v))
}

// loadEnclosing pushes the receiver for an implicit field or method access:
// this, or a walk out through this$0 to the class that declares the member.
func (e *emitter) loadEnclosing(owner *sym.ClassSym) {
	e.c.Aload(0)
	c := e.sym
	for c != nil && owner != nil && c != owner && e.thisField != "" {
		e.c.GetField(c.Binary, "this$0", "L"+c.Outer.Binary+";")
		c = c.Outer
	}
}

func (e *emitter) selector(n *ast.SelectorExpr) {
	s := e.in.Use(n)
	v, ok := s.(*sym.VarSym)
	if !ok {
		e.expr(n.X)
		return
	}
	t := e.tt.FieldType(v)
	d := types.Descriptor(t).String()

	if v.Flags.Has(sym.FlagStatic) {
		e.c.GetStatic(fieldOwner(v), v.Name, d)
		return
	}
	// An array's length is not a field reference.
	if v.Name == "length" && e.in.Type(n.X).Kind() == types.KindArray {
		e.expr(n.X)
		e.c.Op(op.Arraylength)
		return
	}
	e.expr(n.X)
	e.c.GetField(fieldOwner(v), v.Name, d)
}

func (e *emitter) index(n *ast.IndexExpr) {
	et := e.in.Type(n)
	e.expr(n.X)
	e.exprAs(n.Index, types.Int)
	e.c.Op(arrayLoadOp(et))
}

func (e *emitter) thisRef(n *ast.This) {
	e.c.Aload(0)
	if n.Qualifier == nil {
		return
	}
	// Outer.this walks the this$0 chain.
	want := sym.NameString(n.Qualifier, e.src)
	c := e.sym
	for c != nil && sym.Dotted(c.Binary) != want && c.Outer != nil {
		e.c.GetField(c.Binary, "this$0", "L"+c.Outer.Binary+";")
		c = c.Outer
	}
}

func (e *emitter) classLit(n *ast.ClassLit) {
	if n.Type == nil {
		e.c.GetStatic("java/lang/Void", "TYPE", "Ljava/lang/Class;")
		return
	}
	t := e.in.Type(n.Type)
	if t.Kind().IsPrimitive() {
		b := boxes[t.Kind()]
		e.c.GetStatic(b.owner, "TYPE", "Ljava/lang/Class;")
		return
	}
	e.c.Cconst(castTarget(t))
}

// ---------------------------------------------------------------- calls

func (e *emitter) call(n *ast.CallExpr) {
	m, _ := e.in.Use(n).(*sym.MethodSym)
	if m == nil {
		bug("call did not resolve to a method")
	}
	mt := e.tt.MethodType(m)
	d := e.methodDesc(m)
	owner := m.Class.Binary
	static := m.Flags.Has(sym.FlagStatic)

	// A private member of another class in the nest goes through an accessor.
	if acc := e.accessorFor(m); acc != nil {
		if !static {
			e.receiver(n, m)
		}
		e.args(n.Args, mt, m.Flags.Has(sym.FlagVarargs))
		od := d
		if !static {
			od = prependParam(d, "L"+acc.owner+";")
		}
		e.c.InvokeStatic(e.sym.Binary, acc.name, od)
		return
	}

	if !static {
		e.receiver(n, m)
	}
	e.args(n.Args, mt, m.Flags.Has(sym.FlagVarargs))

	switch {
	case static:
		e.c.InvokeStatic(owner, m.Name, d)
	case isSuperCall(n) || m.Flags.Has(sym.FlagPrivate):
		// invokespecial: a super call is not virtually dispatched, and neither
		// is a private method.
		e.c.InvokeSpecial(owner, m.Name, d)
	case m.Class.IsInterface():
		e.c.InvokeInterface(owner, m.Name, d)
	default:
		e.c.InvokeVirtual(owner, m.Name, d)
	}
}

func (e *emitter) receiver(n *ast.CallExpr, m *sym.MethodSym) {
	switch x := n.X.(type) {
	case nil:
		e.loadEnclosing(m.Class)
	case *ast.Super:
		e.c.Aload(0)
	default:
		if _, isType := e.in.Use(x).(*sym.ClassSym); isType {
			return // a static call qualified by a type name
		}
		e.expr(x)
	}
}

func isSuperCall(n *ast.CallExpr) bool {
	_, ok := n.X.(*ast.Super)
	return ok
}

// args emits an argument list, converting each to the parameter type.
//
// A varargs call gets its explicit array creation here, at the call site. attr
// picked the phase; what it does not record is *which* phase, so this infers
// it from the shape — an argument count that does not match, or a trailing
// argument that is not already assignable to the array type.
func (e *emitter) args(args []ast.Expr, mt *types.MethodType, varargs bool) {
	np := len(mt.Params)
	if varargs && np > 0 && !e.passedAsArray(args, mt) {
		for i := 0; i < np-1; i++ {
			e.exprAs(args[i], mt.Params[i])
		}
		at, ok := mt.Params[np-1].(*types.ArrayType)
		if !ok {
			bug("varargs parameter is not an array")
		}
		rest := args[np-1:]
		e.c.Iconst(int32(len(rest)))
		e.newArrayOf(at.Elem)
		for i, a := range rest {
			e.c.Op(op.Dup)
			e.c.Iconst(int32(i))
			e.exprAs(a, at.Elem)
			e.c.Op(arrayStoreOp(at.Elem))
		}
		return
	}
	for i, a := range args {
		if i < np {
			e.exprAs(a, mt.Params[i])
		} else {
			e.expr(a)
		}
	}
}

func (e *emitter) passedAsArray(args []ast.Expr, mt *types.MethodType) bool {
	np := len(mt.Params)
	if len(args) != np {
		return false
	}
	last := e.in.Type(args[np-1])
	return last.Kind() == types.KindArray || last.Kind() == types.KindNull
}

// ---------------------------------------------------------------- creation

func (e *emitter) newExpr(n *ast.NewExpr) {
	t := e.in.Type(n)
	binary := castTarget(t)
	e.c.New(binary)
	e.c.Op(op.Dup)

	// An anonymous class takes the enclosing instance and the captures its own
	// constructor declared, exactly as a named inner class does.
	if n.Outer != nil {
		e.expr(n.Outer)
	} else if needsOuter(t) {
		e.c.Aload(0)
	}

	m, _ := e.in.Use(n).(*sym.MethodSym)
	d := "()V"
	if m != nil {
		d = e.methodDesc(m)
		e.args(n.Args, e.tt.MethodType(m), m.Flags.Has(sym.FlagVarargs))
	}
	e.c.InvokeSpecial(binary, sym.InitName, d)
}

func needsOuter(t types.Type) bool {
	ct, ok := t.(*types.ClassType)
	return ok && ct.Sym != nil && ct.Sym.Outer != nil && !ct.Sym.Flags.Has(sym.FlagStatic)
}

func (e *emitter) newArray(n *ast.NewArrayExpr) {
	t := e.in.Type(n)
	if n.Init != nil {
		e.arrayInit(n.Init, t)
		return
	}
	if len(n.DimExprs) == 1 {
		e.exprAs(n.DimExprs[0].X, types.Int)
		at, _ := t.(*types.ArrayType)
		if at == nil {
			bug("array creation has no array type")
		}
		e.newArrayOf(at.Elem)
		return
	}
	for _, d := range n.DimExprs {
		e.exprAs(d.X, types.Int)
	}
	e.c.MultiANewArray(types.Descriptor(t).String(), len(n.DimExprs))
}

func (e *emitter) newArrayOf(elem types.Type) {
	if code, ok := primitiveArrayCode(elem); ok {
		e.c.NewArray(code)
		return
	}
	e.c.ANewArray(castTarget(elem))
}

func primitiveArrayCode(t types.Type) (uint8, bool) {
	switch t.Kind() {
	case types.KindBoolean:
		return op.TBoolean, true
	case types.KindChar:
		return op.TChar, true
	case types.KindFloat:
		return op.TFloat, true
	case types.KindDouble:
		return op.TDouble, true
	case types.KindByte:
		return op.TByte, true
	case types.KindShort:
		return op.TShort, true
	case types.KindInt:
		return op.TInt, true
	case types.KindLong:
		return op.TLong, true
	}
	return 0, false
}

func (e *emitter) arrayInit(a *ast.ArrayInit, want types.Type) {
	at, ok := want.(*types.ArrayType)
	if !ok {
		bug("array initialiser for non-array type %s", want)
	}
	e.c.Iconst(int32(len(a.Elts)))
	e.newArrayOf(at.Elem)
	for i, el := range a.Elts {
		e.c.Op(op.Dup)
		e.c.Iconst(int32(i))
		e.initValue(el, at.Elem)
		e.c.Op(arrayStoreOp(at.Elem))
	}
}

// ---------------------------------------------------------------- operators

func (e *emitter) binary(n *ast.BinaryExpr) {
	switch n.Op {
	case token.LAND, token.LOR, token.EQL, token.NEQ,
		token.LSS, token.GTR, token.LEQ, token.GEQ:
		// A comparison in value position materialises through cond.
		e.condValue(n)
		return
	case token.ADD:
		if e.isConcat(n) {
			e.concat(n)
			return
		}
	}

	lt, rt := e.in.Type(n.X), e.in.Type(n.Y)
	rest := e.in.Type(n)

	// A shift does not promote its operands together: §15.19 promotes each
	// separately, and the result type is the left operand's.
	if n.Op == token.SHL || n.Op == token.SHR || n.Op == token.USHR {
		e.exprAs(n.X, rest)
		e.exprAs(n.Y, types.Int)
		e.c.Op(shiftOp(n.Op, rest.Kind()))
		return
	}

	p := types.PromoteBinary(lt, rt)
	if rest.Kind() == types.KindBoolean {
		p = types.PromoteBinary(lt, rt) // & | ^ on booleans stay booleans
	}
	e.exprAs(n.X, p)
	e.exprAs(n.Y, p)
	e.c.Op(arithOp(n.Op, p.Kind()))
}

func (e *emitter) unary(n *ast.UnaryExpr) {
	switch n.Op {
	case token.NOT:
		e.condValue(n)
	case token.ADD:
		e.exprAs(n.X, e.in.Type(n)) // unary plus is a promotion and nothing else
	case token.SUB:
		t := e.in.Type(n)
		e.exprAs(n.X, t)
		e.c.Op(negOp(t.Kind()))
	case token.TILDE:
		t := e.in.Type(n)
		e.exprAs(n.X, t)
		if t.Kind() == types.KindLong {
			e.c.Lconst(-1)
			e.c.Op(op.Lxor)
		} else {
			e.c.Iconst(-1)
			e.c.Op(op.Ixor)
		}
	case token.INC, token.DEC:
		e.incdec(n.X, n.Op, true, false)
	default:
		bug("unhandled unary %s", n.Op)
	}
}

func (e *emitter) cast(n *ast.CastExpr) {
	from := e.in.Type(n.X)
	to := e.in.Type(n)
	e.expr(n.X)
	e.convert(from, to)
}

func (e *emitter) instanceOf(n *ast.InstanceOfExpr) {
	e.expr(n.X)
	if n.Type != nil {
		e.c.InstanceOf(castTarget(e.in.Type(n.Type)))
		return
	}
	// A type pattern binds, so it tests and then stores.
	tp, ok := n.Pattern.(*ast.TypePattern)
	if !ok {
		bug("unsupported pattern reached pass two")
	}
	t := e.in.Type(tp.Type)
	v, _ := e.in.Use(tp.Name).(*sym.VarSym)
	if v == nil {
		bug("pattern variable did not resolve")
	}
	slot := e.slots.declare(v, t)

	no, done := e.c.NewLabel(), e.c.NewLabel()
	e.c.Op(op.Dup)
	e.c.InstanceOf(castTarget(t))
	e.c.IfEq(no)
	e.c.CheckCast(castTarget(t))
	e.storeLocal(slot, t)
	e.c.Iconst(1)
	e.c.Goto(done)
	e.alive = false
	e.mark(no)
	e.c.Op(op.Pop)
	e.c.Iconst(0)
	e.mark(done)
}

// A conditional expression is a branch, not a select: both arms are converted
// to the result type so the two paths agree on what is on the stack.
func (e *emitter) condExpr(n *ast.CondExpr) {
	t := e.in.Type(n)
	els, done := e.c.NewLabel(), e.c.NewLabel()
	e.cond(n.Cond, els, false)
	e.exprAs(n.Then, t)
	e.c.Goto(done)
	e.alive = false
	e.mark(els)
	e.exprAs(n.Else, t)
	e.mark(done)
}

// ---------------------------------------------------------------- lvalues

// assign implements plain and compound assignment. An lvalue is evaluated
// once: a[i()] += f() evaluates the arrayref and the index one time, dup2s
// them to reload, and dup_x2s the result out if the enclosing expression
// wants it.
func (e *emitter) assign(n *ast.AssignExpr, value bool) {
	it := e.lvalue(n.LHS)
	t := it.typ()

	if n.Op == token.ASSIGN {
		e.initValue(n.RHS, t)
		if value {
			it.stash(e)
		}
		it.store(e)
		return
	}

	// Compound assignment: read, operate, write, with one evaluation of the
	// address. Not desugared into a temp — the JVM has the instructions, and
	// spilling would generate worse code than javac for no reason.
	it.dupAddr(e)
	it.load(e)

	binOp := compoundOp(n.Op)
	rt := e.in.Type(n.RHS)

	if binOp == token.SHL || binOp == token.SHR || binOp == token.USHR {
		e.exprAs(n.RHS, types.Int)
		e.c.Op(shiftOp(binOp, t.Kind()))
	} else if binOp == token.ADD && e.stringItem(t) {
		bug("compound string concatenation needs a StringBuilder chain")
	} else {
		p := types.PromoteBinary(t, rt)
		e.convert(t, p)
		e.exprAs(n.RHS, p)
		e.c.Op(arithOp(binOp, p.Kind()))
		// §15.26.2: the result is narrowed back to the variable's type by an
		// implicit cast, which is why `byte b; b += 300;` compiles.
		e.convert(p, t)
	}

	if value {
		it.stash(e)
	}
	it.store(e)
}

func (e *emitter) stringItem(t types.Type) bool {
	ct, ok := t.(*types.ClassType)
	return ok && ct.Binary() == sym.StringName
}

// incdec emits ++ and -- in either position. An int local takes iinc, which is
// the one case where no read/modify/write is needed.
func (e *emitter) incdec(x ast.Expr, kind token.Kind, value, postfix bool) {
	delta := int32(1)
	if kind == token.DEC {
		delta = -1
	}
	t := e.in.Type(x)

	if v, ok := e.simpleIntLocal(x); ok {
		slot := e.slots.slot(v)
		if value && postfix {
			e.c.Iload(slot)
		}
		e.c.Iinc(slot, int(delta))
		if value && !postfix {
			e.c.Iload(slot)
		}
		return
	}

	it := e.lvalue(x)
	it.dupAddr(e)
	it.load(e)
	if value && postfix {
		it.stash(e)
	}
	p := types.Promote(t)
	e.convert(t, p)
	e.pushOne(p, delta)
	e.c.Op(arithOp(token.ADD, p.Kind()))
	e.convert(p, t)
	if value && !postfix {
		it.stash(e)
	}
	it.store(e)
}

func (e *emitter) simpleIntLocal(x ast.Expr) (*sym.VarSym, bool) {
	id, ok := x.(*ast.Ident)
	if !ok {
		return nil, false
	}
	v, ok := e.in.Use(id).(*sym.VarSym)
	if !ok || v.Var == sym.VarField || e.captureOf(v) != nil {
		return nil, false
	}
	if e.tt.FieldType(v).Kind() != types.KindInt {
		return nil, false
	}
	return v, e.slots.has(v)
}

func (e *emitter) pushOne(t types.Type, delta int32) {
	switch t.Kind() {
	case types.KindLong:
		e.c.Lconst(int64(delta))
	case types.KindFloat:
		e.c.Fconst(float32(delta))
	case types.KindDouble:
		e.c.Dconst(float64(delta))
	default:
		e.c.Iconst(delta)
	}
}

// lvalue builds the item for an assignable expression and pushes whatever the
// load and store need beneath the value.
func (e *emitter) lvalue(x ast.Expr) item {
	switch n := x.(type) {
	case *ast.ParenExpr:
		return e.lvalue(n.X)

	case *ast.IndexExpr:
		e.expr(n.X)
		e.exprAs(n.Index, types.Int)
		return indexItem{t: e.in.Type(x)}

	case *ast.SelectorExpr:
		v, _ := e.in.Use(n).(*sym.VarSym)
		if v == nil {
			bug("assignment target did not resolve to a variable")
		}
		if v.Flags.Has(sym.FlagStatic) {
			return e.itemFor(v)
		}
		e.expr(n.X)
		return e.itemFor(v)

	default:
		v, _ := e.in.Use(x).(*sym.VarSym)
		if v == nil {
			bug("assignment target did not resolve to a variable")
		}
		if v.Var == sym.VarField && !v.Flags.Has(sym.FlagStatic) {
			e.loadEnclosing(v.Class)
		}
		return e.itemFor(v)
	}
}

// ---------------------------------------------------------------- opcodes

func arithOp(k token.Kind, t types.Kind) op.Op {
	switch t {
	case types.KindLong:
		switch k {
		case token.ADD:
			return op.Ladd
		case token.SUB:
			return op.Lsub
		case token.MUL:
			return op.Lmul
		case token.QUO:
			return op.Ldiv
		case token.REM:
			return op.Lrem
		case token.AND:
			return op.Land
		case token.OR:
			return op.Lor
		case token.XOR:
			return op.Lxor
		}
	case types.KindFloat:
		switch k {
		case token.ADD:
			return op.Fadd
		case token.SUB:
			return op.Fsub
		case token.MUL:
			return op.Fmul
		case token.QUO:
			return op.Fdiv
		case token.REM:
			return op.Frem
		}
	case types.KindDouble:
		switch k {
		case token.ADD:
			return op.Dadd
		case token.SUB:
			return op.Dsub
		case token.MUL:
			return op.Dmul
		case token.QUO:
			return op.Ddiv
		case token.REM:
			return op.Drem
		}
	default:
		switch k {
		case token.ADD:
			return op.Iadd
		case token.SUB:
			return op.Isub
		case token.MUL:
			return op.Imul
		case token.QUO:
			return op.Idiv
		case token.REM:
			return op.Irem
		case token.AND:
			return op.Iand
		case token.OR:
			return op.Ior
		case token.XOR:
			return op.Ixor
		}
	}
	bug("no opcode for %s on %s", k, t)
	return op.Nop
}

func shiftOp(k token.Kind, t types.Kind) op.Op {
	long := t == types.KindLong
	switch k {
	case token.SHL:
		if long {
			return op.Lshl
		}
		return op.Ishl
	case token.SHR:
		if long {
			return op.Lshr
		}
		return op.Ishr
	case token.USHR:
		if long {
			return op.Lushr
		}
		return op.Iushr
	}
	bug("not a shift: %s", k)
	return op.Nop
}

func negOp(t types.Kind) op.Op {
	switch t {
	case types.KindLong:
		return op.Lneg
	case types.KindFloat:
		return op.Fneg
	case types.KindDouble:
		return op.Dneg
	}
	return op.Ineg
}

// cmpOp picks between the g and l variants. NaN must fail both `<` and `>`, so
// the variant chosen is the one whose NaN result (1 or -1) fails the test being
// emitted: fcmpl for the less-than family, fcmpg for greater-than.
func cmpOp(t types.Kind, k token.Kind) op.Op {
	g := k == token.GTR || k == token.GEQ
	switch t {
	case types.KindLong:
		return op.Lcmp
	case types.KindFloat:
		if g {
			return op.Fcmpg
		}
		return op.Fcmpl
	case types.KindDouble:
		if g {
			return op.Dcmpg
		}
		return op.Dcmpl
	}
	bug("no comparison opcode for %s", t)
	return op.Nop
}

func compoundOp(k token.Kind) token.Kind {
	switch k {
	case token.ADD_ASSIGN:
		return token.ADD
	case token.SUB_ASSIGN:
		return token.SUB
	case token.MUL_ASSIGN:
		return token.MUL
	case token.QUO_ASSIGN:
		return token.QUO
	case token.REM_ASSIGN:
		return token.REM
	case token.AND_ASSIGN:
		return token.AND
	case token.OR_ASSIGN:
		return token.OR
	case token.XOR_ASSIGN:
		return token.XOR
	case token.SHL_ASSIGN:
		return token.SHL
	case token.SHR_ASSIGN:
		return token.SHR
	case token.USHR_ASSIGN:
		return token.USHR
	}
	bug("not a compound assignment: %s", k)
	return k
}

func (e *emitter) accessorFor(s sym.Symbol) *accessorRec {
	owner := ownerClass(s)
	if owner == nil || owner == e.sym || !s.Base().Flags.Has(sym.FlagPrivate) {
		return nil
	}
	return e.accessors[owner.Binary+"."+s.Base().Name]
}

// lambdaValue constructs the synthetic implementor: new, dup, push the
// enclosing instance and each capture, invokespecial its constructor.
func (e *emitter) lambdaValue(x ast.Expr) {
	rec := e.lambdaFor(x)
	if rec == nil {
		bug("lambda has no synthetic class")
	}
	e.c.New(rec.binary)
	e.c.Op(op.Dup)

	desc := "("
	if !rec.inStatic {
		e.c.Aload(0)
		desc += "L" + e.sym.Binary + ";"
	}
	for _, v := range rec.captures {
		t := e.tt.FieldType(v)
		if cap := e.captureOf(v); cap != nil {
			e.c.Aload(0)
			e.c.GetField(e.sym.Binary, cap.name, cap.desc)
		} else {
			e.loadLocal(e.slots.slot(v), t)
		}
		desc += types.Descriptor(t).String()
	}
	desc += ")V"
	e.c.InvokeSpecial(rec.binary, sym.InitName, desc)
}

func (e *emitter) lambdaFor(x ast.Expr) *lambdaRec {
	for _, rec := range e.lambdas {
		if ast.Node(rec.expr) == ast.Node(x) || ast.Node(rec.ref) == ast.Node(x) {
			return rec
		}
	}
	return nil
}