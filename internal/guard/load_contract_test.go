package guard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/contract"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

type ContractSkewSuite struct {
	suite.Suite
	root string
	deps Dependencies
}

func TestContractSkewSuite(t *testing.T) {
	suite.Run(t, new(ContractSkewSuite))
}

func (s *ContractSkewSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.deps = Dependencies{Binary: filepath.Join(s.root, "magus"), BinaryVersion: "v0-test"}
}

func (s *ContractSkewSuite) carryContract() {
	for _, dir := range []string{"internal/commands/testdata", "internal/spell/gen/decls", "internal/spell/gen/types"} {
		s.Require().NoError(os.MkdirAll(filepath.Join(s.root, dir), 0o755))
	}
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "internal/commands/testdata/api.lock"), []byte("another contract\n"), 0o644))
}

func (s *ContractSkewSuite) deny(line string) string {
	at := location{workspace: s.root, dir: s.root, cacheDir: s.T().TempDir()}
	return denyContractSkew(s.T().Context(), s.deps, at, line, DialectBash)
}

func (s *ContractSkewSuite) TestADifferingContractRefusesAChangeAndNamesTheRelink() {
	s.carryContract()
	s.Contains(s.deny("touch notes.txt"), fmt.Sprintf("relink ./magus, alone on its line: `%s`", ward.RelinkAt(s.root)))
}

func (s *ContractSkewSuite) TestADifferingContractLetsAReadThrough() {
	s.carryContract()
	s.Empty(s.deny("git status"))
}

func (s *ContractSkewSuite) TestADifferingContractLetsTheRelinkThrough() {
	s.carryContract()
	s.Empty(s.deny(ward.RelinkAt(s.root)))
}

func (s *ContractSkewSuite) TestATreeCarryingNoContractNeverDiffers() {
	s.Empty(s.deny("touch notes.txt"))
}

func (s *ContractSkewSuite) TestAnUnreadableContractRefusesAChange() {
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "internal/commands/testdata/api.lock"), 0o755))
	s.Contains(s.deny("touch notes.txt"), "cannot read the contract")
}

// authorityRows is a job store holding the given rows.
type authorityRows []types.Job

func (r authorityRows) List() ([]types.Job, error) { return r, nil }

// AuthorityContractSuite reproduces the incident of 2026-11-12. The root
// relinked while a worker was live, and the worker's binary, built from its
// authority, was refused as stale against the root's working tree.
type AuthorityContractSuite struct {
	suite.Suite
	root, authority string
	deps            Dependencies
}

func TestAuthorityContractSuite(t *testing.T) {
	suite.Run(t, new(AuthorityContractSuite))
}

// SetupTest commits this binary's contract in a checkout of magus as the
// authority, then moves the working tree to another contract.
func (s *AuthorityContractSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.git("init", "-q", "-b", "main")
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n", ownModule), 0o644))
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "magus"), []byte("#!/bin/sh\n"), 0o755))
	carryOwnContract(s.T(), s.root)
	s.authority = s.commit("authority")
	carryContract(s.T(), s.root, "config another.key\n")
	s.deps = Dependencies{Binary: filepath.Join(s.root, "magus"), BinaryVersion: "v0-test"}
}

func (s *AuthorityContractSuite) git(args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", s.root, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
	s.Require().NoError(err, string(out))
	return strings.TrimSpace(string(out))
}

func (s *AuthorityContractSuite) commit(msg string) string {
	s.git("add", "-A")
	s.git("commit", "-q", "-m", msg)
	return s.git("rev-parse", "HEAD")
}

// at is the call's location, whose store holds the worker's row recording
// authority.
func (s *AuthorityContractSuite) at(authority string) location {
	return location{workspace: s.root, dir: s.root, cacheDir: s.T().TempDir(),
		rows: authorityRows{{ID: "worker", Authority: authority}}}
}

func (s *AuthorityContractSuite) deny(lease, authority string) string {
	s.deps.lease = lease
	return denyContractSkew(s.T().Context(), s.deps, s.at(authority), "touch notes.txt", DialectBash)
}

func (s *AuthorityContractSuite) TestTheWorkingTreeCarriesAnotherContract() {
	s.True(differs(contract.Check(s.root)))
}

