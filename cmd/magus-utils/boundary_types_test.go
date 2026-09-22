package main

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A closed string type declares its cases by hand in its own package, and the Buzz
// mirror names the same values here. This holds the two to one list: a case added to
// either side alone fails.
func TestBoundaryEnumsMatchTheirSets(t *testing.T) {
	for _, e := range boundaryEnums {
		values, ok := reflect.Zero(e.Type).Interface().(interface{ Values() []string })
		if !ok {
			continue
		}
		var mirrored []string
		for _, c := range e.Cases {
			if c.Value != "" {
				mirrored = append(mirrored, c.Value)
			}
		}
		require.NotEmpty(t, mirrored, e.Name)
		assert.Equal(t, values.Values(), mirrored, "%s: boundaryEnums and the type's declared set differ", e.Name)
	}
}
