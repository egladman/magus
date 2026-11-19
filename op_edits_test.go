package magus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// opEditsSuite runs a fixture formatter op directly, as `magus run fmt` and
// `magus run fmt:rw` would (ADR 0053). Its check mode fails while a.txt differs from
// its formatted form; under rw it writes the formatted form.
type opEditsSuite struct {
	suite.Suite
	w     *testkit.Workspace
	src   string
	spell string
	runs  atomic.Int32
}

func TestOpEditsSuite(t *testing.T) { suite.Run(t, new(opEditsSuite)) }

const (
	opEditsDrift     = "unformatted\n"
	opEditsFormatted = "formatted\n"
)

func (s *opEditsSuite) SetupTest() {
	s.w = testkit.NewWorkspace(s.T())
	s.src = s.w.Write("a.txt", opEditsDrift)
	s.runs.Store(0)
}

// open registers the fixture op with edits and opens the workspace under magusfile.
func (s *opEditsSuite) open(edits []string, magusfile string) *Magus {
	s.w.Magusfile(magusfile)
	s.spell = register.Spell(s.T(), "zzz-op-edits-spell",
		spells.WithTargets("fmt"),
		spells.WithSources("*.txt"),
		spells.WithOps(map[string]spells.Op{"fmt": {Command: spells.Command{
			Bin: "true", Edits: edits, Charms: map[string]spells.Charm{types.CharmReadWrite: {}},
		}}}),
		spells.WithInvoker(func(ctx context.Context, _ spells.InvokeRequest) (any, error) {
			s.runs.Add(1)
			if types.HasCharm(ctx, types.CharmReadWrite) {
				return nil, os.WriteFile(s.src, []byte(opEditsFormatted), 0o644)
			}
			got, err := os.ReadFile(s.src)
			if err != nil {
				return nil, err
			}
			if string(got) != opEditsFormatted {
				return nil, errors.New("fmt: a.txt is not formatted")
			}
			return nil, nil
		}),
	)
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(s.spell), WithSpell(types.MagusfileSpellName))
	m, err := Open(context.Background(), s.w.Root(), WithWorkspaceRegistry(reg), WithSpawnRule(tier.Ceiling))
	s.Require().NoError(err, "Open")
	s.T().Cleanup(func() { _ = m.Close() })
	return m
}

func (s *opEditsSuite) run(m *Magus, opts ...RunOption) error {
	return m.Run(context.Background(), []types.Target{{Path: ".", Name: "fmt"}}, opts...)
}

func (s *opEditsSuite) content() string {
	got, err := os.ReadFile(s.src)
	s.Require().NoError(err)
	return string(got)
}

// opRemedy is the MGS4007 remedy for the fixture op run directly.
func (s *opEditsSuite) opRemedy() string {
	return fmt.Sprintf("declare them in the edits of op %s::fmt", s.spell)
}

func (s *opEditsSuite) TestCheckVariantFailsOnDrift() {
	m := s.open([]string{"*.txt"}, "")
	err := s.run(m)
	s.Require().Error(err)
	s.NotErrorIs(err, types.UndeclaredSourceModified, "the tool's own failure, not a mutation report")
	s.ErrorContains(err, "a.txt is not formatted")
	s.Equal(opEditsDrift, s.content(), "the check writes nothing")
}

// An edit glob that matches no file is a claim about a class of file (go.sum before the
// first tidy), so the run does not refuse it the way it refuses a target's.
func (s *opEditsSuite) TestWriteVariantPassesAndIsNotRecorded() {
	m := s.open([]string{"a.txt", "absent.lock"}, "")
	s.Require().NoError(s.run(m, WithWrite()), "the op declares the file it rewrote")
	s.Equal(opEditsFormatted, s.content())

	s.Require().NoError(os.WriteFile(s.src, []byte(opEditsDrift), 0o644))
	s.Require().NoError(s.run(m, WithWrite()))
	s.Equal(int32(2), s.runs.Load(), "the run that rewrote filed no entry under the pre-run bytes")
	s.Equal(opEditsFormatted, s.content())
}

