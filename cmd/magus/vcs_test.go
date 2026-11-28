package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
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

type ConcludeMergeSuite struct {
	suite.Suite
}

func TestConcludeMergeSuite(t *testing.T) { suite.Run(t, new(ConcludeMergeSuite)) }

func (s *ConcludeMergeSuite) line(name string) string {
	enabled := true
	res, err := vcs.Resolve(context.Background(), "", "", types.VCSOptions{Name: name, Enabled: &enabled})
	s.Require().NoError(err)
	s.Require().NotNil(res.VCS)
	return concludeMergeLine(res.VCS)
}

func (s *ConcludeMergeSuite) TestGitNamesMergeContinue() {
	s.Equal("conclude it with `git merge --continue`", s.line("git"))
}

func (s *ConcludeMergeSuite) TestHgConcludesWithACommit() {
	s.Equal("conclude it with a hg commit", s.line("hg"))
}

func (s *ConcludeMergeSuite) TestJJHasNothingToConclude() {
	s.Equal("jj recorded the merge in the working-copy commit; nothing is left to conclude", s.line("jj"))
}
