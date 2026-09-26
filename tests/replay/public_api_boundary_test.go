package replay_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
)

// The generated HTTP client remains a transport implementation detail. This
// catches accidental wire types in any exported ClientAPI request or result.
func TestClientAPIDoesNotExposeHTTPWireModels(t *testing.T) {
	seen := make(map[reflect.Type]bool)
	var inspect func(reflect.Type)
	inspect = func(typ reflect.Type) {
		if typ == nil || seen[typ] {
			return
		}
		seen[typ] = true
		if strings.Contains(typ.PkgPath(), "/pkg/generatedhttp") {
			t.Errorf("ClientAPI exposes generated HTTP type %s", typ)
			return
		}
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
			inspect(typ.Elem())
		case reflect.Map:
			inspect(typ.Key())
			inspect(typ.Elem())
		case reflect.Struct:
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if field.IsExported() {
					inspect(field.Type)
				}
			}
		case reflect.Interface:
			for i := 0; i < typ.NumMethod(); i++ {
				inspect(typ.Method(i).Type)
			}
		case reflect.Func:
			for i := 0; i < typ.NumIn(); i++ {
				inspect(typ.In(i))
			}
			for i := 0; i < typ.NumOut(); i++ {
				inspect(typ.Out(i))
			}
		}
	}
	inspect(reflect.TypeOf((*ring.ClientAPI)(nil)).Elem())
}
