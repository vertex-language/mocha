package classfile

// An Attr is one attribute_info structure. Attributes this package does not
// model appear as *Raw, which is required rather than merely convenient: the
// specification obliges an implementation to ignore attributes it does not
// recognise, and non-predefined attributes may not affect semantics.
type Attr interface {
	AttrName() string
}

// Raw is an attribute retained verbatim. Data aliases the class file bytes.
type Raw struct {
	Name string
	Data []byte
}

func (a *Raw) AttrName() string { return a.Name }

// Attrs is an attribute table.
type Attrs []Attr

// Find returns the first attribute with the given name, or nil.
func (as Attrs) Find(name string) Attr {
	for _, a := range as {
		if a.AttrName() == name {
			return a
		}
	}
	return nil
}

// Raws returns every unmodelled attribute, in file order.
func (as Attrs) Raws() []*Raw {
	var out []*Raw
	for _, a := range as {
		if r, ok := a.(*Raw); ok {
			out = append(out, r)
		}
	}
	return out
}

// location is where an attribute table sits, which determines both the legal
// attribute set and what a name means in context.
type location uint8

const (
	locClass location = 1 << iota
	locField
	locMethod
	locCode
	locRecord
)

// where records the locations at which each predefined attribute may appear,
// and the first major version defining it (JVMS Tables 4.7-A and 4.7-C).
var where = map[string]struct {
	at    location
	since uint16
}{
	"ConstantValue":                        {locField, Java1_0},
	"Code":                                 {locMethod, Java1_0},
	"StackMapTable":                        {locCode, Java6},
	"Exceptions":                           {locMethod, Java1_0},
	"InnerClasses":                         {locClass, Java1_0},
	"EnclosingMethod":                      {locClass, Java5},
	"Synthetic":                            {locClass | locField | locMethod, Java1_0},
	"Signature":                            {locClass | locField | locMethod | locRecord, Java5},
	"SourceFile":                           {locClass, Java1_0},
	"SourceDebugExtension":                 {locClass, Java5},
	"LineNumberTable":                      {locCode, Java1_0},
	"LocalVariableTable":                   {locCode, Java1_0},
	"LocalVariableTypeTable":               {locCode, Java5},
	"Deprecated":                           {locClass | locField | locMethod, Java1_0},
	"RuntimeVisibleAnnotations":            {locClass | locField | locMethod | locRecord, Java5},
	"RuntimeInvisibleAnnotations":          {locClass | locField | locMethod | locRecord, Java5},
	"RuntimeVisibleParameterAnnotations":   {locMethod, Java5},
	"RuntimeInvisibleParameterAnnotations": {locMethod, Java5},
	"RuntimeVisibleTypeAnnotations":        {locClass | locField | locMethod | locCode | locRecord, Java8},
	"RuntimeInvisibleTypeAnnotations":      {locClass | locField | locMethod | locCode | locRecord, Java8},
	"AnnotationDefault":                    {locMethod, Java5},
	"BootstrapMethods":                     {locClass, Java7},
	"MethodParameters":                     {locMethod, Java8},
	"Module":                               {locClass, Java9},
	"ModulePackages":                       {locClass, Java9},
	"ModuleMainClass":                      {locClass, Java9},
	"NestHost":                             {locClass, Java11},
	"NestMembers":                          {locClass, Java11},
	"Record":                               {locClass, Java16},
	"PermittedSubclasses":                  {locClass, Java17},
}