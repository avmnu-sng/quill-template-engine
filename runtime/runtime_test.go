package runtime

// Shared test fixtures: small host Object implementations exercising the
// optional capability interfaces (GetField/CallMethod, Stringify, Index,
// Equal, Iterate, ClassName).

// fieldObj implements GetField/CallMethod plus optional ClassName, used to
// exercise dotted access, the no-stringify-hook render error, and identity
// equality.
type fieldObj struct {
	fields map[string]Value
	class  string
}

func newFieldObj(class string, fields map[string]Value) *fieldObj {
	return &fieldObj{fields: fields, class: class}
}

func (o *fieldObj) GetField(name string) (Value, bool) {
	v, ok := o.fields[name]
	return v, ok
}

func (o *fieldObj) CallMethod(name string, args []Value) (Value, error) {
	return Null(), nil
}

func (o *fieldObj) ClassName() string { return o.class }

// stringyObj adds a Stringify hook.
type stringyObj struct {
	*fieldObj
	text string
}

func (o *stringyObj) Stringify() (string, error) { return o.text, nil }

// indexObj adds the host index interface for a[k] subscripting.
type indexObj struct {
	*fieldObj
	byKey map[string]Value
}

func (o *indexObj) GetIndex(key Value) (Value, bool) {
	s, _ := ToText(key)
	v, ok := o.byKey[s]
	return v, ok
}

// iterObj adds host iteration.
type iterObj struct {
	*fieldObj
	pairs []Pair
}

func (o *iterObj) Iterate() []Pair { return o.pairs }

// equalObj overrides identity equality with a value hook keyed on id.
type equalObj struct {
	*fieldObj
	id int
}

func (o *equalObj) Equal(other Value) bool {
	if other.Kind != KObject {
		return false
	}
	e, ok := other.Obj.(*equalObj)
	return ok && e.id == o.id
}
