package classpath

import "sort"

// Static is an in-memory entry: a set of class files a driver already holds.
//
// It exists for two callers. One is loose .class inputs, whose binary names
// only classfile can determine — the driver reads them, then registers them
// here, which keeps this package below classfile rather than above it. The
// other is tests, which want a path with no filesystem.
type Static struct {
	name  string
	data  map[string][]byte
	names []string
}

// NewStatic returns an entry serving the given classes. The map is not copied.
func NewStatic(name string, classes map[string][]byte) *Static {
	s := &Static{name: name, data: classes}
	for n := range classes {
		s.names = append(s.names, n)
	}
	sort.Strings(s.names)
	return s
}

func (s *Static) Kind() Kind          { return KindDir }
func (s *Static) Container() string   { return s.name }
func (s *Static) has(binary string) bool { _, ok := s.data[binary]; return ok }

func (s *Static) Class(binary string) (*Class, error) {
	b, ok := s.data[binary]
	if !ok {
		return nil, &NotFoundError{Binary: binary, Container: s.name}
	}
	return &Class{
		Binary: binary,
		Data:   append([]byte(nil), b...),
		Origin: Origin{Kind: KindDir, Container: s.name, Entry: entryName(binary)},
	}, nil
}

func (s *Static) Names() ([]string, error) { return s.names, nil }
func (s *Static) Close() error             { return nil }