func (s *AuthorityContractSuite) TestALeasedCallMatchingItsAuthorityIsJudged() {
	s.Empty(s.deny("worker", s.authority))
}

func (s *AuthorityContractSuite) TestAnUnleasedCallReadsTheWorkingTree() {
	s.Contains(s.deny("", s.authority), "relink ./magus")
}

func (s *AuthorityContractSuite) TestARowWithNoAuthorityReadsTheWorkingTree() {
	s.Contains(s.deny("worker", ""), "relink ./magus")
}

func (s *AuthorityContractSuite) TestAnAuthorityCarryingAnotherContractIsRefused() {
	moved := s.commit("another contract")
	carryOwnContract(s.T(), s.root)
	s.Contains(s.deny("worker", moved), fmt.Sprintf("%s at %.12s carries", s.root, moved))
}

func (s *AuthorityContractSuite) TestAnUnreadableAuthorityIsRefused() {
	s.Contains(s.deny("worker", "0123456789abcdef0123456789abcdef01234567"), "cannot read the contract")
}

func (s *AuthorityContractSuite) TestALeasedLoadFailureMatchingItsAuthorityIsNotStale() {
	ctx, failure := s.T().Context(), errors.New("magusfile.buzz:3:1: expected expression")
	s.Equal(unloadNone, unloadCause(ctx, failure, s.at(s.authority), "worker"))
	s.Equal(unloadStale, unloadCause(ctx, failure, s.at(s.authority), ""))
}

func TestLoadContractNamesTheTriggerPaths(t *testing.T) {
	for _, p := range []string{
		"internal/spell/gen/decls/magus.buzz",
		"internal/interp/bindings/gen/boundary_objects.go",
		"std/gen/std-api.lock",
		"internal/commands/testdata/api-go.lock",
		"libs/gopherbuzz/checker_members.go",
		"libs/gopherbuzz/compiler.go",
	} {
		assert.True(t, loadContract(p), p)
	}
	for _, p := range []string{
		"run.go",
		"libs/gopherbuzz/checker_test.go",
		"libs/gopherbuzz/vm.go",
		"internal/spell/gen/types/quote.buzz",
		"docs/index.md",
	} {
		assert.False(t, loadContract(p), p)
	}
}

// contractCheckout is a checkout of magus with a base commit, then one commit changing the
// host declarations and one changing a doc, returning the three commits.
func contractCheckout(t *testing.T) (root string, base, decls, doc string) {
	t.Helper()
	root = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	git("init", "-q", "-b", "main")
	write("go.mod", fmt.Sprintf("module %s\n\ngo 1.26\n", ownModule))
	write("magus.yaml", "{}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	base = git("rev-parse", "HEAD")
	write("internal/spell/gen/decls/magus.buzz", "export fun x() > void {}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "decls")
	decls = git("rev-parse", "HEAD")
	write("docs/a.md", "a\n")
	git("add", "-A")
	git("commit", "-q", "-m", "doc")
	doc = git("rev-parse", "HEAD")
	git("reset", "-q", "--hard", base)
	return root, base, decls, doc
}

// A pick that changes the load contract is refused while the tree loads, served the dry
// run instead; the rest of a pick and an ordinary one run.
func TestContractPickIsRefusedOnlyForTheLoadContract(t *testing.T) {
	root, base, decls, doc := contractCheckout(t)
	at := location{workspace: root, dir: root}
	ctx := context.Background()
	deny := func(line string) string { return denyContractPick(ctx, Dependencies{}, at, line, DialectBash) }

	assert.Contains(t, deny("git cherry-pick "+decls), "changes the load contract (internal/spell/gen/decls/magus.buzz)")
	assert.Contains(t, deny(fmt.Sprintf("git cherry-pick -x %s..%s", base, doc)), "load contract")
	assert.Contains(t, deny("git merge "+decls), "load contract")
	assert.Empty(t, deny("git cherry-pick "+doc), "an ordinary pick")
	assert.Empty(t, deny("git cherry-pick --continue"))
	assert.Empty(t, deny("git merge --ff-only "+decls), "a fast-forward lands a tree something already built")
	assert.Empty(t, denyContractPick(ctx, Dependencies{LoadFailure: assert.AnError}, at, "git cherry-pick "+decls, DialectBash),
		"while the tree does not load, finishing the pick is the way out")
}
