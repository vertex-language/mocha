package types

// The signature grammar of JVMS §4.7.9.1. This is the only parser for it in
// the toolchain: classfile retains Signature as an opaque string precisely so
// that this package can be the one place that reads it.
//
// Parsing is separated from resolution. A sigType tree carries names as
// strings and touches no symbol table, so resolution never re-walks bytes and
// parsing never needs a class path.
//
// A signature that fails to parse is reported as absent, never as an error:
// the attribute is optional metadata, and falling back to the descriptor
// erases generics without making anything unusable. This is the same trade
// classfile makes for an attribute at the wrong location or the wrong version.

type sigType interface{ sigNode() }

type sigBase struct{ ch byte }

type sigArray struct{ elem sigType }

type sigVar struct{ name string }

// sigArg is one TypeArgument. wild is 0 for a plain argument, '*' for the
// unbounded wildcard, '+' for extends and '-' for super.
type sigArg struct {
	wild byte
	typ  sigType
}

type sigSuffix struct {
	name string
	args []sigArg
}

type sigClass struct {
	name   string // internal form, package specifier folded in
	args   []sigArg
	suffix []sigSuffix
}

func (*sigBase) sigNode()  {}
func (*sigArray) sigNode() {}
func (*sigVar) sigNode()   {}
func (*sigClass) sigNode() {}

// sigParam is one TypeParameter: an identifier, an optional class bound, and
// any interface bounds, flattened into one list in declaration order.
//
// An empty ClassBound is legal and meaningful: <T::Comparable<T>> means the
// interface bound is the only bound, not that Object is intersected with it.
type sigParam struct {
	name   string
	bounds []sigType
}

type classSig struct {
	params []sigParam
	super  sigType
	ifaces []sigType
}

type methodSig struct {
	params []sigParam
	args   []sigType
	result sigType
	throws []sigType
}

// --- entry points -----------------------------------------------------------

func parseClassSignature(s string) (classSig, bool) {
	if s == "" {
		return classSig{}, false
	}
	p := &sigParser{s: s}
	var out classSig
	out.params = p.typeParams()
	out.super = p.referenceType()
	for !p.done() && p.err == nil {
		out.ifaces = append(out.ifaces, p.referenceType())
	}
	if p.err != nil || !p.done() {
		return classSig{}, false
	}
	return out, true
}

func parseMethodSignature(s string) (methodSig, bool) {
	if s == "" {
		return methodSig{}, false
	}
	p := &sigParser{s: s}
	var out methodSig
	out.params = p.typeParams()
	if !p.eat('(') {
		return methodSig{}, false
	}
	for !p.at(')') && p.err == nil && !p.done() {
		out.args = append(out.args, p.javaType())
	}
	if !p.eat(')') {
		return methodSig{}, false
	}
	out.result = p.javaType() // Result admits V, which javaType accepts
	for p.at('^') && p.err == nil {
		p.next()
		out.throws = append(out.throws, p.referenceType())
	}
	if p.err != nil || !p.done() {
		return methodSig{}, false
	}
	return out, true
}

func parseFieldSignature(s string) (sigType, bool) {
	if s == "" {
		return nil, false
	}
	p := &sigParser{s: s}
	t := p.referenceType()
	if p.err != nil || !p.done() {
		return nil, false
	}
	return t, true
}

// --- parser -----------------------------------------------------------------

type sigParser struct {
	s   string
	i   int
	err error
}

type sigError struct{ msg string }

func (e *sigError) Error() string { return "types: signature: " + e.msg }

func (p *sigParser) fail(msg string) {
	if p.err == nil {
		p.err = &sigError{msg}
	}
}

func (p *sigParser) done() bool { return p.i >= len(p.s) }

func (p *sigParser) at(c byte) bool { return p.i < len(p.s) && p.s[p.i] == c }

func (p *sigParser) next() byte {
	if p.done() {
		p.fail("unexpected end")
		return 0
	}
	c := p.s[p.i]
	p.i++
	return c
}

