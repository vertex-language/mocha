package lower

import (
	"github.com/vertex-language/mocha/sym"
	"github.com/vertex-language/mocha/types"
)

// slotMap assigns local variable slots for one method.
//
// Slots are assigned in pass one and read by both pass two and
// LocalVariableTable. Assigning inside the closure would survive a replay and
// break the widening fixpoint, since the second run would start from the first
// run's counter.
//
// flow already indexes locals densely per method for its bitsets, which is most
// of the answer; what this adds is the two-slot rule and scope reuse.
type slotMap struct {
	of   map[*sym.VarSym]int
	next int
	max  int
}

func newSlotMap() *slotMap {
	return &slotMap{of: make(map[*sym.VarSym]int)}
}

// reserve allocates n contiguous slots without naming them: `this`, and the
// scratch a desugaring needs.
func (s *slotMap) reserve(n int) int {
	at := s.next
	s.next += n
	if s.next > s.max {
		s.max = s.next
	}
	return at
}

// declare assigns v a slot, two wide for a long or a double.
func (s *slotMap) declare(v *sym.VarSym, t types.Type) int {
	w := types.Slots(t)
	if w < 1 {
		w = 1 // void never reaches here; a broken type still needs a slot
	}
	at := s.reserve(w)
	s.of[v] = at
	return at
}

// slot returns v's assigned slot. A miss is a bug in pass one, not a diagnostic.
func (s *slotMap) slot(v *sym.VarSym) int {
	n, ok := s.of[v]
	if !ok {
		bug("no slot assigned for %s", v.Name)
	}
	return n
}

func (s *slotMap) has(v *sym.VarSym) bool {
	_, ok := s.of[v]
	return ok
}

// mark and release bracket a scope. Slots are reused across disjoint scopes,
// which is what keeps max_locals down in a method with several sequential
// blocks; max remembers the high-water mark regardless.
func (s *slotMap) mark() int { return s.next }

func (s *slotMap) release(m int) { s.next = m }