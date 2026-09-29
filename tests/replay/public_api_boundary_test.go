package replay_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
)

// Generated wire models remain transport implementation details. This catches
// accidental protocol types in any exported ClientAPI request or result.
func TestClientAPIDoesNotExposeWireModels(t *testing.T) {
	t.Parallel()

	seen := make(map[reflect.Type]bool)

	var inspect func(reflect.Type)

	inspect = func(typ reflect.Type) {
		if typ == nil || seen[typ] {
			return
		}

		seen[typ] = true

		if strings.Contains(typ.PkgPath(), "/internal/generated") ||
			strings.Contains(typ.PkgPath(), "/pkg/generatedhttp") ||
			strings.Contains(typ.PkgPath(), "/pkg/generatedsignaling") ||
			strings.Contains(typ.PkgPath(), "/pkg/generatedfcm") {
			t.Errorf("ClientAPI exposes generated wire type %s", typ)

			return
		}

		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
			inspect(typ.Elem())
		case reflect.Map:
			inspect(typ.Key())
			inspect(typ.Elem())
		case reflect.Struct:
			for i := range typ.NumField() {
				field := typ.Field(i)
				if field.IsExported() {
					inspect(field.Type)
				}
			}
		case reflect.Interface:
			for i := range typ.NumMethod() {
				inspect(typ.Method(i).Type)
			}
		case reflect.Func:
			for i := range typ.NumIn() {
				inspect(typ.In(i))
			}

			for i := range typ.NumOut() {
				inspect(typ.Out(i))
			}
		case reflect.Invalid,
			reflect.Bool,
			reflect.Int,
			reflect.Int8,
			reflect.Int16,
			reflect.Int32,
			reflect.Int64,
			reflect.Uint,
			reflect.Uint8,
			reflect.Uint16,
			reflect.Uint32,
			reflect.Uint64,
			reflect.Uintptr,
			reflect.Float32,
			reflect.Float64,
			reflect.Complex64,
			reflect.Complex128,
			reflect.String,
			reflect.UnsafePointer:
			return
		}
	}
	inspect(reflect.TypeOf((*ring.ClientAPI)(nil)).Elem())
}
