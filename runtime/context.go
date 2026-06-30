package runtime

// Context is Quill's lexical variable scope: an ordered set of name->Value
// bindings. block, for, macro, with, and capture each introduce a scope by
// cloning the context; the clone's edits do not leak back to the parent, which
// gives the port's save/restore semantics (spec 04 Section 8). Insertion order
// is preserved so an "available: ..." list in an undefined-variable error is
// deterministic (spec 04 Section 8.1).
type Context struct {
	order []string
	vars  map[string]Value
}

// NewContext returns an empty Context.
func NewContext() *Context {
	return &Context{vars: map[string]Value{}}
}

// Set binds name to v, recording first-seen order. Re-setting an existing name
// updates the value and keeps its original position.
func (c *Context) Set(name string, v Value) {
	if _, ok := c.vars[name]; !ok {
		c.order = append(c.order, name)
	}
	c.vars[name] = v
}

// Get returns the binding for name. The bool is false when name is unbound; the
// strict-undefined policy at the access layer turns that into an error rather
// than a silent Null (spec 04 Section 8.1).
func (c *Context) Get(name string) (Value, bool) {
	v, ok := c.vars[name]
	return v, ok
}

// Has reports whether name is bound. It backs the is defined test on a bare
// identifier, which tests presence and never throws (spec 04 Section 8.3); a
// name bound to Null is present (Has true).
func (c *Context) Has(name string) bool {
	_, ok := c.vars[name]
	return ok
}

// Names returns the bound names in insertion order, for building the
// "available: ..." hint in a strict-undefined error.
func (c *Context) Names() []string {
	return append([]string(nil), c.order...)
}

// Clone returns a value-copy of the context: a fresh binding set where each
// value is itself value-copied (so a nested *Array does not alias across the
// boundary, per spec 04 Section 6.3). This is the scope-entry primitive; on
// scope exit the caller simply discards the clone and keeps the parent.
func (c *Context) Clone() *Context {
	cp := &Context{
		order: append([]string(nil), c.order...),
		vars:  make(map[string]Value, len(c.vars)),
	}
	for k, v := range c.vars {
		cp.vars[k] = CopyValue(v)
	}
	return cp
}
