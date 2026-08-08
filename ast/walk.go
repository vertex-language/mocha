package ast

import "reflect"

// Visitor is the callback protocol for Walk. Visit returns the visitor to use
// for n's children, or nil to skip them.
type Visitor interface {
	Visit(n Node) Visitor
}

// Walk traverses n in source order, depth first. Children are discovered by
// reflection over exported fields, so a node that gains a field is traversed
// without this file changing — the trade is a slower walk than a generated one.
// If tree traversal shows up in a profile, generate the switch from the node
// declarations and keep this as the reference implementation.
//
// Field order in the struct declarations is source order, so traversal is too.
func Walk(v Visitor, n Node) {
	if v == nil || n == nil || isNilNode(n) {
		return
	}
	if v = v.Visit(n); v == nil {
		return
	}
	walkChildren(v, reflect.ValueOf(n))
	v.Visit(nil) // signal end of this subtree
}

var nodeType = reflect.TypeOf((*Node)(nil)).Elem()

func walkChildren(v Visitor, val reflect.Value) {
	if val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return
	}
	t := val.Type()
	for i := 0; i < val.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		walkValue(v, val.Field(i))
	}
}

func walkValue(v Visitor, val reflect.Value) {
	switch val.Kind() {
	case reflect.Ptr, reflect.Interface:
		if val.IsNil() {
			return
		}
		if val.Type().Implements(nodeType) {
			if n, ok := val.Interface().(Node); ok {
				Walk(v, n)
			}
			return
		}
		// A Releaser or another non-Node interface: not part of the tree.
	case reflect.Slice:
		for i := 0; i < val.Len(); i++ {
			walkValue(v, val.Index(i))
		}
	}
}

// isNilNode reports whether n is a typed nil, which a plain n == nil misses.
func isNilNode(n Node) bool {
	v := reflect.ValueOf(n)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

type inspector func(Node) bool

func (f inspector) Visit(n Node) Visitor {
	if n != nil && f(n) {
		return f
	}
	return nil
}

// Inspect walks n, calling f for each node. f returns false to skip a subtree.
// Unlike Walk, f is never called with a nil node.
func Inspect(n Node, f func(Node) bool) { Walk(inspector(f), n) }