package parser

import (
	"reflect"
	"sync"

	"github.com/vertex-language/mocha/ast"
)

// The arena batches node allocation into per-type chunks and hands the whole
// set back at once, so a whole-program build does not hold every tree it has
// ever parsed. It stays unexported: ast declares the one-method Releaser
// interface and this implements it, which costs ast no import.
//
// Release is a promise, not a check. Every node in the tree is invalid
// afterwards, and nothing here detects a caller that kept a pointer. The
// contract is the one in the docs: `defer tree.Release()` at the parse site,
// and consumers that need a node past that lifetime copy what they need.

const chunkSize = 64

type recyclable interface{ recycle() }

type chunk[T any] struct {
	buf [chunkSize]T
	n   int
}

func (c *chunk[T]) recycle() {
	var zero T
	for i := 0; i < c.n; i++ {
		c.buf[i] = zero // drop references so the GC can collect what nodes held
	}
	c.n = 0
	poolFor[T]().Put(c)
}

type arena struct {
	open map[reflect.Type]any // the chunk currently being filled, per type
	all  []recyclable
	done bool
}

func newArena() *arena {
	return &arena{open: make(map[reflect.Type]any, 32)}
}

// alloc returns a zeroed *T from the arena. It is a free function because Go
// methods cannot take type parameters.
func alloc[T any](a *arena) *T {
	if a == nil || a.done {
		return new(T) // parsing after Release, or no arena: fall back to the heap
	}
	t := reflect.TypeFor[T]()
	c, _ := a.open[t].(*chunk[T])
	if c == nil || c.n == chunkSize {
		c = poolFor[T]().Get().(*chunk[T])
		a.open[t] = c
		a.all = append(a.all, c)
	}
	p := &c.buf[c.n]
	c.n++
	return p
}

func (a *arena) Release() {
	if a == nil || a.done {
		return
	}
	a.done = true
	for _, c := range a.all {
		c.recycle()
	}
	a.all = nil
	a.open = nil
}

var pools sync.Map // reflect.Type -> *sync.Pool

func poolFor[T any]() *sync.Pool {
	t := reflect.TypeFor[T]()
	if v, ok := pools.Load(t); ok {
		return v.(*sync.Pool)
	}
	pl := &sync.Pool{New: func() any { return new(chunk[T]) }}
	v, _ := pools.LoadOrStore(t, pl)
	return v.(*sync.Pool)
}

var _ ast.Releaser = (*arena)(nil)