package classfile

import (
	"fmt"
	"os"

	"github.com/vertex-language/mocha/jvm/desc"
)

// MaxCodeLength is the ceiling on a method's code array (JVMS §4.9.1).
const MaxCodeLength = 65535

// maxReplays bounds the branch-widening fixpoint. Each pass can only widen
// branches, never narrow them, so it terminates; the bound catches bugs.
const maxReplays = 10

// A Builder assembles a class file.
//
// The default target is class file 49.0. From 50.0 the verifier expects a
// StackMapTable, and generating correct frames means implementing the type
// checker of §4.10.1, which this package does not. Emitting 49.0 gets the
// class verified by type inference instead, which every current JVM still
// supports. SetVersion rejects anything higher rather than producing a class
// that fails to load.
type Builder struct {
	pool    *PoolBuilder
	version Version
	flags   Flags
	name    string
	super   string
	ifaces  []string

	fields  []*FieldBuilder
	methods []*MethodBuilder

	sourceFile string
	err        error
}

// NewBuilder starts a class with the given internal-form name.
func NewBuilder(internal string) *Builder {
	return &Builder{
		pool:    newPoolBuilder(),
		version: Version{Major: Java5},
		flags:   AccPublic | AccSuper,
		name:    internal,
		super:   "java/lang/Object",
	}
}

// Pool exposes the interning pool, for constants this API does not cover.
func (b *Builder) Pool() *PoolBuilder { return b.pool }

// SetVersion sets the target class file version.
func (b *Builder) SetVersion(v Version) {
	if v.Major >= Java6 {
		b.fail("cannot target class file %s: versions 50.0 and above require a "+
			"StackMapTable, which this encoder does not generate", v)
		return
	}
	if v.Major < Java1_0 {
		b.fail("class file version %s is below the minimum of 45.0", v)
		return
	}
	// A minor of 65535 claims dependence on a release's preview features. This
	// encoder emits nothing preview, and a preview file is loadable only by a
	// VM of that exact release, so the claim would only ever cost the caller
	// portability.
	if v.Minor == PreviewMinor {
		b.fail("cannot target class file %s: this encoder emits no preview constructs", v)
		return
	}
	b.version = v
}

// SetFlags replaces the class access flags. ACC_SUPER should stay set: every
// VM since Java 8 treats it as set regardless, and clearing it only confuses
// older tools.
func (b *Builder) SetFlags(f Flags) { b.flags = f }

// SetSuper names the direct superclass. Pass "" only for java/lang/Object.
func (b *Builder) SetSuper(internal string) { b.super = internal }

// AddInterface appends a direct superinterface.
func (b *Builder) AddInterface(internal string) { b.ifaces = append(b.ifaces, internal) }

// SetSourceFile records the originating source name.
func (b *Builder) SetSourceFile(name string) { b.sourceFile = name }

func (b *Builder) fail(format string, args ...any) {
	if b.err == nil {
		b.err = fmt.Errorf("classfile: "+format, args...)
	}
}

// A FieldBuilder assembles one field_info.
type FieldBuilder struct {
	b          *Builder
	flags      Flags
	name       string
	descriptor string
	constant   uint16 // pool index of a ConstantValue, or 0
	signature  string
}

// Field adds a field.
func (b *Builder) Field(flags Flags, name, descriptor string) *FieldBuilder {
	if _, err := desc.ParseField(descriptor); err != nil {
		b.fail("field %s: %v", name, err)
	}
	f := &FieldBuilder{b: b, flags: flags, name: name, descriptor: descriptor}
	b.fields = append(b.fields, f)
	return f
}

// ConstantInt and its siblings attach a ConstantValue attribute. §4.7.2 gives
// the attribute meaning only on a static field, and the JLS only produces one
// for a static final, so anything else is refused rather than emitted and
// silently ignored by the VM.
func (f *FieldBuilder) ConstantInt(v int32)      { f.setConstant(f.b.pool.Int(v)) }
func (f *FieldBuilder) ConstantLong(v int64)     { f.setConstant(f.b.pool.Long(v)) }
func (f *FieldBuilder) ConstantFloat(v float32)  { f.setConstant(f.b.pool.Float(v)) }
func (f *FieldBuilder) ConstantDouble(v float64) { f.setConstant(f.b.pool.Double(v)) }
func (f *FieldBuilder) ConstantString(v string)  { f.setConstant(f.b.pool.String(v)) }

func (f *FieldBuilder) setConstant(idx uint16) {
	if !f.flags.Has(AccStatic | AccFinal) {
		f.b.fail("field %s carries a ConstantValue but is not static final", f.name)
		return
	}
	f.constant = idx
}

// Signature attaches a generic signature.
func (f *FieldBuilder) Signature(s string) { f.signature = s }

