package rpc

import (
	"reflect"
	"testing"
	"time"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/proxy"
)

// Method drift across the api.Service triplet is caught by the compiler: every
// implementation carries a `var _ api.Service` assertion. Field drift is not.
// Add a field to a domain type, forget its line in convert.go, and it works
// perfectly under --dry-run and --state-dir while vanishing through the daemon.
// Nothing fails to build and nothing fails to test.
//
// These fill every field reflectively rather than from a fixture, so a new
// field is covered the moment it exists rather than when someone remembers.

func TestNoFieldIsDroppedCrossingTheWire(t *testing.T) {
	t.Run("Spec", func(t *testing.T) {
		var v api.Spec
		fill(t, reflect.ValueOf(&v).Elem())
		assertSame(t, "Spec", v, apiSpec(protoSpec(v)))
	})
	t.Run("State", func(t *testing.T) {
		var v api.State
		fill(t, reflect.ValueOf(&v).Elem())
		assertSame(t, "State", v, apiState(protoState(v)))
	})
	t.Run("Sandbox", func(t *testing.T) {
		var v api.Sandbox
		fill(t, reflect.ValueOf(&v).Elem())
		assertSame(t, "Sandbox", v, apiSandbox(protoSandbox(v)))
	})
	t.Run("Port", func(t *testing.T) {
		var v api.Port
		fill(t, reflect.ValueOf(&v).Elem())
		assertSame(t, "Port", []api.Port{v}, apiPorts(protoPorts([]api.Port{v})))
	})
	t.Run("Ref", func(t *testing.T) {
		var v api.Ref
		fill(t, reflect.ValueOf(&v).Elem())
		assertSame(t, "Ref", v, apiRef(protoRef(v)))
	})
	t.Run("Path", func(t *testing.T) {
		var v api.Path
		fill(t, reflect.ValueOf(&v).Elem())
		assertSame(t, "Path", v, apiPath(protoPath(v)))
	})
	t.Run("ExecRequest", func(t *testing.T) {
		var v api.ExecRequest
		fill(t, reflect.ValueOf(&v).Elem())
		assertSame(t, "ExecRequest", v, apiExecRequest(protoExecRequest(v)))
	})
	t.Run("Stats", func(t *testing.T) {
		var v api.Stats
		fill(t, reflect.ValueOf(&v).Elem())
		assertSame(t, "Stats", v, apiSample(protoSample(v)))
	})
	t.Run("Policy", func(t *testing.T) {
		var v proxy.Policy
		fill(t, reflect.ValueOf(&v).Elem())
		assertSame(t, "Policy", v, apiPolicy(protoPolicy(v)))
	})
	t.Run("Entry", func(t *testing.T) {
		var v proxy.Entry
		fill(t, reflect.ValueOf(&v).Elem())
		assertSame(t, "Entry", v, apiDecision(protoDecision(v)))
	})
}

// assertSame names the field that differs, since "two structs are not equal" is
// not enough to find a line missing from a converter.
func assertSame(t *testing.T, what string, want, got any) {
	t.Helper()
	w, g := reflect.ValueOf(want), reflect.ValueOf(got)
	if w.Kind() != reflect.Struct {
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s did not survive the round trip:\n got %+v\nwant %+v", what, got, want)
		}
		return
	}
	for i := range w.NumField() {
		name := w.Type().Field(i).Name
		if !reflect.DeepEqual(w.Field(i).Interface(), g.Field(i).Interface()) {
			t.Errorf("%s.%s did not survive the round trip: got %+v, want %+v\n"+
				"add it to convert.go, or the field works in-process and vanishes through the daemon",
				what, name, g.Field(i).Interface(), w.Field(i).Interface())
		}
	}
}

// fill sets every field of a struct to a distinctive non-zero value.
func fill(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			v.Set(reflect.ValueOf(time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)))
			return
		}
		for i := range v.NumField() {
			if v.Field(i).CanSet() {
				fill(t, v.Field(i))
			}
		}
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(7)
	case reflect.Float64, reflect.Float32:
		v.SetFloat(1.5)
	case reflect.Slice:
		e := reflect.New(v.Type().Elem()).Elem()
		fill(t, e)
		v.Set(reflect.Append(v, e))
	case reflect.Map:
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fill(t, k)
		fill(t, e)
		v.Set(reflect.MakeMap(v.Type()))
		v.SetMapIndex(k, e)
	default:
		t.Fatalf("fill does not handle %s; teach it before adding a field of that kind", v.Kind())
	}
}
