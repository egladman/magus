package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/graph/knowledge"
)

// A lazy graph builds at most once per command: a load, a failed one and a skipped one
// each answer every later get alike without asking again.
func TestLazyGraphLoadsOnce(t *testing.T) {
	t.Parallel()

	built := knowledge.NewGraph()
	for name, tc := range map[string]struct {
		skip  bool
		err   error
		want  *knowledge.Graph
		loads int
	}{
		"a load":         {want: built, loads: 1},
		"a failed load":  {err: errors.New("no index"), loads: 1},
		"a skipped load": {skip: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var lazy lazyGraph
			var skips, loads int
			skip := func(context.Context) bool { skips++; return tc.skip }
			build := func(context.Context) (*knowledge.Graph, error) {
				loads++
				if tc.err != nil {
					return nil, tc.err
				}
				return built, nil
			}
			for range 3 {
				assert.Same(t, tc.want, lazy.get(t.Context(), skip, build))
			}
			assert.Equal(t, []int{1, tc.loads}, []int{skips, loads})
		})
	}
}