// A MethodBuilder assembles one method_info.
type MethodBuilder struct {
	b          *Builder
	flags      Flags
	name       string
	descriptor string
	signature  string
	exceptions []string

	code      []byte
	maxStack  uint16
	maxLocals uint16
	handlers  []encodedHandler
	lines     []LineNumber
	hasCode   bool
}

type encodedHandler struct {
	start, end, handler uint16
	catchType           uint16
}

// Method adds a method.
func (b *Builder) Method(flags Flags, name, descriptor string) *MethodBuilder {
	if _, err := desc.ParseMethod(descriptor); err != nil {
		b.fail("method %s: %v", name, err)
	}
	m := &MethodBuilder{b: b, flags: flags, name: name, descriptor: descriptor}
	b.methods = append(b.methods, m)
	return m
}

// Throws appends a checked exception to the Exceptions attribute.
func (m *MethodBuilder) Throws(internal string) { m.exceptions = append(m.exceptions, internal) }

// Signature attaches a generic signature.
func (m *MethodBuilder) Signature(s string) { m.signature = s }

// Code builds the method body.
//
// The body is a closure rather than a sequence of calls on a builder because
// the encoder may need to run it more than once. A conditional branch has no
// long form, so making one reach further than 32767 bytes means inverting the
// test and jumping over a goto_w — which changes that instruction's length and
// moves everything after it. Replaying the closure with the widening decisions
// from the previous pass is what makes that tractable.
//
// max_stack and max_locals are computed as the body is emitted; do not set
// them yourself. The descriptor's return type is threaded into the CodeWriter,
// which is what lets CodeWriter.Return pick the right return opcode.
func (m *MethodBuilder) Code(body func(*CodeWriter)) {
	if m.b.err != nil {
		return
	}
	widen := map[int]bool{}

	receiver := !m.flags.Has(AccStatic)
	sig, err := desc.ParseMethod(m.descriptor)
	if err != nil {
		m.b.fail("method %s: %v", m.name, err)
		return
	}
	if err := sig.CheckArgSlots(receiver); err != nil {
		m.b.fail("method %s: %v", m.name, err)
		return
	}

	for pass := 0; ; pass++ {
		if pass >= maxReplays {
			m.b.fail("method %s: branch widening did not converge after %d passes",
				m.name, maxReplays)
			return
		}

		c := &CodeWriter{
			p:         m.b.pool,
			ret:       sig.Ret,
			widen:     widen,
			pass:      pass,
			maxLocals: int32(sig.ArgSlots(receiver)),
			reachable: true,
		}
		body(c)
		if c.err != nil {
			m.b.fail("method %s: %v", m.name, c.err)
			return
		}
		if c.reachable {
			m.b.fail("method %s: control can fall off the end of the body", m.name)
			return
		}
		if c.resolve() {
			continue // widen set was updated; run it again
		}
		if c.err != nil {
			m.b.fail("method %s: %v", m.name, c.err)
			return
		}
		if len(c.w.b) == 0 {
			m.b.fail("method %s: code array is empty", m.name)
			return
		}
		if len(c.w.b) > MaxCodeLength {
			m.b.fail("method %s: code array is %d bytes, over the %d limit",
				m.name, len(c.w.b), MaxCodeLength)
			return
		}
		if c.maxStack > MaxStack || c.maxLocals > MaxLocals {
			m.b.fail("method %s: max_stack %d / max_locals %d exceed the u2 limits",
				m.name, c.maxStack, c.maxLocals)
			return
		}

		m.code = c.w.b
		m.maxStack = uint16(c.maxStack)
		m.maxLocals = uint16(c.maxLocals)
		m.lines = c.lines
		m.hasCode = true

		for _, h := range c.handlers {
			var ct uint16
			if h.catchType != "" {
				ct = m.b.pool.Class(h.catchType)
			}
			m.handlers = append(m.handlers, encodedHandler{
				start:     uint16(h.start.pc),
				end:       uint16(h.end.pc),
				handler:   uint16(h.handler.pc),
				catchType: ct,
			})
		}
		return
	}
}