func (p *sigParser) eat(c byte) bool {
	if p.at(c) {
		p.i++
		return true
	}
	p.fail("expected " + string(c))
	return false
}

// typeParams parses an optional TypeParameters group.
func (p *sigParser) typeParams() []sigParam {
	if !p.at('<') {
		return nil
	}
	p.next()
	var out []sigParam
	for !p.at('>') && p.err == nil && !p.done() {
		out = append(out, p.typeParam())
	}
	p.eat('>')
	return out
}

func (p *sigParser) typeParam() sigParam {
	var tp sigParam
	tp.name = p.ident(":")
	if tp.name == "" {
		p.fail("empty type parameter name")
		return tp
	}
	if !p.eat(':') {
		return tp
	}
	// ClassBound may be empty, which is how a parameter bounded only by
	// interfaces is spelled.
	if !p.at(':') && !p.at('>') && !p.done() {
		tp.bounds = append(tp.bounds, p.referenceType())
	}
	for p.at(':') && p.err == nil {
		p.next()
		tp.bounds = append(tp.bounds, p.referenceType())
	}
	return tp
}

// javaType is a ReferenceTypeSignature or a BaseType. V is accepted here
// because a method's Result admits it; a field descriptor never reaches this.
func (p *sigParser) javaType() sigType {
	if p.done() {
		p.fail("unexpected end")
		return nil
	}
	switch c := p.s[p.i]; c {
	case 'B', 'C', 'D', 'F', 'I', 'J', 'S', 'Z', 'V':
		p.i++
		return &sigBase{ch: c}
	}
	return p.referenceType()
}

func (p *sigParser) referenceType() sigType {
	if p.done() {
		p.fail("unexpected end")
		return nil
	}
	switch p.s[p.i] {
	case '[':
		p.i++
		return &sigArray{elem: p.javaType()}
	case 'T':
		p.i++
		name := p.ident(";")
		if !p.eat(';') {
			return nil
		}
		return &sigVar{name: name}
	case 'L':
		p.i++
		return p.classType()
	}
	p.fail("not a reference type signature")
	return nil
}

// classType parses everything after the leading L, including any suffix chain,
// up to and including the terminating semicolon.
func (p *sigParser) classType() sigType {
	c := &sigClass{}
	c.name = p.ident("<.;")
	if c.name == "" {
		p.fail("empty class name")
		return nil
	}
	c.args = p.typeArgs()

	for p.at('.') && p.err == nil {
		p.next()
		var suf sigSuffix
		suf.name = p.ident("<.;")
		if suf.name == "" {
			p.fail("empty nested class name")
			return nil
		}
		suf.args = p.typeArgs()
		c.suffix = append(c.suffix, suf)
	}
	if !p.eat(';') {
		return nil
	}
	return c
}

func (p *sigParser) typeArgs() []sigArg {
	if !p.at('<') {
		return nil
	}
	p.next()
	var out []sigArg
	for !p.at('>') && p.err == nil && !p.done() {
		out = append(out, p.typeArg())
	}
	p.eat('>')
	return out
}

// typeArg parses one TypeArgument. The unbounded wildcard is a bare '*' with
// no following type; the bounded forms take a leading + or -.
func (p *sigParser) typeArg() sigArg {
	switch {
	case p.at('*'):
		p.next()
		return sigArg{wild: '*'}
	case p.at('+'):
		p.next()
		return sigArg{wild: '+', typ: p.referenceType()}
	case p.at('-'):
		p.next()
		return sigArg{wild: '-', typ: p.referenceType()}
	}
	return sigArg{typ: p.referenceType()}
}

// ident scans up to any of the stop characters. The package specifier is
// folded in: a class name is scanned whole, slashes included, which is exactly
// the internal form everything below this package agrees on.
func (p *sigParser) ident(stop string) string {
	start := p.i
	for p.i < len(p.s) {
		c := p.s[p.i]
		if indexByte(stop, c) >= 0 {
			break
		}
		p.i++
	}
	return p.s[start:p.i]
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}