package classfile

import "fmt"

// decoder carries the reader plus the context every attribute parser needs.
type decoder struct {
	r       reader
	pool    *Pool
	version Version
	mode    Mode
}

func (d *decoder) name() string {
	if d.r.file == "" {
		return "class file"
	}
	return d.r.file
}

func (d *decoder) utf8(i uint16) (string, error) { return d.pool.UTF8(i) }

func (d *decoder) class(i uint16) (string, error) { return d.pool.Class(i) }

// optClass resolves a class index that is allowed to be zero.
func (d *decoder) optClass(i uint16) (string, error) {
	if i == 0 {
		return "", nil
	}
	return d.pool.Class(i)
}

// optUTF8 resolves a Utf8 index that is allowed to be zero.
func (d *decoder) optUTF8(i uint16) (string, error) {
	if i == 0 {
		return "", nil
	}
	return d.pool.UTF8(i)
}

func (d *decoder) members(loc location) ([]Field, error) {
	n := int(d.r.u2())
	out := make([]Field, 0, n)
	for i := 0; i < n && !d.r.done(); i++ {
		var f Field
		f.Flags = Flags(d.r.u2())
		var err error
		if f.Name, err = d.utf8(d.r.u2()); err != nil {
			return nil, err
		}
		if f.Descriptor, err = d.utf8(d.r.u2()); err != nil {
			return nil, err
		}
		if f.Attrs, err = d.attributes(loc); err != nil {
			return nil, err
		}
		for _, a := range f.Attrs {
			switch t := a.(type) {
			case *ConstantValue:
				v := t.Value
				f.ConstantValue = &v
			case *Signature:
				f.Signature = t.Value
			case *Deprecated:
				f.Deprecated = true
			case *Synthetic:
				f.Synthetic = true
			}
		}
		out = append(out, f)
	}
	return out, d.r.err
}

func (d *decoder) methods() ([]Method, error) {
	n := int(d.r.u2())
	out := make([]Method, 0, n)
	for i := 0; i < n && !d.r.done(); i++ {
		var m Method
		m.Flags = Flags(d.r.u2())
		var err error
		if m.Name, err = d.utf8(d.r.u2()); err != nil {
			return nil, err
		}
		if m.Descriptor, err = d.utf8(d.r.u2()); err != nil {
			return nil, err
		}
		if m.Attrs, err = d.attributes(locMethod); err != nil {
			return nil, err
		}
		for _, a := range m.Attrs {
			switch t := a.(type) {
			case *Code:
				m.Code = t
			case *Exceptions:
				m.Exceptions = t.Classes
			case *Signature:
				m.Signature = t.Value
			case *Deprecated:
				m.Deprecated = true
			case *Synthetic:
				m.Synthetic = true
			}
		}
		out = append(out, m)
	}
	return out, d.r.err
}

// attributes reads an attributes_count and the table that follows.
func (d *decoder) attributes(loc location) (Attrs, error) {
	n := int(d.r.u2())
	out := make(Attrs, 0, n)
	for i := 0; i < n && !d.r.done(); i++ {
		nameIdx := d.r.u2()
		length := d.r.u4()
		if d.r.done() {
			break
		}
		name, err := d.utf8(nameIdx)
		if err != nil {
			return nil, err
		}
		body := d.r.bytes(int(length))
		if d.r.done() {
			break
		}

		a, err := d.attribute(name, body, loc)
		if err != nil {
			return nil, err
		}
		if a != nil {
			out = append(out, a)
		}
		// Under KeepRaw every attribute keeps its bytes. a == nil means the
		// attribute was dropped by SkipCode or SkipDebug, and those still get
		// a *Raw: the mode exists for byte-exact round-tripping, and dropping
		// the bytes would defeat it. A *Raw is not duplicated.
		if d.mode&KeepRaw != 0 {
			if _, isRaw := a.(*Raw); !isRaw {
				out = append(out, &Raw{Name: name, Data: body})
			}
		}
	}
	return out, d.r.err
}