// Bytes assembles the class file.
//
// Method bodies have already been emitted by the time this runs — MethodBuilder
// .Code interns as it encodes — so the pool is populated before it is written.
// What this function must still get right is that everything it interns itself
// (this_class, super_class, the interfaces, member names and descriptors, and
// the attribute names) happens before pool.emit, which is why the field and
// method tables are built into scratch writers first and spliced in afterwards.
func (b *Builder) Bytes() ([]byte, error) {
	if b.err != nil {
		return nil, b.err
	}

	// Intern in a fixed order so output is deterministic across runs.
	thisIdx := b.pool.Class(b.name)
	var superIdx uint16
	if b.super != "" {
		superIdx = b.pool.Class(b.super)
	} else if b.name != "java/lang/Object" {
		return nil, fmt.Errorf("classfile: %s has no superclass but is not java/lang/Object", b.name)
	}
	ifaceIdx := make([]uint16, len(b.ifaces))
	for i, s := range b.ifaces {
		ifaceIdx[i] = b.pool.Class(s)
	}

	var fields, methods writer
	fields.u2(uint16(len(b.fields)))
	for _, f := range b.fields {
		b.emitField(&fields, f)
	}
	methods.u2(uint16(len(b.methods)))
	for _, m := range b.methods {
		b.emitMethod(&methods, m)
	}

	var classAttrs writer
	n := 0
	if b.sourceFile != "" {
		n++
	}
	classAttrs.u2(uint16(n))
	if b.sourceFile != "" {
		nameIdx := b.pool.UTF8("SourceFile")
		valIdx := b.pool.UTF8(b.sourceFile)
		classAttrs.attr(nameIdx, func(w *writer) { w.u2(valIdx) })
	}

	if err := b.pool.Err(); err != nil {
		return nil, err
	}
	if b.err != nil {
		return nil, b.err
	}

	var w writer
	w.u4(Magic)
	w.u2(b.version.Minor)
	w.u2(b.version.Major)
	b.pool.emit(&w)
	w.u2(uint16(b.flags))
	w.u2(thisIdx)
	w.u2(superIdx)
	w.u2(uint16(len(ifaceIdx)))
	for _, i := range ifaceIdx {
		w.u2(i)
	}
	w.raw(fields.b)
	w.raw(methods.b)
	w.raw(classAttrs.b)
	return w.b, nil
}

// WriteFile assembles the class and writes it to path.
func (b *Builder) WriteFile(path string) error {
	out, err := b.Bytes()
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func (b *Builder) emitField(w *writer, f *FieldBuilder) {
	w.u2(uint16(f.flags))
	w.u2(b.pool.UTF8(f.name))
	w.u2(b.pool.UTF8(f.descriptor))

	var attrs writer
	n := 0
	if f.constant != 0 {
		n++
	}
	if f.signature != "" {
		n++
	}
	attrs.u2(uint16(n))
	if f.constant != 0 {
		idx := f.constant
		attrs.attr(b.pool.UTF8("ConstantValue"), func(w *writer) { w.u2(idx) })
	}
	if f.signature != "" {
		s := b.pool.UTF8(f.signature)
		attrs.attr(b.pool.UTF8("Signature"), func(w *writer) { w.u2(s) })
	}
	w.raw(attrs.b)
}

func (b *Builder) emitMethod(w *writer, m *MethodBuilder) {
	if m.hasCode && (m.flags.Has(AccAbstract) || m.flags.Has(AccNative)) {
		b.fail("method %s is abstract or native but has a body", m.name)
		return
	}
	if !m.hasCode && !m.flags.Has(AccAbstract) && !m.flags.Has(AccNative) {
		b.fail("method %s has no body but is neither abstract nor native", m.name)
		return
	}

	w.u2(uint16(m.flags))
	w.u2(b.pool.UTF8(m.name))
	w.u2(b.pool.UTF8(m.descriptor))

	var attrs writer
	n := 0
	if m.hasCode {
		n++
	}
	if len(m.exceptions) > 0 {
		n++
	}
	if m.signature != "" {
		n++
	}
	attrs.u2(uint16(n))

	if m.hasCode {
		attrs.attr(b.pool.UTF8("Code"), func(w *writer) {
			w.u2(m.maxStack)
			w.u2(m.maxLocals)
			w.u4(uint32(len(m.code)))
			w.raw(m.code)
			w.u2(uint16(len(m.handlers)))
			for _, h := range m.handlers {
				w.u2(h.start)
				w.u2(h.end)
				w.u2(h.handler)
				w.u2(h.catchType)
			}
			cn := 0
			if len(m.lines) > 0 {
				cn++
			}
			w.u2(uint16(cn))
			if len(m.lines) > 0 {
				w.attr(b.pool.UTF8("LineNumberTable"), func(w *writer) {
					w.u2(uint16(len(m.lines)))
					for _, ln := range m.lines {
						w.u2(ln.StartPC)
						w.u2(ln.Line)
					}
				})
			}
		})
	}
	if len(m.exceptions) > 0 {
		idx := make([]uint16, len(m.exceptions))
		for i, e := range m.exceptions {
			idx[i] = b.pool.Class(e)
		}
		attrs.attr(b.pool.UTF8("Exceptions"), func(w *writer) {
			w.u2(uint16(len(idx)))
			for _, i := range idx {
				w.u2(i)
			}
		})
	}
	if m.signature != "" {
		s := b.pool.UTF8(m.signature)
		attrs.attr(b.pool.UTF8("Signature"), func(w *writer) { w.u2(s) })
	}
	w.raw(attrs.b)
}