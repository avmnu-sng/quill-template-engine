package ext

import (
	"strconv"
	"strings"
	"time"

	"github.com/avmnu-sng/quill-template-engine/pkg/errors"
	"github.com/avmnu-sng/quill-template-engine/pkg/runtime"
)

// dateValue is the host Object that carries a time.Time through the engine so
// the date family (the date function, the date filter, date_modify) can pass
// dates as first-class values (spec 03 Sections 2.4, 3.2). It uses the Go date
// model: a time.Time and a location. Stringify renders the
// default layout so a bare {{ someDate }} has a sensible spelling.
type dateValue struct {
	t time.Time
}

const defaultDateLayout = "2006-01-02 15:04:05"

// GetField reports no fields: a date exposes no attributes, so it always
// returns (null, false).
func (d *dateValue) GetField(string) (runtime.Value, bool) { return runtime.Null(), false }

// CallMethod always fails: a date has no callable methods, so any invocation
// returns a runtime error.
func (d *dateValue) CallMethod(string, []runtime.Value) (runtime.Value, error) {
	return runtime.Null(), errors.New(errors.KindRuntime, "a date has no methods")
}

// Stringify renders the date in the default Go reference layout (spec 03 Section
// 2.6: Go reference layout).
func (d *dateValue) Stringify() (string, error) {
	return d.t.Format(defaultDateLayout), nil
}

// ClassName returns "Date", the host type name reported for a date value.
func (d *dateValue) ClassName() string { return "Date" }

// fnDate constructs a date value from a string, a Unix timestamp, or another
// date, in an optional timezone (spec 03 Section 3.2). With no argument it is
// "now". The Go date model parses RFC3339 and the default layout; a bare integer
// is a Unix timestamp.
func fnDate(args []runtime.Value) (runtime.Value, error) {
	loc := time.UTC
	if len(args) > 1 && !args[1].IsNull() {
		name, err := wantString(args[1])
		if err != nil {
			return runtime.Null(), err
		}
		l, err := time.LoadLocation(name)
		if err != nil {
			return runtime.Null(), errors.New(errors.KindRuntime, "unknown timezone %q", name)
		}
		loc = l
	}
	t, err := coerceTime(arg(args, 0), loc)
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Obj(&dateValue{t: t}), nil
}

// filterDate formats a date (or a string/timestamp coerced to one) with a Go
// reference layout (spec 03 Section 2.6 divergence). layout defaults to the
// engine default; an optional tz reinterprets the instant in that zone.
func filterDate(args []runtime.Value) (runtime.Value, error) {
	v := arg(args, 0)
	layout := defaultDateLayout
	if len(args) > 1 && !args[1].IsNull() {
		l, err := wantString(args[1])
		if err != nil {
			return runtime.Null(), err
		}
		layout = l
	}
	loc := time.UTC
	if len(args) > 2 && !args[2].IsNull() {
		name, err := wantString(args[2])
		if err != nil {
			return runtime.Null(), err
		}
		l, err := time.LoadLocation(name)
		if err != nil {
			return runtime.Null(), errors.New(errors.KindRuntime, "unknown timezone %q", name)
		}
		loc = l
	}
	t, err := coerceTime(v, loc)
	if err != nil {
		return runtime.Null(), err
	}
	if len(args) > 2 && !args[2].IsNull() {
		t = t.In(loc)
	}
	return runtime.Str(t.Format(layout)), nil
}

// filterDateModify applies a relative delta like "+1 day" / "-2 hours" to a date
// (spec 03 Section 2.4), returning a new date value. The accepted units are
// year(s)/month(s)/day(s)/hour(s)/minute(s)/second(s) and the abbreviation set
// Go's duration parser does not cover.
func filterDateModify(args []runtime.Value) (runtime.Value, error) {
	t, err := coerceTime(arg(args, 0), time.UTC)
	if err != nil {
		return runtime.Null(), err
	}
	delta, err := wantString(arg(args, 1))
	if err != nil {
		return runtime.Null(), err
	}
	nt, err := applyDelta(t, delta)
	if err != nil {
		return runtime.Null(), err
	}
	return runtime.Obj(&dateValue{t: nt}), nil
}

// coerceTime turns a Value into a time.Time: a date value passes through, an
// integer is a Unix timestamp, and a string is parsed against RFC3339 then the
// default layout then a date-only layout. Null is "now".
func coerceTime(v runtime.Value, loc *time.Location) (time.Time, error) {
	switch v.Kind() {
	case runtime.KNull:
		return time.Now().In(loc), nil
	case runtime.KObject:
		if d, ok := v.AsObject().(*dateValue); ok {
			return d.t, nil
		}
		return time.Time{}, errors.New(errors.KindRuntime, "value is not a date")
	case runtime.KInt:
		return time.Unix(v.AsInt(), 0).In(loc), nil
	case runtime.KFloat:
		return time.Unix(int64(v.AsFloat()), 0).In(loc), nil
	case runtime.KStr, runtime.KSafe:
		s := strings.TrimSpace(v.AsStr())
		if s == "" || s == "now" {
			return time.Now().In(loc), nil
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.Unix(n, 0).In(loc), nil
		}
		for _, layout := range []string{time.RFC3339, defaultDateLayout, "2006-01-02", "15:04:05"} {
			if t, err := time.ParseInLocation(layout, s, loc); err == nil {
				return t, nil
			}
		}
		return time.Time{}, errors.New(errors.KindRuntime, "cannot parse %q as a date", s)
	default:
		return time.Time{}, errors.New(errors.KindRuntime, "cannot interpret %s as a date", v.Kind())
	}
}

// applyDelta parses one or more "+N unit" / "-N unit" terms and applies them.
func applyDelta(t time.Time, delta string) (time.Time, error) {
	fields := strings.Fields(delta)
	i := 0
	for i < len(fields) {
		num, err := strconv.Atoi(fields[i])
		if err != nil {
			return time.Time{}, errors.New(errors.KindRuntime,
				"cannot parse date modifier %q (want forms like \"+1 day\")", delta)
		}
		if i+1 >= len(fields) {
			return time.Time{}, errors.New(errors.KindRuntime,
				"date modifier %q is missing a unit", delta)
		}
		unit := strings.ToLower(strings.TrimSuffix(fields[i+1], "s"))
		switch unit {
		case "year":
			t = t.AddDate(num, 0, 0)
		case "month":
			t = t.AddDate(0, num, 0)
		case "day":
			t = t.AddDate(0, 0, num)
		case "week":
			t = t.AddDate(0, 0, num*7)
		case "hour":
			t = t.Add(time.Duration(num) * time.Hour)
		case "minute", "min":
			t = t.Add(time.Duration(num) * time.Minute)
		case "second", "sec":
			t = t.Add(time.Duration(num) * time.Second)
		default:
			return time.Time{}, errors.New(errors.KindRuntime,
				"unknown date modifier unit %q", fields[i+1])
		}
		i += 2
	}
	return t, nil
}
