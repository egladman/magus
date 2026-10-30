package emit

import (
	"reflect"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
)

func goName(rt reflect.Type) string { return rt.Name() }

func TestTSTypesRefusesUnorderedOrUnknown(t *testing.T) {
	diag := TSType{Name: "Diagnostic", Type: reflect.TypeFor[types.Diagnostic]()}
	result := TSType{Name: "Result", Type: reflect.TypeFor[types.Result[buzzgen.T]]()}

	_, err := TSTypes([]TSType{result, diag}, goName)
	require.ErrorContains(t, err, "Diagnostic must be flagged TS and precede it")

	other := TSType{Name: "Box", Type: reflect.TypeFor[types.Result[buzzgen.T]]()}
	_, err = TSTypes([]TSType{diag, other}, goName)
	require.ErrorContains(t, err, "no TypeScript form for a generic other than Result")
}

func TestTSTypesRefusesAStructDeclaredLater(t *testing.T) {
	type inner struct{ N int }
	type outer struct{ In inner }
	_, err := TSTypes([]TSType{{Name: "outer", Type: reflect.TypeFor[outer]()}}, goName)
	require.ErrorContains(t, err, "inner is not flagged TS ahead of its use")
}
