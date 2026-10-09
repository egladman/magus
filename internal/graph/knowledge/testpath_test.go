package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsTestPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{"pkg/x_test.go", true},
		{"pkg/x_test.go:12", true},
		{"web/a.test.ts", true},
		{"web/a.spec.tsx", true},
		{"py/test_thing.py", true},
		{"py/thing_test.py", true},
		{"pkg/x.go", false},
		{"web/testing.ts", false},
		{"py/tester.py", false},
		{"contest.go", false},
		{"", false},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, IsTestPath(tc.path), tc.path)
	}
}
