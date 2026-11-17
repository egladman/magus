package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSplitVCSVerb pins that the subcommand is found past a leading flag. Reading args[0]
// alone let `magus vcs -q merge-driver ...` miss the merge-driver dispatch profile, which
// keeps a workspace load (and its .gitattributes write) out of git's index
// manipulation.
func TestSplitVCSVerb(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantVerb string
		wantRest []string
	}{
		{"plain", []string{"resolve"}, "resolve", nil},
		{"verb then args", []string{"ls-files", "a.go"}, "ls-files", []string{"a.go"}},
		{"flag before verb", []string{"-q", "merge-driver", "%O"}, "merge-driver", []string{"-q", "%O"}},
		{"long flag before verb", []string{"--silent", "ls-files"}, "ls-files", []string{"--silent"}},
		{"help flag is its own verb", []string{"-h"}, "-h", nil},
		{"help flag after a flag", []string{"-q", "--help"}, "--help", []string{"-q"}},
		{"no verb at all", []string{"-q"}, "", []string{"-q"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verb, rest := splitVCSVerb(tt.args)
			assert.Equal(t, tt.wantVerb, verb)
			if tt.wantRest == nil {
				assert.Empty(t, rest)
				return
			}
			assert.Equal(t, tt.wantRest, rest)
		})
	}
}
