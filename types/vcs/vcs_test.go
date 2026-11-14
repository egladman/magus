package vcs

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type CheckpointSuite struct{ suite.Suite }

func TestCheckpointSuite(t *testing.T) { suite.Run(t, new(CheckpointSuite)) }

func (s *CheckpointSuite) TestCleanTokenIsTheRevision() {
	s.Equal("abc", Checkpoint{Revision: "abc"}.Token())
}

func (s *CheckpointSuite) TestTrackedOnlyTokenKeepsThePatchDigest() {
	cp := Checkpoint{Revision: "abc", Dirty: true, PatchDigest: "0123456789abcdef"}
	s.Equal("abc+0123456789abcdef", cp.Token())
}

func (s *CheckpointSuite) TestUntrackedFilesChangeTheDigest() {
	tracked := Checkpoint{Revision: "abc", Dirty: true, PatchDigest: "0123456789abcdef"}
	both := tracked
	both.UntrackedDigest = "fedcba9876543210"
	s.NotEqual(tracked.Token(), both.Token())
	s.Len(both.Token(), len("abc+")+32)
}
