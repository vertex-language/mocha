// Package classfile reads the JVM class file format, as specified in chapter 4
// of the Java Virtual Machine Specification.
//
// The package handles the binary format and nothing else. It does not open
// jars, resolve a classpath, verify bytecode, or perform data flow analysis;
// those belong to classpath and ir respectively. Read takes bytes and returns
// a Class.
//
// A Class aliases the byte slice it was read from: constant pool strings, code
// arrays and raw attribute data all point into it. Reading a class therefore
// keeps the whole file alive, which is the right trade for a compiler that
// reads a class once and lowers it immediately.
package classfile

import (
	"fmt"
	"os"

	"github.com/vertex-language/mocha/jvm/desc"
)

// Magic is the class file signature.
const Magic = 0xCAFEBABE

// Mode controls how much of a class file is decoded.
type Mode uint

const (
	// DefaultMode decodes everything this package models.
	DefaultMode Mode = 0

	// SkipCode drops method bodies. Stub jars such as android.jar have no
	// interesting code, and skipping it is most of the decode time.
	SkipCode Mode = 1 << iota

	// SkipDebug drops LineNumberTable, LocalVariableTable and
	// LocalVariableTypeTable.
	SkipDebug

	// KeepRaw retains the original bytes of every attribute alongside the
	// decoded form, for byte-exact round-tripping. It applies to attributes
	// dropped by SkipCode and SkipDebug too: those still appear as *Raw,
	// because round-tripping is the point of the mode. Combining KeepRaw with
	// a Skip flag therefore saves decode time, not memory.
	KeepRaw

	// AllowPreview accepts a class file that depends on the preview features
	// of the release named by PreviewRelease.
	AllowPreview
)

// A Class is one decoded class file.
type Class struct {
	Version    Version
	Pool       *Pool
	Flags      Flags
	Name       string // internal form, e.g. "java/lang/Thread"
	Super      string // "" only for java/lang/Object
	Interfaces []string
	Fields     []Field
	Methods    []Method
	Attrs      Attrs

	// Hoisted from Attrs.
	SourceFile string
	Signature  string
	NestHost   string
	Deprecated bool
	Synthetic  bool
}

// IsInterface reports whether the file defines an interface.
func (c *Class) IsInterface() bool { return c.Flags.Has(AccInterface) }

// IsModule reports whether the file is a module-info rather than a type.
func (c *Class) IsModule() bool { return c.Flags.Has(AccModule) }

// Method returns the method with the given name and descriptor. A class file
// may not contain two methods sharing both, so the match is unique.
func (c *Class) Method(name, descriptor string) (*Method, bool) {
	for i := range c.Methods {
		if c.Methods[i].Name == name && c.Methods[i].Descriptor == descriptor {
			return &c.Methods[i], true
		}
	}
	return nil, false
}

// Field returns the field with the given name and descriptor.
func (c *Class) Field(name, descriptor string) (*Field, bool) {
	for i := range c.Fields {
		if c.Fields[i].Name == name && c.Fields[i].Descriptor == descriptor {
			return &c.Fields[i], true
		}
	}
	return nil, false
}

// ReadFile reads and decodes the class file at path.
func ReadFile(path string, mode Mode) (*Class, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return read(b, path, mode)
}

// Read decodes a class file. The returned Class aliases b, which must not be
// modified afterwards.
func Read(b []byte, mode Mode) (*Class, error) { return read(b, "", mode) }

func read(b []byte, file string, mode Mode) (*Class, error) {
	d := &decoder{r: reader{b: b, file: file}, mode: mode}

	if magic := d.r.u4(); magic != Magic {
		if d.r.err == nil {
			d.r.off = 0
			d.r.fail("bad magic 0x%08x, want 0x%08x", magic, uint32(Magic))
		}
		return nil, d.r.err
	}

	c := &Class{}
	c.Version.Minor = d.r.u2()
	c.Version.Major = d.r.u2()
	if d.r.err != nil {
		return nil, d.r.err
	}
	if err := c.Version.check(mode&AllowPreview != 0); err != nil {
		return nil, &SyntaxError{File: file, Off: 4, Msg: err.Error()}
	}
	d.version = c.Version

	c.Pool = readPool(&d.r, c.Version)
	if d.r.err != nil {
		return nil, d.r.err
	}
	d.pool = c.Pool

	c.Flags = Flags(d.r.u2())

	var err error
	if c.Name, err = d.class(d.r.u2()); err != nil {
		return nil, err
	}
	// super_class is zero exactly for java/lang/Object and for module-info.
	if super := d.r.u2(); super != 0 {
		if c.Super, err = d.class(super); err != nil {
			return nil, err
		}
	} else if c.Name != "java/lang/Object" && !c.IsModule() {
		return nil, fmt.Errorf("%s: super_class is 0 but this class is %s, not java/lang/Object",
			d.name(), desc.Binary(c.Name))
	}

	n := int(d.r.u2())
	c.Interfaces = make([]string, 0, n)
	for i := 0; i < n && !d.r.done(); i++ {
		name, err := d.class(d.r.u2())
		if err != nil {
			return nil, err
		}
		c.Interfaces = append(c.Interfaces, name)
	}

	if c.Fields, err = d.members(locField); err != nil {
		return nil, err
	}
	if c.Methods, err = d.methods(); err != nil {
		return nil, err
	}
	if c.Attrs, err = d.attributes(locClass); err != nil {
		return nil, err
	}
	if d.r.err != nil {
		return nil, d.r.err
	}
	if d.r.off != len(b) {
		return nil, &SyntaxError{File: file, Off: d.r.off,
			Msg: fmt.Sprintf("%d trailing bytes after the class file", len(b)-d.r.off)}
	}

	hoistClass(c)
	return c, nil
}

func hoistClass(c *Class) {
	for _, a := range c.Attrs {
		switch t := a.(type) {
		case *SourceFile:
			c.SourceFile = t.Value
		case *Signature:
			c.Signature = t.Value
		case *NestHost:
			c.NestHost = t.Host
		case *Deprecated:
			c.Deprecated = true
		case *Synthetic:
			c.Synthetic = true
		case *BootstrapMethods:
			c.Pool.bsms = t.Methods
		}
	}
}