// attribute decodes one attribute body. It returns a *Raw for anything not
// modelled, not defined at this location, or introduced after this class file
// version — never an error, because the specification requires unrecognised
// attributes to be ignored rather than rejected.
func (d *decoder) attribute(name string, body []byte, loc location) (Attr, error) {
	spec, known := where[name]
	if !known || spec.at&loc == 0 || !d.version.AtLeast(spec.since) {
		return &Raw{Name: name, Data: body}, nil
	}

	// A nested reader bounded by the attribute body, so a length that lies
	// cannot walk into the next attribute.
	sub := &decoder{
		r:       reader{b: body, file: d.r.file},
		pool:    d.pool,
		version: d.version,
		mode:    d.mode,
	}

	switch name {
	case "SourceFile":
		v, err := sub.utf8(sub.r.u2())
		return &SourceFile{Value: v}, orErr(err, sub)

	case "Signature":
		v, err := sub.utf8(sub.r.u2())
		return &Signature{Value: v}, orErr(err, sub)

	case "ConstantValue":
		v, err := d.pool.Const(sub.r.u2(), d.version)
		if err != nil {
			return nil, err
		}
		// Table 4.7.2-A: only these five may give a field its constant value.
		// Pool.Const is deliberately broader, since it also serves ldc.
		switch v.Tag {
		case TagInteger, TagFloat, TagLong, TagDouble, TagString:
		default:
			return nil, fmt.Errorf("%s: ConstantValue names a %s, which is not a legal field constant",
				d.name(), v.Tag)
		}
		return &ConstantValue{Value: v}, orErr(nil, sub)

	case "Synthetic":
		return &Synthetic{}, orErr(nil, sub)

	case "Deprecated":
		return &Deprecated{}, orErr(nil, sub)

	case "NestHost":
		v, err := sub.class(sub.r.u2())
		return &NestHost{Host: v}, orErr(err, sub)

	case "Exceptions":
		list, err := sub.classList()
		return &Exceptions{Classes: list}, orErr(err, sub)

	case "NestMembers":
		list, err := sub.classList()
		return &NestMembers{Classes: list}, orErr(err, sub)

	case "PermittedSubclasses":
		list, err := sub.classList()
		return &PermittedSubclasses{Classes: list}, orErr(err, sub)

	case "EnclosingMethod":
		var a EnclosingMethod
		var err error
		if a.Class, err = sub.class(sub.r.u2()); err != nil {
			return nil, err
		}
		// method_index is zero when the class sits in an initialiser.
		if mi := sub.r.u2(); mi != 0 {
			if a.Name, a.Descriptor, err = d.pool.NameAndType(mi); err != nil {
				return nil, err
			}
		}
		return &a, orErr(nil, sub)

	case "InnerClasses":
		n := int(sub.r.u2())
		a := &InnerClasses{Classes: make([]InnerClass, 0, n)}
		for i := 0; i < n && !sub.r.done(); i++ {
			var ic InnerClass
			var err error
			if ic.Inner, err = sub.class(sub.r.u2()); err != nil {
				return nil, err
			}
			if ic.Outer, err = sub.optClass(sub.r.u2()); err != nil {
				return nil, err
			}
			if ic.SimpleName, err = sub.optUTF8(sub.r.u2()); err != nil {
				return nil, err
			}
			ic.Flags = Flags(sub.r.u2())
			a.Classes = append(a.Classes, ic)
		}
		return a, orErr(nil, sub)

	case "BootstrapMethods":
		n := int(sub.r.u2())
		a := &BootstrapMethods{Methods: make([]BootstrapMethod, 0, n)}
		for i := 0; i < n && !sub.r.done(); i++ {
			h, err := d.pool.Handle(sub.r.u2(), d.version)
			if err != nil {
				return nil, err
			}
			argc := int(sub.r.u2())
			args := make([]uint16, 0, argc)
			for j := 0; j < argc && !sub.r.done(); j++ {
				args = append(args, sub.r.u2())
			}
			a.Methods = append(a.Methods, BootstrapMethod{Method: h, Arguments: args})
		}
		return a, orErr(nil, sub)

	case "MethodParameters":
		n := int(sub.r.u1()) // note: u1, not u2
		a := &MethodParameters{Params: make([]MethodParameter, 0, n)}
		for i := 0; i < n && !sub.r.done(); i++ {
			name, err := sub.optUTF8(sub.r.u2())
			if err != nil {
				return nil, err
			}
			a.Params = append(a.Params, MethodParameter{Name: name, Flags: Flags(sub.r.u2())})
		}
		return a, orErr(nil, sub)

	case "Record":
		n := int(sub.r.u2())
		a := &Record{Components: make([]RecordComponent, 0, n)}
		for i := 0; i < n && !sub.r.done(); i++ {
			var rc RecordComponent
			var err error
			if rc.Name, err = sub.utf8(sub.r.u2()); err != nil {
				return nil, err
			}
			if rc.Descriptor, err = sub.utf8(sub.r.u2()); err != nil {
				return nil, err
			}
			if rc.Attrs, err = sub.attributes(locRecord); err != nil {
				return nil, err
			}
			if s, ok := rc.Attrs.Find("Signature").(*Signature); ok {
				rc.Signature = s.Value
			}
			a.Components = append(a.Components, rc)
		}
		return a, orErr(nil, sub)

	case "StackMapTable":
		return &StackMapTable{Data: body}, nil

	case "LineNumberTable":
		if d.mode&SkipDebug != 0 {
			return nil, nil
		}
		n := int(sub.r.u2())
		a := &LineNumberTable{Lines: make([]LineNumber, 0, n)}
		for i := 0; i < n && !sub.r.done(); i++ {
			a.Lines = append(a.Lines, LineNumber{StartPC: sub.r.u2(), Line: sub.r.u2()})
		}
		return a, orErr(nil, sub)

	case "LocalVariableTable", "LocalVariableTypeTable":
		if d.mode&SkipDebug != 0 {
			return nil, nil
		}
		vars, err := sub.localVars()
		if err != nil {
			return nil, err
		}
		if name == "LocalVariableTable" {
			return &LocalVariableTable{Vars: vars}, orErr(nil, sub)
		}
		return &LocalVariableTypeTable{Vars: vars}, orErr(nil, sub)

	case "Code":
		if d.mode&SkipCode != 0 {
			return nil, nil
		}
		return sub.code()
	}

	// Named in the location table but not decoded here: SourceDebugExtension,
	// and the annotation and module families. Retained verbatim.
	return &Raw{Name: name, Data: body}, nil
}

