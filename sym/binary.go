package sym

import (
	"fmt"

	"github.com/vertex-language/mocha/classfile"
)

// binaryCompleter fills a ClassSym from a class file on the path.
//
// It reads with SkipCode|SkipDebug: a symbol table wants signatures, and the
// bodies are the expensive part. A generic signature is recorded verbatim and
// not parsed — erasure lives in the descriptor, which is what everything below
// this package consumes.
type binaryCompleter struct {
	table  *Table
	binary string
}

func (bc *binaryCompleter) Complete(c *ClassSym) error {
	cf, err := bc.table.load(bc.binary)
	if err != nil {
		return err
	}
	if cf.Name != bc.binary {
		// The path found the file under one name and it calls itself another.
		// Trusting either silently produces a symbol nothing can resolve.
		return fmt.Errorf("sym: %s declares itself as %s", bc.binary, cf.Name)
	}
	if cf.IsModule() {
		return fmt.Errorf("sym: %s is a module descriptor, not a type", bc.binary)
	}

	c.Flags |= classFileClassFlags(cf.Flags)
	if cf.Deprecated {
		c.Flags |= FlagDeprecated
	}
	if cf.Synthetic {
		c.Flags |= FlagSynthetic
	}
	c.Super = cf.Super
	c.Interfaces = cf.Interfaces
	c.SourceFile = cf.SourceFile

	if r, ok := cf.Attrs.Find("Record").(*classfile.Record); ok {
		c.Flags |= FlagRecord
		bc.enterRecordComponents(c, r)
	}
	if p, ok := cf.Attrs.Find("PermittedSubclasses").(*classfile.PermittedSubclasses); ok {
		c.Flags |= FlagSealed
		c.Permits = p.Classes
	}
	if ic, ok := cf.Attrs.Find("InnerClasses").(*classfile.InnerClasses); ok {
		bc.enterNested(c, ic)
	}

	for i := range cf.Fields {
		bc.enterField(c, &cf.Fields[i])
	}
	for i := range cf.Methods {
		bc.enterMethod(c, &cf.Methods[i])
	}
	return nil
}

func (bc *binaryCompleter) enterField(c *ClassSym, f *classfile.Field) {
	v := &VarSym{
		Sym: Sym{
			Name:  f.Name,
			Kind:  KindVar,
			Flags: classFileFieldFlags(f.Flags),
			Owner: c,
		},
		Var:        VarField,
		Class:      c,
		Descriptor: f.Descriptor,
		Const:      f.ConstantValue,
	}
	if f.Flags.Has(classfile.AccEnum) {
		v.Var = VarEnumConstant
	}
	if f.Deprecated {
		v.Flags |= FlagDeprecated
	}
	c.Members.Enter(v)
}

func (bc *binaryCompleter) enterMethod(c *ClassSym, m *classfile.Method) {
	// <clinit> is never referenced symbolically — the VM calls it, and no
	// invocation instruction may name it — so it is not a member anything can
	// resolve to.
	if m.IsClassInit() {
		return
	}
	ms := &MethodSym{
		Sym: Sym{
			Name:  m.Name,
			Kind:  KindMethod,
			Flags: classFileMethodFlags(m.Flags),
			Owner: c,
		},
		Class:      c,
		Descriptor: m.Descriptor,
		Throws:     m.Exceptions,
	}
	if m.Deprecated {
		ms.Flags |= FlagDeprecated
	}
	c.Members.Enter(ms)
}

// enterNested walks the InnerClasses table twice: once to find this class's own
// row, which names its enclosing class, and once for the rows it encloses.
//
// A row with an empty SimpleName is an anonymous class and a row with an empty
// Outer is local or anonymous; neither is a member type, and neither can be
// named from source.
func (bc *binaryCompleter) enterNested(c *ClassSym, ic *classfile.InnerClasses) {
	for _, row := range ic.Classes {
		if row.Inner == c.Binary {
			if row.Outer != "" {
				c.Outer = bc.table.Class(row.Outer)
			}
			// The inner row carries the flags a nested class actually has;
			// the top-level access_flags of a nested class are a lossy copy.
			c.Flags |= classFileClassFlags(row.Flags)
			if row.Flags&classfile.AccStatic != 0 {
				c.Flags |= FlagStatic
			}
			continue
		}
		if row.Outer != c.Binary || row.SimpleName == "" {
			continue
		}
		if nested := bc.table.Class(row.Inner); nested != nil {
			c.Members.Enter(nested)
		}
	}
}

func (bc *binaryCompleter) enterRecordComponents(c *ClassSym, r *classfile.Record) {
	for _, comp := range r.Components {
		// The component itself is not a member — the field and the accessor
		// generated from it are, and both are already in the class file. This
		// records the component list so a record pattern can be checked in
		// declaration order, which is the one thing the field table loses.
		v := &VarSym{
			Sym: Sym{
				Name:  comp.Name,
				Kind:  KindVar,
				Flags: FlagImplicit | FlagFinal | FlagPrivate,
				Owner: c,
			},
			Var:        VarRecordComponent,
			Class:      c,
			Descriptor: comp.Descriptor,
		}
		c.recordComponents = append(c.recordComponents, v)
	}
}