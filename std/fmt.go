package std

import (
	"context"
	"fmt"
)

//go:generate go run ../cmd/magus-bindings-gen -module fmt -lang buzz -out ../host/gen/fmt.go

func init() { Register(Fmt) }

// Fmt is the "fmt" host module: string formatting via Go's printf verbs. It
// exists to collapse long "+"-concatenation chains into a single readable call.
// Args are strings (the variadic boundary only carries strings), so use %s/%q —
// numeric verbs like %d have nothing typed to act on.
var Fmt = Module{
	Name: "fmt",
	Doc:  "String formatting (printf-style).",
	Methods: []Method{
		{
			Name:    "sprintf",
			Doc:     "Format string args into the template using Go printf verbs (e.g. %s, %q). Returns the formatted string.",
			Args:    []Arg{{Name: "format", Type: TypeString}, {Name: "args", Type: TypeString, Variadic: true}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    FmtSprintf,
		},
	},
}

// FmtSprintf formats args into format using Go's fmt.Sprintf. The variadic
// boundary carries strings, so each arg satisfies %s/%q verbs.
func FmtSprintf(_ context.Context, format string, args ...string) (string, error) {
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}
	return fmt.Sprintf(format, anyArgs...), nil
}