func (d *decoder) classList() ([]string, error) {
	n := int(d.r.u2())
	out := make([]string, 0, n)
	for i := 0; i < n && !d.r.done(); i++ {
		name, err := d.class(d.r.u2())
		if err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, nil
}

func (d *decoder) localVars() ([]LocalVariable, error) {
	n := int(d.r.u2())
	out := make([]LocalVariable, 0, n)
	for i := 0; i < n && !d.r.done(); i++ {
		var lv LocalVariable
		lv.StartPC = d.r.u2()
		lv.Length = d.r.u2()
		var err error
		if lv.Name, err = d.utf8(d.r.u2()); err != nil {
			return nil, err
		}
		if lv.Descriptor, err = d.utf8(d.r.u2()); err != nil {
			return nil, err
		}
		lv.Slot = d.r.u2()
		out = append(out, lv)
	}
	return out, nil
}

// code decodes a Code attribute body. Note the widths: max_stack and
// max_locals are u2 and code_length is u4. The first edition of the
// specification used u1/u1/u2, and that version is still widely mirrored.
func (d *decoder) code() (*Code, error) {
	c := &Code{}
	c.MaxStack = d.r.u2()
	c.MaxLocals = d.r.u2()

	length := d.r.u4()
	if length == 0 {
		return nil, fmt.Errorf("%s: code_length is 0 (a Code attribute must hold at least one instruction)", d.name())
	}
	if length >= 65536 {
		// §4.9.1: a method whose code array exceeds 65535 bytes is invalid,
		// and its branch offsets could not address it in any case.
		return nil, fmt.Errorf("%s: code_length %d exceeds the 65535-byte limit", d.name(), length)
	}
	c.Bytes = d.r.bytes(int(length))

	n := int(d.r.u2())
	c.Handlers = make([]ExceptionHandler, 0, n)
	for i := 0; i < n && !d.r.done(); i++ {
		var h ExceptionHandler
		h.StartPC = d.r.u2()
		h.EndPC = d.r.u2()
		h.HandlerPC = d.r.u2()
		// catch_type 0 means "catch everything", which is how finally compiles.
		var err error
		if h.CatchType, err = d.optClass(d.r.u2()); err != nil {
			return nil, err
		}
		c.Handlers = append(c.Handlers, h)
	}

	var err error
	if c.Attrs, err = d.attributes(locCode); err != nil {
		return nil, err
	}
	for _, a := range c.Attrs {
		switch t := a.(type) {
		case *LineNumberTable:
			c.LineNumbers = t
		case *StackMapTable:
			c.StackMap = t
		}
	}
	return c, orErr(nil, d)
}

// orErr prefers an explicit error, then the sub-reader's sticky error, then
// the leftovers. An attribute body whose declared length exceeds what its own
// structure accounts for is malformed: nothing in §4.7 permits slack inside a
// predefined attribute, and accepting it hides a misparse that would otherwise
// surface here rather than three attributes later.
func orErr(err error, sub *decoder) error {
	if err != nil {
		return err
	}
	if sub.r.err != nil {
		return sub.r.err
	}
	if n := len(sub.r.b) - sub.r.off; n != 0 {
		return &SyntaxError{File: sub.r.file, Off: sub.r.off,
			Msg: fmt.Sprintf("%d bytes left over after the attribute body", n)}
	}
	return nil
}