package main

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReorderFlagsFirst(t *testing.T) {
	// A flag set mirroring the shapes cmdParse sees: a value flag, a bool flag.
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.String("output", "", "value flag")
		fs.Bool("explain", false, "bool flag")
		var v verbosity
		fs.Var(&v, "v", "counted bool flag")
		return fs
	}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"bool flag after positional", []string{"lint:rw", "--explain"}, []string{"--explain", "lint:rw"}},
		{"value flag after positional", []string{"lint:rw", "-output", "name"}, []string{"-output", "name", "lint:rw"}},
		{"flags already first unchanged", []string{"--explain", "lint:rw"}, []string{"--explain", "lint:rw"}},
		{"equals form self-contained", []string{"api", "--output=json"}, []string{"--output=json", "api"}},
		{"counted bool -v does not eat positional", []string{"build", "-v"}, []string{"-v", "build"}},
		{"double-dash halts reorder, tail preserved", []string{"test", "--explain", "--", "-run", "X"}, []string{"--explain", "test", "--", "-run", "X"}},
		{"bare dash is a positional", []string{"-", "--explain"}, []string{"--explain", "-"}},
		{"multiple positionals keep order", []string{"a", "--explain", "b"}, []string{"--explain", "a", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, reorderFlagsFirst(newFS(), c.in))
		})
	}
}
