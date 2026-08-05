package gen

import (
	"reflect"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
)

// TestEveryBoundaryTypeEncodes is the guard the DoctorCheckStatus bug got past.
//
// A field typed as a DEFINED type over a basic kind (types.DoctorCheckStatus,
// types.TargetRunState) matches no case in AnyVal's type switch, because a type switch
// matches on identity rather than underlying type - so it fell through and arrived in
// Buzz as null. `doctor().checks[0].status` read null rather than "ok" while the SDK
// docs told callers to branch on exactly that field. The identical trap had already
// bitten types.BuzzObject once before.
//
// Nothing caught either, because there are 51 runtime boundary types and no test
// crossed all of them. This does: it populates every exported field with a non-zero
// value, encodes, and asserts nothing arrived null. The list is generated from the same
// registry that emits the mirrors, so a new boundary type is covered when it is
// declared rather than when someone remembers.
func TestEveryBoundaryTypeEncodes(t *testing.T) {
	for _, zero := range RuntimeBoundaryTypes {
		rt := reflect.TypeOf(zero)
		t.Run(rt.Name(), func(t *testing.T) {
			v := reflect.New(rt).Elem()
			populate(v)

			obj, ok := v.Interface().(interface{ BuzzObject() types.BuzzObject })
			require.True(t, ok, "%s is registered as a runtime object but has no BuzzObject", rt.Name())

			encoded := AnyVal(obj.BuzzObject())
			require.True(t, encoded.IsMap(), "%s encoded as %s, not a map", rt.Name(), encoded.Kind())

			for _, key := range encoded.MapKeys() {
				got, _ := encoded.MapGet(key)
				require.False(t, got.IsNull(),
					"%s.%s arrived as null; a populated field must survive the boundary", rt.Name(), key)
			}
		})
	}
}

// populate fills every exported field with a non-zero value, so a field that fails to
// encode shows up as null rather than as an indistinguishable zero.
func populate(v reflect.Value) {
	rt := v.Type()
	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		fv := v.Field(i)
		switch fv.Kind() {
		case reflect.String:
			// Through SetString rather than a literal so a DEFINED string type gets a
			// value of its own type - which is the whole case under test.
			fv.SetString("x")
		case reflect.Bool:
			fv.SetBool(true)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			fv.SetInt(1)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			fv.SetUint(1)
		case reflect.Float32, reflect.Float64:
			fv.SetFloat(1)
		case reflect.Slice:
			fv.Set(reflect.MakeSlice(fv.Type(), 1, 1))
			if fv.Index(0).Kind() == reflect.Struct {
				populate(fv.Index(0))
			} else if fv.Index(0).Kind() == reflect.String {
				fv.Index(0).SetString("x")
			}
		case reflect.Map:
			fv.Set(reflect.MakeMap(fv.Type()))
		case reflect.Struct:
			if fv.Type() == reflect.TypeOf(time.Time{}) {
				fv.Set(reflect.ValueOf(time.Unix(1, 0).UTC()))
				continue
			}
			populate(fv)
		case reflect.Pointer:
			fv.Set(reflect.New(fv.Type().Elem()))
			if fv.Elem().Kind() == reflect.Struct {
				populate(fv.Elem())
			}
		}
	}
}
