//go:build !wasm

package std

import (
	"testing"

	"github.com/egladman/magus/types"
	typesvcs "github.com/egladman/magus/types/vcs"
	"github.com/stretchr/testify/suite"
)

type magusHandleSuite struct{ suite.Suite }

func TestMagusHandle(t *testing.T) { suite.Run(t, new(magusHandleSuite)) }

func (s *magusHandleSuite) TestJobResultDecodesThroughTheVersionedDecoder() {
	result, err := decodeJobResult(map[string]any{
		"api_version":   types.JobResultAPIVersion,
		"changed_paths": []any{"internal/job/lifecycle.go"},
		"validation": map[string]any{
			"command":    "magus run test .",
			"output_ref": "out123",
		},
		"unresolved_risks": []any{},
	})
	s.Require().NoError(err)
	s.Equal(types.JobResultAPIVersion, result.Version)
	s.Equal("out123", result.Validation.OutputRef)
}

func (s *magusHandleSuite) TestJobResultRefusesAFieldTheVerifierIgnores() {
	_, err := decodeJobResult(map[string]any{
		"api_version":      types.JobResultAPIVersion,
		"changed_paths":    []any{},
		"validation":       map[string]any{},
		"unresolved_risks": []any{},
		"claimed_pass":     true,
	})
	s.Require().ErrorContains(err, "claimed_pass")
}

func (s *magusHandleSuite) TestMovedPathsNamesEachDigestThatMoved() {
	at := typesvcs.Checkpoint{Revision: "r1"}
	c := types.Change{At: at, Files: map[string]string{"a.go": "1", "b.go": "2"}}

	got := movedPaths(c, at, map[string]string{"a.go": "1", "b.go": "3", "c.go": "4"})

	s.Equal([]string{"b.go", "c.go"}, got)
}

func (s *magusHandleSuite) TestMovedPathsNamesAMovedRevision() {
	c := types.Change{At: typesvcs.Checkpoint{Revision: "r1"}}

	got := movedPaths(c, typesvcs.Checkpoint{Revision: "r2"}, nil)

	s.Equal([]string{"HEAD r1 -> r2"}, got)
}

func (s *magusHandleSuite) TestTierOptionsKeepTheVCSDiffForNoPaths() {
	got := tierOptions(types.Change{Base: "abc123"}, types.TierOptions{Base: "ignored", Paths: []string{}})

	s.Equal(types.TierOptions{Base: "abc123"}, got)
}

func (s *magusHandleSuite) TestTierOptionsKeepNamedPaths() {
	got := tierOptions(types.Change{}, types.TierOptions{Paths: []string{"notes/v1.md"}, Checkout: "/other"})

	s.Equal(types.TierOptions{Paths: []string{"notes/v1.md"}, Checkout: "/other"}, got)
}

func (s *magusHandleSuite) TestMovedPathsIsEmptyForTheSameTree() {
	at := typesvcs.Checkpoint{Revision: "r1", PatchDigest: "p"}
	c := types.Change{At: at, Files: map[string]string{"a.go": "1"}}

	s.Empty(movedPaths(c, at, map[string]string{"a.go": "1"}))
}
