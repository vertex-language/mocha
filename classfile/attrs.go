package classfile

// Modelled attributes. Annotation and module attributes are deliberately left
// as *Raw: mocha never inspects them, and the element_value and target_info
// unions are a large amount of code to maintain for no consumer. Add them the
// day something needs them.

// SourceFile names the source that produced the class.
type SourceFile struct{ Value string }

func (*SourceFile) AttrName() string { return "SourceFile" }

// Signature carries a generic type signature (JVMS §4.7.9.1). The grammar is
// not parsed here; erasure lives in the descriptor, which is what ir wants.
type Signature struct{ Value string }

func (*Signature) AttrName() string { return "Signature" }

// ConstantValue is the compile-time constant of a static final field.
type ConstantValue struct{ Value Const }

func (*ConstantValue) AttrName() string { return "ConstantValue" }

// Exceptions lists the checked exceptions a method declares, in internal form.
type Exceptions struct{ Classes []string }

func (*Exceptions) AttrName() string { return "Exceptions" }

// Synthetic marks a compiler-generated member.
type Synthetic struct{}

func (*Synthetic) AttrName() string { return "Synthetic" }

// Deprecated marks a deprecated member.
type Deprecated struct{}

func (*Deprecated) AttrName() string { return "Deprecated" }

// NestHost names the nest this class belongs to.
type NestHost struct{ Host string }

func (*NestHost) AttrName() string { return "NestHost" }

// NestMembers lists the classes belonging to a nest hosted by this class.
type NestMembers struct{ Classes []string }

func (*NestMembers) AttrName() string { return "NestMembers" }

// PermittedSubclasses lists the classes that may extend a sealed class.
type PermittedSubclasses struct{ Classes []string }

func (*PermittedSubclasses) AttrName() string { return "PermittedSubclasses" }

// InnerClass is one row of the InnerClasses table.
type InnerClass struct {
	Inner      string // internal form
	Outer      string // "" for local and anonymous classes
	SimpleName string // "" for anonymous classes
	Flags      Flags
}

// InnerClasses records the nesting relationships this class participates in.
type InnerClasses struct{ Classes []InnerClass }

func (*InnerClasses) AttrName() string { return "InnerClasses" }

// EnclosingMethod locates a local or anonymous class within its enclosing
// method. Name and Descriptor are empty when the class sits in an initialiser.
type EnclosingMethod struct {
	Class      string
	Name       string
	Descriptor string
}

func (*EnclosingMethod) AttrName() string { return "EnclosingMethod" }

// RecordComponent is one component of a record class.
type RecordComponent struct {
	Name       string
	Descriptor string
	Signature  string
	Attrs      Attrs
}

// Record marks a record class and describes its components.
type Record struct{ Components []RecordComponent }

func (*Record) AttrName() string { return "Record" }

// MethodParameter is one row of MethodParameters. Name is empty when the
// parameter is unnamed.
type MethodParameter struct {
	Name  string
	Flags Flags
}

// MethodParameters records formal parameter names and flags.
type MethodParameters struct{ Params []MethodParameter }

func (*MethodParameters) AttrName() string { return "MethodParameters" }

// BootstrapMethods is the class's bootstrap method table. The parsed entries
// are installed on the Pool, since the pool's Dynamic entries index them.
type BootstrapMethods struct{ Methods []BootstrapMethod }

func (*BootstrapMethods) AttrName() string { return "BootstrapMethods" }

// StackMapTable is retained but not decoded. mocha does not verify, and the
// frame encoding is only needed by a writer targeting class file 50 or later.
type StackMapTable struct{ Data []byte }

func (*StackMapTable) AttrName() string { return "StackMapTable" }

// LineNumber maps a bytecode offset to a source line.
type LineNumber struct {
	StartPC uint16
	Line    uint16
}

// LineNumberTable is the debug line mapping for a Code attribute.
type LineNumberTable struct{ Lines []LineNumber }

func (*LineNumberTable) AttrName() string { return "LineNumberTable" }

// LocalVariable is one row of LocalVariableTable or LocalVariableTypeTable.
// For the type table, Descriptor holds a generic signature instead.
type LocalVariable struct {
	StartPC    uint16
	Length     uint16
	Name       string
	Descriptor string
	Slot       uint16
}

// LocalVariableTable names the local variable slots of a method.
type LocalVariableTable struct{ Vars []LocalVariable }

func (*LocalVariableTable) AttrName() string { return "LocalVariableTable" }

// LocalVariableTypeTable carries generic signatures for local variables.
type LocalVariableTypeTable struct{ Vars []LocalVariable }

func (*LocalVariableTypeTable) AttrName() string { return "LocalVariableTypeTable" }

// ExceptionHandler is one row of a Code attribute's exception table. A
// CatchType of "" means the handler catches everything, which is how finally
// blocks are compiled.
type ExceptionHandler struct {
	StartPC   uint16
	EndPC     uint16
	HandlerPC uint16
	CatchType string
}

// Code is the body of a method (JVMS §4.7.3).
type Code struct {
	MaxStack  uint16
	MaxLocals uint16
	Bytes     []byte // the code array; aliases the class file
	Handlers  []ExceptionHandler
	Attrs     Attrs

	LineNumbers *LineNumberTable // nil under SkipDebug
	StackMap    *StackMapTable
}

func (*Code) AttrName() string { return "Code" }