package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// LeaseBoundarySuite pins the routes by which a leased worker's write left
// its boundary. Each one reached a file the worker's write paths do not
// name.
type LeaseBoundarySuite struct {
	suite.Suite
	ctx  context.Context
	root string
}

func TestLeaseBoundarySuite(t *testing.T) {
	suite.Run(t, new(LeaseBoundarySuite))
}

func (s *LeaseBoundarySuite) SetupTest() {
	s.ctx, s.root = fleetFixture(s.T(), writePathFleet()...)
}

func (s *LeaseBoundarySuite) deny(command string) string {
	return denyWriteOutsideLease(s.ctx, Dependencies{callDir: s.root}, "lease-b", command)
}

func (s *LeaseBoundarySuite) jobLedger() string {
	return filepath.Join(os.Getenv("XDG_STATE_HOME"), "magus", "jobs", "repo-0123456789ab", "jobs.jsonl")
}

func (s *LeaseBoundarySuite) TestToolWriteIntoTheJobLedgerIsRefused() {
	g := gradeLeasedWrite(s.ctx, Dependencies{}, "lease-b", s.jobLedger())
	s.Equal(decisionDeny, g.Decision)
	s.Contains(g.Reason, "magus's own state directory")
}

func (s *LeaseBoundarySuite) TestShellAppendToTheJobLedgerIsRefused() {
	s.Contains(s.deny(`echo '{"id":"lease-b","write_paths":["**"]}' >> `+s.jobLedger()), "magus's own state directory")
}

func (s *LeaseBoundarySuite) TestCopyIntoTheUserConfigIsRefused() {
	s.Contains(s.deny("cp x "+filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "magus", "magus.yaml")), "magus's own config directory")
}

func (s *LeaseBoundarySuite) TestTeeIntoTheSharedCacheIsRefused() {
	s.Contains(s.deny("echo x | tee "+filepath.Join(os.Getenv("XDG_CACHE_HOME"), "magus", "cache", "k")), "magus's own cache directory")
}

func (s *LeaseBoundarySuite) TestUnleasedWriteIntoTheJobLedgerIsNotGraded() {
	s.Empty(gradeLeasedWrite(s.ctx, Dependencies{}, "", s.jobLedger()).Reason)
}

func (s *LeaseBoundarySuite) TestReadOfTheJobLedgerPasses() {
	s.Empty(s.deny("cat " + s.jobLedger()))
}

func (s *LeaseBoundarySuite) TestWriteInAScratchCheckoutIsNotMagusState() {
	s.Empty(s.deny("echo x > " + filepath.Join(os.Getenv("XDG_CACHE_HOME"), "magus", "tmp", "scratch", "notes.txt")))
}

// nestedLease stands a worker's checkout in .claude/worktrees/x under a
// parent checkout. It returns the context, the parent and the nested root.
func (s *LeaseBoundarySuite) nestedLease() (context.Context, string, string) {
	parent := s.T().TempDir()
	nested := filepath.Join(parent, ".claude", "worktrees", "x")
	for _, dir := range []string{parent, nested} {
		s.Require().NoError(os.MkdirAll(filepath.Join(dir, "internal", "guard"), 0o755))
		s.Require().NoError(os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644))
	}
	s.Require().NoError(os.WriteFile(filepath.Join(nested, ".git"), []byte("gitdir: x\n"), 0o644))
	ctx, _ := fleetFixture(s.T(), types.Job{
		ID: "nested", WritePaths: []string{"internal/guard/**"}, CheckoutRoot: nested,
		State: types.StateRunning, Registered: 1,
	})
	return ctx, parent, nested
}

