package ext

import (
	"testing"

	"github.com/avmnu-sng/quill-template-engine/errors"
	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// --- test doubles -----------------------------------------------------------

// jsonStringObj is a host Object that renders through a Stringify hook, so json
// and ToText-based paths serialize it as the returned string literal.
type jsonStringObj struct{ s string }

func (*jsonStringObj) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }
func (*jsonStringObj) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), nil
}
func (o *jsonStringObj) Stringify() (string, error) { return o.s, nil }

// jsonOpaqueObj is a host Object with NO Stringify hook, so ToText fails on it
// and json serialization propagates a render error (spec 03 Section 2.6).
type jsonOpaqueObj struct{}

func (*jsonOpaqueObj) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }
func (*jsonOpaqueObj) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), nil
}

// countObj reports a fixed Count, exercising the Counter arm of |length.
type countObj struct{ n int }

func (*countObj) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }
func (*countObj) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), nil
}
func (o *countObj) Count() int { return o.n }

// attrObj is a host Object with one readable field and one callable method,
// backing attribute(obj, name) and attribute(obj, name, args).
type attrObj struct{}

func (*attrObj) GetField(name string) (runtime.Value, bool) {
	if name == "title" {
		return runtime.Str("Quill"), true
	}
	return runtime.Null(), false
}

func (*attrObj) CallMethod(name string, args []runtime.Value) (runtime.Value, error) {
	if name == "greet" {
		who := "world"
		if len(args) > 0 {
			who = args[0].S
		}
		return runtime.Str("hi " + who), nil
	}
	return runtime.Null(), errors.New(errors.KindAttribute, "no method %q", name)
}

// mapWith builds a string-keyed *Array in the given insertion order, so key
// ordering in json and keys is deterministic and assertable.
func mapWith(pairs ...[2]string) runtime.Value {
	a := runtime.NewArray()
	for _, p := range pairs {
		a.SetStr(p[0], runtime.Str(p[1]))
	}
	return runtime.Arr(a)
}

// --- json filter: encodeJSON / encodeJSONString -----------------------------

