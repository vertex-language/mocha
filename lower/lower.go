// Package lower is code generation: it walks an attributed, checked tree and
// drives classfile.Builder until a class file falls out.
//
// This is javac's TransTypes, Lower and Gen in one package. The class file is
// the IR: classfile.Builder is the intermediate representation every Java
// compiler eventually produces, and building a second one to hold the same
// facts on the way there would be a data structure whose only consumer is the
// code three hundred lines below it.
//
// attr says what everything is. flow says what is assigned, reachable and
// captured. warn says whether it is legal. This package is the first that asks
// what it runs as, and the last that knows Java existed.
package lower

import (
	"path/filepath"
	"sort"

	"github.com/vertex-language/mocha/analyzer/attr"
	"github.com/vertex-language/mocha/analyzer/flow"
	"github.com/vertex-language/mocha/ast"
	"github.com/vertex-language/mocha/classfile"
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/token"
	"github.com/vertex-language/mocha/types"
)

// Lower emits one classfile.Builder per class: top-level, member, local,
// anonymous, and one per lambda.
//
// The caller decides what becomes of them — a file under out/com/example/, an
// entry in a jar, or input to ir/builder on the way to dex. This package never
// writes anything.
//
// Do not call this on a unit that did not type-check. Emitting from a tree with
// an ErrorType in it produces a class file that fails verification, which is a
// worse diagnostic than the one already reported.
func Lower(in *attr.Info, fl *flow.Flow, tt *types.Table, u *sym.Unit) (
	[]*classfile.Builder, []token.Diagnostic) {

	if u == nil || u.Module != nil {
		return nil, nil // a module declaration declares no types
	}

	lo := &lowerer{in: in, fl: fl, tt: tt, u: u, src: u.Src}

	// A compilation unit does not survive this package. In goes one *sym.Unit;
	// out come N independent classes, and Outer$1 is a sibling of Outer rather
	// than a child. The queue is that flattening.
	for _, c := range u.Types {
		lo.enqueue(c)
	}
	for _, c := range sortedLocals(in.Local) {
		lo.enqueue(c)
	}

	for i := 0; i < len(lo.queue); i++ {
		lo.class(lo.queue[i])
	}
	return lo.out, lo.diags
}

type lowerer struct {
	in  *attr.Info
	fl  *flow.Flow
	tt  *types.Table
	u   *sym.Unit
	src *token.File

	queue []*sym.ClassSym
	seen  map[*sym.ClassSym]bool

	out   []*classfile.Builder
	diags []token.Diagnostic
}

// enqueue adds a class and every member type it declares. Local and anonymous
// classes are not reachable this way — attr entered them, and Info.Local is
// where they are.
func (lo *lowerer) enqueue(c *sym.ClassSym) {
	if c == nil {
		return
	}
	if lo.seen == nil {
		lo.seen = make(map[*sym.ClassSym]bool)
	}
	if lo.seen[c] {
		return
	}
	lo.seen[c] = true
	lo.queue = append(lo.queue, c)

	if err := c.Complete(); err != nil {
		lo.errorf(c.Pos, c.End, "cannot complete %s: %v", sym.Dotted(c.Binary), err)
		return
	}
	c.Members.Each(func(s sym.Symbol) bool {
		if n, ok := s.(*sym.ClassSym); ok {
			lo.enqueue(n)
		}
		return true
	})
}

// sortedLocals orders attribution's local and anonymous classes by binary name.
// The map iteration order is not stable and the artifact must be: same inputs
// and flags, byte-identical output.
func sortedLocals(m map[ast.Decl]*sym.ClassSym) []*sym.ClassSym {
	out := make([]*sym.ClassSym, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Binary < out[j].Binary })
	return out
}

// class builds one classfile.Builder: pass one, then pass two.
func (lo *lowerer) class(c *sym.ClassSym) {
	if err := c.Complete(); err != nil {
		return // already reported by enqueue
	}

	b := classfile.NewBuilder(c.Binary)
	b.SetFlags(classFlags(c.Flags))

	// sym.ClassSym.Super is "" for java/lang/Object and for an interface; the
	// class file wants java/lang/Object in the second case.
	switch {
	case c.Super != "":
		b.SetSuper(c.Super)
	case c.Binary != sym.ObjectName:
		b.SetSuper(sym.ObjectName)
	}
	for _, i := range c.Interfaces {
		b.AddInterface(i)
	}
	if name := lo.sourceFile(c); name != "" {
		b.SetSourceFile(name)
	}

	cc := &classCtx{
		lowerer: lo,
		sym:     c,
		b:       b,
		slots:   make(map[*sym.MethodSym]*slotMap),
	}

	cc.declare() // pass one: mutates the Builder, has side effects
	cc.emit()    // pass two: closures, pure functions of the tree

	lo.out = append(lo.out, b)
}

func (lo *lowerer) sourceFile(c *sym.ClassSym) string {
	if c.SourceFile != "" {
		return c.SourceFile
	}
	if lo.src != nil {
		return filepath.Base(lo.src.Name())
	}
	return ""
}

// classCtx is one class's pass-one state, read by pass two.
type classCtx struct {
	*lowerer
	sym *sym.ClassSym
	b   *classfile.Builder

	// slots is filled by pass one and read by pass two. Assigning inside the
	// closure would survive a replay and break the widening fixpoint.
	slots map[*sym.MethodSym]*slotMap

	// pending is what pass two walks: one entry per method that has a body,
	// in the order pass one declared them.
	pending []*pendingBody

	// captures and thisField come from capture.go.
	captures  []*captureField
	thisField string // "this$0", or "" for a class with no enclosing instance
}

// pendingBody is a declared method waiting for its closure.
type pendingBody struct {
	mb   *classfile.MethodBuilder
	m    *sym.MethodSym
	body *ast.Block

	// ctor is set for <init> and carries the initialisers folded into it.
	ctor *ctorPlan
	// clinit is set for <clinit>.
	clinit *clinitPlan
}

// line records a source line against the current offset, for LineNumberTable.
func (e *emitter) line(p token.Pos) {
	if e.src == nil || !p.IsValid() {
		return
	}
	if n := e.src.Position(p).Line; n > 0 && n != e.lastLine {
		e.c.Line(n)
		e.lastLine = n
	}
}

func (lo *lowerer) errorf(pos, end token.Pos, format string, args ...any) {
	lo.diags = append(lo.diags, token.Diagnostic{
		Pos: pos, End: end,
		Severity: token.SevError,
		Msg:      sprintf(format, args...),
	})
}

// bug panics. No diagnostics from pass two: everything reportable was reported
// by attr, flow or warn, and a diagnostic inside a replayed closure would fire
// twice and break the one-diagnostic rule held from the parser down. A
// condition pass two cannot emit is a bug in a phase below.
func bug(format string, args ...any) {
	panic("lower: " + sprintf(format, args...))
}