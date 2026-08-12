package flow

// bits is a dense bitset over one method's locals.
//
// Definite assignment runs a fixpoint over loop bodies, so the state is copied
// and merged constantly. A map would allocate on every copy; a slice of words
// does not, and a method with more than sixty-four locals is rare enough that
// the growth path is never hot.
type bits struct {
	w []uint64
}

func newBits(n int) bits {
	if n == 0 {
		return bits{}
	}
	return bits{w: make([]uint64, (n+63)/64)}
}

func (b bits) clone() bits {
	if len(b.w) == 0 {
		return bits{}
	}
	w := make([]uint64, len(b.w))
	copy(w, b.w)
	return bits{w: w}
}

func (b *bits) grow(n int) {
	need := (n + 64) / 64
	for len(b.w) < need {
		b.w = append(b.w, 0)
	}
}

func (b *bits) set(i int) {
	if i < 0 {
		return
	}
	b.grow(i)
	b.w[i/64] |= 1 << uint(i%64)
}

func (b *bits) clear(i int) {
	if i < 0 || i/64 >= len(b.w) {
		return
	}
	b.w[i/64] &^= 1 << uint(i%64)
}

func (b bits) has(i int) bool {
	if i < 0 || i/64 >= len(b.w) {
		return false
	}
	return b.w[i/64]&(1<<uint(i%64)) != 0
}

// and is the merge at a join point: a variable is definitely assigned after an
// if-else only if it was assigned on both paths. Intersection, not union — the
// direction that makes the analysis conservative in the safe direction.
func (b bits) and(o bits) bits {
	n := len(b.w)
	if len(o.w) < n {
		n = len(o.w)
	}
	out := bits{w: make([]uint64, n)}
	for i := 0; i < n; i++ {
		out.w[i] = b.w[i] & o.w[i]
	}
	return out
}

// or is the merge for definite unassignment across paths where one is
// unreachable, and for accumulating what a loop body may have assigned.
func (b bits) or(o bits) bits {
	long, short := b, o
	if len(short.w) > len(long.w) {
		long, short = short, long
	}
	out := long.clone()
	for i := range short.w {
		out.w[i] |= short.w[i]
	}
	return out
}

func (b bits) equal(o bits) bool {
	n := len(b.w)
	if len(o.w) > n {
		n = len(o.w)
	}
	for i := 0; i < n; i++ {
		if b.word(i) != o.word(i) {
			return false
		}
	}
	return true
}

func (b bits) word(i int) uint64 {
	if i >= len(b.w) {
		return 0
	}
	return b.w[i]
}

// state is the pair §16 actually tracks. They are not complements: a variable
// may be neither definitely assigned nor definitely unassigned, and that state
// is precisely what makes both a read of it and a second assignment to a blank
// final an error.
type state struct {
	da bits // definitely assigned
	du bits // definitely unassigned
}

func (cx *ctx) newState() state {
	n := len(cx.order)
	st := state{da: newBits(n), du: newBits(n)}
	// Every blank final starts definitely unassigned; a parameter starts
	// definitely assigned.
	for i := range cx.order {
		if cx.blanks[i] {
			st.du.set(i)
		} else {
			st.da.set(i)
		}
	}
	return st
}

func (s state) clone() state {
	return state{da: s.da.clone(), du: s.du.clone()}
}

// join merges two paths that both reach a point.
func join(a, b state) state {
	return state{da: a.da.and(b.da), du: a.du.and(b.du)}
}

func (s state) equal(o state) bool {
	return s.da.equal(o.da) && s.du.equal(o.du)
}

// assign records a write: the variable becomes definitely assigned and stops
// being definitely unassigned.
func (s *state) assign(i int) {
	s.da.set(i)
	s.du.clear(i)
}

// cond is the two-state result of a boolean expression. §16.1 makes the true
// and false branches carry different assignment facts, which is what lets
// `if (c && (x = f()) > 0) use(x);` compile.
type cond struct {
	whenTrue  state
	whenFalse state
}

// unconditional is the cond for an expression whose value does not depend on
// an assignment: both branches carry the same facts.
func unconditional(s state) cond {
	return cond{whenTrue: s.clone(), whenFalse: s.clone()}
}

// merge collapses a cond back to one state, for a context that does not care
// which way the condition went.
func (c cond) merge() state { return join(c.whenTrue, c.whenFalse) }