// TestJSONScalars pins the exact serialization of every scalar Kind through the
// json filter (spec 03 Section 2.6).
func TestJSONScalars(t *testing.T) {
	cases := []struct {
		name string
		in   runtime.Value
		want string
	}{
		{"null", runtime.Null(), "null"},
		{"true", runtime.Bool(true), "true"},
		{"false", runtime.Bool(false), "false"},
		{"int", runtime.Int(42), "42"},
		{"negative int", runtime.Int(-7), "-7"},
		{"float", runtime.Float(1.5), "1.5"},
		{"plain string", runtime.Str("abc"), `"abc"`},
		{"safe string", runtime.Safe("abc"), `"abc"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := callFilter(t, "json", c.in); got.S != c.want {
				t.Errorf("json(%s) = %q, want %q", c.name, got.S, c.want)
			}
		})
	}
}

// TestJSONStringEscaping drives encodeJSONString through each mandatory escape,
// the \u00XX control-character path, and confirms HTML metacharacters and slash
// are emitted literally (the two deliberate divergences from Go's json.Marshal,
// spec 03 Section 2.6).
func TestJSONStringEscaping(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"quote", "a\"b", `"a\"b"`},
		{"backslash", "a\\b", `"a\\b"`},
		{"newline", "a\nb", `"a\nb"`},
		{"carriage return", "a\rb", `"a\rb"`},
		{"tab", "a\tb", `"a\tb"`},
		{"backspace", "a\bb", `"a\bb"`},
		{"formfeed", "a\fb", `"a\fb"`},
		{"control char u0001", "a\x01b", `"a\u0001b"`},
		{"control char u001f", "\x1f", `"\u001f"`},
		{"html not escaped", "<a> & </a>", `"<a> & </a>"`},
		{"slash not escaped", "a/b", `"a/b"`},
		{"unicode kept literal", "café", "\"café\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := callFilter(t, "json", runtime.Str(c.in))
			if got.S != c.want {
				t.Errorf("json(%q) = %q, want %q", c.in, got.S, c.want)
			}
		})
	}
}

// TestJSONCollections pins list, empty-array, nested and object (map) output,
// including deterministic insertion-order keys.
func TestJSONCollections(t *testing.T) {
	empty := callFilter(t, "json", runtime.Arr(runtime.NewArray()))
	if empty.S != "[]" {
		t.Errorf("empty array json = %q, want []", empty.S)
	}

	// A nil *Array is also list-shaped and encodes as [].
	nilArr := callFilter(t, "json", runtime.Arr(nil))
	if nilArr.S != "[]" {
		t.Errorf("nil array json = %q, want []", nilArr.S)
	}

	l := callFilter(t, "json", list(runtime.Int(1), runtime.Str("x"), runtime.Bool(true)))
	if l.S != `[1,"x",true]` {
		t.Errorf("list json = %q", l.S)
	}

	// Map keys emit in insertion order (b before a), NOT sorted.
	m := callFilter(t, "json", mapWith([2]string{"b", "1"}, [2]string{"a", "2"}))
	if m.S != `{"b":"1","a":"2"}` {
		t.Errorf("map json = %q, want insertion-ordered", m.S)
	}

	// Nested: a list holding a map holding a list.
	inner := runtime.NewArray()
	inner.SetStr("nums", list(runtime.Int(1), runtime.Int(2)))
	nested := callFilter(t, "json", list(runtime.Arr(inner)))
	if nested.S != `[{"nums":[1,2]}]` {
		t.Errorf("nested json = %q", nested.S)
	}
}

// TestJSONPretty exercises the indented form (pretty=true) with the default and
// a custom indent, so the newline/indent branches of encodeJSONArray are hit.
func TestJSONPretty(t *testing.T) {
	got := callFilter(t, "json", list(runtime.Int(1), runtime.Int(2)), runtime.Bool(true))
	want := "[\n  1,\n  2\n]"
	if got.S != want {
		t.Errorf("pretty list = %q, want %q", got.S, want)
	}

	m := callFilter(t, "json", mapWith([2]string{"k", "v"}), runtime.Bool(true), runtime.Str("\t"))
	wantMap := "{\n\t\"k\": \"v\"\n}"
	if m.S != wantMap {
		t.Errorf("pretty map custom indent = %q, want %q", m.S, wantMap)
	}
}

// TestJSONObjectStringify serializes a host Object with a Stringify hook as its
// quoted string, and a hook-less Object surfaces a render error (spec 03 2.6).
func TestJSONObjectStringify(t *testing.T) {
	got := callFilter(t, "json", runtime.Obj(&jsonStringObj{s: "a\"b"}))
	if got.S != `"a\"b"` {
		t.Errorf("json(stringify obj) = %q, want %q", got.S, `"a\"b"`)
	}

	s := Core()
	f, ok := s.Filter("json")
	if !ok {
		t.Fatal("json filter not registered")
	}
	// A top-level hook-less object surfaces a KindRender error (encodeJSON's
	// KObject arm -> ToText with no stringify hook).
	_, err := f.Fn([]runtime.Value{runtime.Obj(&jsonOpaqueObj{})})
	if err == nil {
		t.Fatal("json of a hook-less object must error")
	}
	if errors.KindOf(err) != errors.KindRender {
		t.Errorf("top-level obj error kind = %v, want KindRender", errors.KindOf(err))
	}

	// The render error must also propagate when the hook-less object is NESTED
	// inside a collection: encodeJSONArray forwards the child encodeJSON error
	// rather than swallowing it or emitting a partial document. This exercises
	// the list-value and map-value error arms of encodeJSONArray, which the
	// top-level case above does not reach.
	if _, err := f.Fn([]runtime.Value{list(runtime.Obj(&jsonOpaqueObj{}))}); err == nil {
		t.Fatal("json of a list holding a hook-less object must error")
	} else if errors.KindOf(err) != errors.KindRender {
		t.Errorf("nested-in-list error kind = %v, want KindRender", errors.KindOf(err))
	}

	mapWithObj := runtime.NewArray()
	mapWithObj.SetStr("bad", runtime.Obj(&jsonOpaqueObj{}))
	if _, err := f.Fn([]runtime.Value{runtime.Arr(mapWithObj)}); err == nil {
		t.Fatal("json of a map holding a hook-less object must error")
	} else if errors.KindOf(err) != errors.KindRender {
		t.Errorf("nested-in-map error kind = %v, want KindRender", errors.KindOf(err))
	}
}

// TestJSONInvalidIndentArg confirms an unrenderable indent argument is an error
// (the wantString/ToText failure arm of filterJSON). A hook-less object cannot
// render to text, so passing it as the indent surfaces that error.
func TestJSONInvalidIndentArg(t *testing.T) {
	s := Core()
	f, _ := s.Filter("json")
	_, err := f.Fn([]runtime.Value{list(runtime.Int(1)), runtime.Bool(true), runtime.Obj(&jsonOpaqueObj{})})
	if err == nil {
		t.Fatal("unrenderable indent argument must error")
	}
	if errors.KindOf(err) != errors.KindRender {
		t.Errorf("indent-arg error kind = %v, want KindRender", errors.KindOf(err))
	}
}

// --- attribute() function: fnAttribute --------------------------------------

// TestAttributePresentField reads an existing field off a host Object.
func TestAttributePresentField(t *testing.T) {
	got := callFn(t, "attribute", runtime.Obj(&attrObj{}), runtime.Str("title"))
	if got.Kind != runtime.KStr || got.S != "Quill" {
		t.Errorf("attribute(obj,'title') = %+v, want Str Quill", got)
	}
}

// TestAttributeMissingField confirms a missing member resolves to null (not an
// error): fnAttribute reads with allowAbsent=true (spec 03 Section 3.2).
func TestAttributeMissingField(t *testing.T) {
	got := callFn(t, "attribute", runtime.Obj(&attrObj{}), runtime.Str("nope"))
	if !got.IsNull() {
		t.Errorf("attribute(obj,'nope') = %+v, want null", got)
	}

	// A missing key on a map also yields null under suppression.
	m := mapWith([2]string{"x", "1"})
	if got := callFn(t, "attribute", m, runtime.Str("y")); !got.IsNull() {
		t.Errorf("attribute(map,'y') = %+v, want null", got)
	}
}

// TestAttributePresentMapKey reads an existing map key by dotted access.
func TestAttributePresentMapKey(t *testing.T) {
	m := mapWith([2]string{"x", "1"}, [2]string{"y", "2"})
	got := callFn(t, "attribute", m, runtime.Str("y"))
	if got.Kind != runtime.KStr || got.S != "2" {
		t.Errorf("attribute(map,'y') = %+v, want Str 2", got)
	}
}

// TestAttributeMethodCall invokes a method via the three-arg form, with and
// without call arguments (spec 03 Section 3.2).
func TestAttributeMethodCall(t *testing.T) {
	got := callFn(t, "attribute", runtime.Obj(&attrObj{}), runtime.Str("greet"),
		list(runtime.Str("bob")))
	if got.Kind != runtime.KStr || got.S != "hi bob" {
		t.Errorf("attribute method call = %+v, want Str 'hi bob'", got)
	}

	// Empty (non-null but empty) args array still dispatches the call.
	got = callFn(t, "attribute", runtime.Obj(&attrObj{}), runtime.Str("greet"),
		runtime.Arr(runtime.NewArray()))
	if got.Kind != runtime.KStr || got.S != "hi world" {
		t.Errorf("attribute method call no args = %+v, want Str 'hi world'", got)
	}

	// A NULL third argument is NOT a method call: fnAttribute's guard is
	// `len(args) > 2 && !args[2].IsNull()`, so a null args slot degrades to a
	// plain field read. attribute(obj, "title", null) must therefore return the
	// field value, not attempt to call a "title" method (which attrObj lacks and
	// would error). This pins the guard's second conjunct.
	got = callFn(t, "attribute", runtime.Obj(&attrObj{}), runtime.Str("title"), runtime.Null())
	if got.Kind != runtime.KStr || got.S != "Quill" {
		t.Errorf("attribute(obj,'title',null) = %+v, want field read Str Quill", got)
	}
}

// TestAttributeMethodCallOnNonObject confirms the call form errors (KindAttribute)
// when the receiver is not an object.
func TestAttributeMethodCallOnNonObject(t *testing.T) {
	s := Core()
	f, _ := s.Function("attribute")
	_, err := f.Fn([]runtime.Value{runtime.Str("scalar"), runtime.Str("m"), list(runtime.Int(1))})
	if err == nil {
		t.Fatal("attribute() method call on a non-object must error")
	}
	if errors.KindOf(err) != errors.KindAttribute {
		t.Errorf("error kind = %v, want KindAttribute", errors.KindOf(err))
	}
}

// TestAttributeMemberOnScalar confirms dotted member access on a plain string
// (a two-arg call) surfaces a KindAttribute error, not a null.
func TestAttributeMemberOnScalar(t *testing.T) {
	s := Core()
	f, _ := s.Function("attribute")
	_, err := f.Fn([]runtime.Value{runtime.Str("scalar"), runtime.Str("len")})
	if err == nil {
		t.Fatal("attribute() member on a string must error")
	}
	if errors.KindOf(err) != errors.KindAttribute {
		t.Errorf("error kind = %v, want KindAttribute", errors.KindOf(err))
	}
}

// --- len()/keys() function aliases: filterLength / filterKeys ---------------

// TestLenFunctionAlias drives the len() function (filterLength) across a string,
// a list, a map, a Counter object, null, and a bare scalar (spec 03 Section 3.4).
func TestLenFunctionAlias(t *testing.T) {
	cases := []struct {
		name string
		in   runtime.Value
		want int64
	}{
		{"string runes", runtime.Str("héllo"), 5},
		{"list", list(runtime.Int(1), runtime.Int(2), runtime.Int(3)), 3},
		{"map", mapWith([2]string{"a", "1"}, [2]string{"b", "2"}), 2},
		{"empty array", runtime.Arr(runtime.NewArray()), 0},
		{"nil array", runtime.Arr(nil), 0},
		{"counter object", runtime.Obj(&countObj{n: 7}), 7},
		{"plain object", runtime.Obj(&jsonOpaqueObj{}), 1},
		{"null", runtime.Null(), 0},
		{"scalar int", runtime.Int(99), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := callFn(t, "len", c.in)
			if got.Kind != runtime.KInt || got.I != c.want {
				t.Errorf("len(%s) = %+v, want %d", c.name, got, c.want)
			}
		})
	}
}

// TestKeysFunctionAlias drives the keys() function (filterKeys): a map yields its
// keys in insertion order; a list yields its integer indices; a non-collection
// yields an empty list (spec 03 Section 3.4).
func TestKeysFunctionAlias(t *testing.T) {
	m := mapWith([2]string{"b", "1"}, [2]string{"a", "2"}, [2]string{"c", "3"})
	keys := callFn(t, "keys", m)
	if keys.Kind != runtime.KArray || keys.Arr.Len() != 3 {
		t.Fatalf("keys(map) shape = %+v", keys)
	}
	gotOrder := []string{
		keys.Arr.Pairs()[0].Val.S,
		keys.Arr.Pairs()[1].Val.S,
		keys.Arr.Pairs()[2].Val.S,
	}
	if gotOrder[0] != "b" || gotOrder[1] != "a" || gotOrder[2] != "c" {
		t.Errorf("keys order = %v, want [b a c] (insertion order)", gotOrder)
	}

	// A list's keys are its 0-based integer indices.
	lk := callFn(t, "keys", list(runtime.Str("x"), runtime.Str("y")))
	if lk.Arr.Len() != 2 || lk.Arr.Pairs()[0].Val.Kind != runtime.KInt ||
		lk.Arr.Pairs()[0].Val.I != 0 || lk.Arr.Pairs()[1].Val.I != 1 {
		t.Errorf("keys(list) = %+v, want [0 1]", lk.Arr.Pairs())
	}

	// A non-collection (scalar) yields an empty list.
	sk := callFn(t, "keys", runtime.Int(5))
	if sk.Kind != runtime.KArray || sk.Arr.Len() != 0 {
		t.Errorf("keys(scalar) = %+v, want empty list", sk)
	}
}