func (s *opEditsSuite) TestWriteVariantRecordsAtItsFixedPoint() {
	m := s.open([]string{"*.txt"}, "")
	s.Require().NoError(s.run(m, WithWrite()))
	s.Require().NoError(s.run(m, WithWrite()), "the second run changes nothing and records")
	s.Require().NoError(s.run(m, WithWrite()))
	s.Equal(int32(2), s.runs.Load(), "the third run replays the fixed point")
}

// The edited file is a source of the check's key, so its own drift invalidates a
// recorded pass.
func (s *opEditsSuite) TestCheckVariantFailsAgainAfterNewDrift() {
	m := s.open([]string{"*.txt"}, "")
	s.Require().NoError(os.WriteFile(s.src, []byte(opEditsFormatted), 0o644))
	s.Require().NoError(s.run(m), "a formatted file passes the check")
	s.Require().NoError(s.run(m))
	s.Require().Equal(int32(1), s.runs.Load(), "the passing check recorded and replayed")

	s.Require().NoError(os.WriteFile(s.src, []byte(opEditsDrift), 0o644))
	err := s.run(m)
	s.Require().Error(err, "a drifted file misses the recorded pass")
	s.ErrorContains(err, "a.txt is not formatted")
	s.Equal(int32(2), s.runs.Load())
}

func (s *opEditsSuite) TestOpRewritingOutsideItsEditsIsReported() {
	m := s.open([]string{"*.md"}, "")
	err := s.run(m, WithWrite())
	s.Require().ErrorIs(err, types.UndeclaredSourceModified)
	s.ErrorContains(err, s.opRemedy())
}

// The typos shape: a write charm on an op that declares no edits.
func (s *opEditsSuite) TestOpWithNoEditsIsStillReported() {
	m := s.open(nil, "")
	err := s.run(m, WithWrite())
	s.Require().ErrorIs(err, types.UndeclaredSourceModified)
	s.ErrorContains(err, s.opRemedy(), "a step with no body to call ctx.writesFiles is told the field it can set")
}

// A magusfile export named like the op shadows it, so the run is the body's and the
// op's edits are not folded; the spell::op filter bypasses the shadow and folds them.
func (s *opEditsSuite) TestComposerDoesNotInheritAnOpsEdits() {
	const shadow = `import "magus";
import "fs";
export fun fmt(ctx: magus\Context, args: [str]) > void !> any {
    fs\writeFile("a.txt", "formatted\n");
}
`
	m := s.open([]string{"*.txt"}, shadow)
	p := m.Get(".")
	s.Require().NotNil(p)
	edit := types.Glob{Pattern: "*.txt"}
	s.NotContains(m.buildStep(p, "fmt").Updates, edit, "the shadowing body runs, not the op")
	s.Empty(m.buildStep(p, "fmt").Ops)

	err := s.run(m, WithWrite())
	s.Require().ErrorIs(err, types.UndeclaredSourceModified)
	s.ErrorContains(err, "ctx.writesFiles", "a body declares its own writes")
	s.NotContains(err.Error(), "edits of op")
	s.Equal(int32(0), s.runs.Load(), "the op never ran")

	s.Require().NoError(os.WriteFile(s.src, []byte(opEditsDrift), 0o644))
	s.Require().NoError(s.run(m, WithWrite(), WithSpellFilter(s.spell)),
		"the filter dispatches to the op, so its edits are folded")
	s.Equal(int32(1), s.runs.Load())
}

// A bare op's edits key its step and exempt the file, and stay off the project's
// target declarations.
func (s *opEditsSuite) TestBareOpStepFoldsItsEdits() {
	m := s.open([]string{"*.txt", "!skip.txt"}, "")
	p := m.Get(".")
	s.Require().NotNil(p)
	step := m.buildStep(p, "fmt")
	edit := types.Glob{Pattern: "*.txt", Except: []string{"skip.txt"}}
	s.Contains(step.Updates, edit)
	s.Contains(step.Sources, edit)
	s.Equal([]string{fmt.Sprintf("%s::fmt", s.spell)}, step.Ops)
	s.Empty(p.TargetUpdates["fmt"], "an op's edits are not a target's declaration")
}
