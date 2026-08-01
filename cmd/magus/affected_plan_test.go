package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadAffectedPlanPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		null  bool
		want  []string
	}{
		{
			name:  "newline paths are sorted and deduplicated",
			input: "z/file.go\na file.go\n\nz/file.go\n",
			want:  []string{"a file.go", "z/file.go"},
		},
		{
			name:  "nul batches collapse into one plan",
			input: "z/file.go\x00\x00a file.go\x00z/file.go\x00",
			null:  true,
			want:  []string{"a file.go", "z/file.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := readAffectedPlanPaths(strings.NewReader(tt.input), tt.null)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
