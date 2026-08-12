package sym

// Scope maps simple names to the symbols declared under them.
//
// Java has three declaration spaces (§6.5): types, variables and methods. A
// field, a method and a member class may all be called `run` in one class, so
// a conflict is only a conflict within one space — which is why Enter takes the
// namespace into account rather than rejecting every repeat.
//
// Overloads are not a conflict here. Two methods sharing a name are ordinary;
// two sharing a name and an erased signature are an error, and detecting that
// needs erasure, which needs types. attr reports it.
type Scope struct {
	// Owner is the class, method or package whose declarations these are.
	Owner  Symbol
	Parent *Scope

	tab   map[string][]Symbol
	order []Symbol
}

// NewScope returns an empty scope nested inside parent, which may be nil.
func NewScope(owner Symbol, parent *Scope) *Scope {
	return &Scope{Owner: owner, Parent: parent, tab: make(map[string][]Symbol)}
}

// Enter declares n. It returns the symbol already in this scope that n
// conflicts with, or nil on success. An unnamed variable is accepted and not
// indexed, so `_` never shadows and never collides.
func (s *Scope) Enter(n Symbol) Symbol {
	b := n.Base()
	if b.Name == "_" {
		s.order = append(s.order, n)
		return nil
	}
	if prev := s.conflict(b); prev != nil {
		return prev
	}
	s.tab[b.Name] = append(s.tab[b.Name], n)
	s.order = append(s.order, n)
	return nil
}

func (s *Scope) conflict(b *Sym) Symbol {
	ns := b.Kind.namespace()
	for _, p := range s.tab[b.Name] {
		pb := p.Base()
		if pb.Kind.namespace() != ns {
			continue
		}
		if ns == nsMethod {
			continue // an overload until erasure says otherwise
		}
		return p
	}
	return nil
}

// Lookup returns every symbol declared under name in this scope only, in
// declaration order.
func (s *Scope) Lookup(name string) []Symbol {
	if s == nil {
		return nil
	}
	return s.tab[name]
}

// LookupKind returns the first symbol of the given kind declared under name in
// this scope, or nil.
func (s *Scope) LookupKind(name string, k Kind) Symbol {
	for _, x := range s.Lookup(name) {
		if x.Base().Kind == k {
			return x
		}
	}
	return nil
}

// Resolve walks outward through enclosing scopes and returns the first scope
// that declares name, together with its symbols. Shadowing falls out of the
// order: the innermost declaration is found first.
func (s *Scope) Resolve(name string) (*Scope, []Symbol) {
	for c := s; c != nil; c = c.Parent {
		if got := c.tab[name]; len(got) > 0 {
			return c, got
		}
	}
	return nil, nil
}

// ResolveKind is Resolve restricted to one declaration space. A local variable
// named `x` does not hide a method named `x`, so a method lookup must not stop
// at the variable.
func (s *Scope) ResolveKind(name string, k Kind) Symbol {
	ns := k.namespace()
	for c := s; c != nil; c = c.Parent {
		for _, x := range c.tab[name] {
			if x.Base().Kind.namespace() == ns {
				return x
			}
		}
	}
	return nil
}

// Each calls f for every symbol in declaration order, stopping early if f
// returns false. Declaration order is source order, because Enter is called in
// source order.
func (s *Scope) Each(f func(Symbol) bool) {
	if s == nil {
		return
	}
	for _, x := range s.order {
		if !f(x) {
			return
		}
	}
}

// All returns every symbol in declaration order.
func (s *Scope) All() []Symbol {
	if s == nil {
		return nil
	}
	return s.order
}

// Len returns the number of symbols declared directly in this scope.
func (s *Scope) Len() int {
	if s == nil {
		return 0
	}
	return len(s.order)
}