func (s *LeaseBoundarySuite) TestShellWriteFromANestedCheckoutIntoItsParentIsRefused() {
	ctx, parent, nested := s.nestedLease()
	target := filepath.Join(parent, "internal", "guard", "guard.go")
	onPath := gradeLeasedWrite(ctx, Dependencies{}, "nested", target)
	s.Require().Equal(decisionDeny, onPath.Decision)
	from := Dependencies{callDir: nested}
	s.Equal(onPath.Reason, denyWriteOutsideLease(ctx, from, "nested", "echo x > ../../../internal/guard/guard.go"))
	s.Equal(onPath.Reason, denyWriteOutsideLease(ctx, from, "nested", "cp x "+target))
}

func (s *LeaseBoundarySuite) write(rel, body string) {
	p := filepath.Join(s.root, rel)
	s.Require().NoError(os.MkdirAll(filepath.Dir(p), 0o755))
	s.Require().NoError(os.WriteFile(p, []byte(body), 0o644))
}

func (s *LeaseBoundarySuite) link(rel, target string) {
	p := filepath.Join(s.root, rel)
	s.Require().NoError(os.MkdirAll(filepath.Dir(p), 0o755))
	s.Require().NoError(os.Symlink(target, p))
}

func (s *LeaseBoundarySuite) TestSedThroughALinkToAnUndeclaredFileIsRefused() {
	s.write("docs/notes.md", "a\n")
	s.link("cmd/magus/notes.md", "../../docs/notes.md")
	s.Contains(s.deny("sed -i 's/a/b/' cmd/magus/notes.md"), "docs/notes.md")
}

func (s *LeaseBoundarySuite) TestRedirectThroughADanglingLinkIsRefused() {
	s.link("cmd/magus/new.go", "../../docs/new.go")
	s.Contains(s.deny("echo x > cmd/magus/new.go"), "docs/new.go")
}

func (s *LeaseBoundarySuite) TestToolWriteThroughADanglingLinkIsRefused() {
	s.link("cmd/magus/new.go", "../../docs/new.go")
	g := gradeLeasedWrite(s.ctx, Dependencies{}, "lease-b", filepath.Join(s.root, "cmd", "magus", "new.go"))
	s.Equal(decisionDeny, g.Decision)
	s.Contains(g.Reason, "docs/new.go")
}

func (s *LeaseBoundarySuite) TestRedirectThroughALinkIntoAnotherCheckoutIsRefused() {
	other := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(other, "magusfile.buzz"), nil, 0o644))
	s.link("cmd/magus/out.go", filepath.Join(other, "out.go"))
	s.Contains(s.deny("echo x > cmd/magus/out.go"), "in another checkout")
}

func (s *LeaseBoundarySuite) TestTeeToANewUndeclaredFileIsRefused() {
	s.Contains(s.deny("echo x | tee docs/new.md"), "docs/new.md")
}

func (s *LeaseBoundarySuite) TestCopyToANewUndeclaredFileIsRefused() {
	s.Contains(s.deny("cp x docs/new.md"), "docs/new.md")
}

func (s *LeaseBoundarySuite) TestInstallToANewUndeclaredFileIsRefused() {
	s.Contains(s.deny("install -m 644 x docs/new.md"), "docs/new.md")
}

func (s *LeaseBoundarySuite) TestMoveToANewUndeclaredFileIsRefused() {
	s.Contains(s.deny("mv /tmp/x docs/new.md"), "docs/new.md")
}

func (s *LeaseBoundarySuite) TestCopyToANewFileInsideTheLeasePasses() {
	s.Empty(s.deny("cp x cmd/magus/new.go"))
}

func (s *LeaseBoundarySuite) TestCopyFromAnUndeclaredFilePasses() {
	s.write("docs/notes.md", "a\n")
	s.Empty(s.deny("cp docs/notes.md cmd/magus/notes.md"))
}

func (s *LeaseBoundarySuite) TestShellWriteInsideTheNestedCheckoutPasses() {
	ctx, _, nested := s.nestedLease()
	s.Empty(denyWriteOutsideLease(ctx, Dependencies{callDir: nested}, "nested", "echo x > internal/guard/guard.go"))
}
