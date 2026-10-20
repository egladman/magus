package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMagusFlag(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args  []string
		names []string
		want  bool
	}{
		{args: []string{"run", "test", ".", "-s"}, names: []string{"s"}, want: true},
		{args: []string{"run", "test", ".", "--silent"}, names: []string{"s", "silent"}, want: true},
		{args: []string{"run", "test", ".", "-s=true"}, names: []string{"s"}, want: true},
		{args: []string{"run", "test", ".", "-s=false"}, names: []string{"s"}, want: false},
		{args: []string{"run", "test", ".", "--silent=0"}, names: []string{"s", "silent"}, want: false},
		{args: []string{"run", "test", ".", "-s", "--silent=false"}, names: []string{"s", "silent"}, want: false},
		{args: []string{"run", "test", ".", "-s=false", "-s"}, names: []string{"s"}, want: true},
		{args: []string{"--dry-run=false", "run", "test", "."}, names: []string{"dry-run"}, want: false},
		{args: []string{"--root=false", "run"}, names: []string{"root"}, want: true},
		{args: []string{"run", "test", ".", "--timeout=0"}, names: []string{"timeout"}, want: true},
		{args: []string{"run", "test", ".", "--", "-s"}, names: []string{"s"}, want: false},
		{args: []string{"run", "test", ".", "-cache-dir", "x"}, names: []string{"c"}, want: false},
		{args: []string{"run", "test", "."}, names: []string{"s"}, want: false},
	} {
		assert.Equal(t, tc.want, magusFlag(tc.args, tc.names...), "%q %q", tc.args, tc.names)
	}
}
