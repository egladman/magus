package proc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/buildinfo"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// devVersionSentinel's doc says to keep it in sync with ward.DevVersion (and cmd/magus's
// unknownVersion, checked by TestUnknownVersionMatchesWardDevVersion in cmd/magus). Neither
// half of that pair could see the other without this test: cmd/magus cannot see proc's
// unexported sentinel, so each package only proves the half reachable from ward.DevVersion.
func TestDevVersionSentinelMatchesWard(t *testing.T) {
	assert.Equal(t, ward.DevVersion, devVersionSentinel)
}

// gateIdentitySuite pins the adoption identity each kind of build gates on.
type gateIdentitySuite struct {
	suite.Suite
}

func TestGateIdentitySuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(gateIdentitySuite))
}

// vcsBuild is an unstamped build that fell back to Go's VCS stamp.
func vcsBuild(commit string, modified bool) types.MagusBuild {
	return types.MagusBuild{Version: devVersionSentinel, Commit: commit, Date: "unknown", Source: types.BuildSourceVCS, Dirty: modified}
}

func (s *gateIdentitySuite) TestTheEmptyVersionDisablesTheGateForAnyBuild() {
	s.Empty(gateIdentity("", types.StampedBuild("v0.4.2-dirty", "6bfd32710", "")))
	s.Empty(gateIdentity("", types.MagusBuild{}))
}

func (s *gateIdentitySuite) TestAStampedCleanVersionPassesThrough() {
	for _, v := range []string{"v0.4.2", "v0.4.2-3-gabc123", "test"} {
		s.Equal(v, gateIdentity(v, types.StampedBuild(v, "abc123", "")))
	}
}

// A stamped clean build in a linked worktree carries a VCS stamp that says modified;
// [buildinfo.Self] reads the stamp first, so the build keeps its shared version and a
// copied ./magus still adopts into a server built at the same commit.
func (s *gateIdentitySuite) TestAStampedCleanBuildIsNotJudgedByItsVCSStamp() {
	b := types.StampedBuild("v0.5.0-rc.3-205-g33268e391", "33268e391", "")

	s.Equal("v0.5.0-rc.3-205-g33268e391", gateIdentity(b.Version, b))
}

// Every dirty build of one commit shares its `git describe --dirty` string while running
// different code, so a dirty build identifies as the executable file it runs from.
func (s *gateIdentitySuite) TestAStampedDirtyBuildIdentifiesAsItsFile() {
	b := types.StampedBuild("v0.4.2-2-g6bfd32710-dirty", "6bfd32710", "")

	s.Equal(buildinfo.File(), gateIdentity(b.Version, b))
}

func (s *gateIdentitySuite) TestAnUnstampedCleanBuildFallsBackToItsVCSCommit() {
	s.Equal("dev-abc123", gateIdentity(devVersionSentinel, vcsBuild("abc123", false)))
}

func (s *gateIdentitySuite) TestAnUnstampedModifiedBuildIdentifiesAsItsFile() {
	s.Equal(buildinfo.File(), gateIdentity(devVersionSentinel, vcsBuild("abc123", true)))
}

func (s *gateIdentitySuite) TestABuildWithNoIdentityNeverAdopts() {
	b := types.MagusBuild{Version: devVersionSentinel, Commit: "unknown", Date: "unknown"}

	s.Equal(buildinfo.Unverifiable(), gateIdentity(devVersionSentinel, b))
}

// Whatever tree built the test binary, a dev build never keeps the shared placeholder.
func (s *gateIdentitySuite) TestThisBinaryIsNeverIdentifiedByThePlaceholder() {
	got := adoptionIdentity(devVersionSentinel)

	s.NotEqual(devVersionSentinel, got)
	s.NotEmpty(got)
}

// versionAdmits is the shared gate. It admits when either side is empty (check disabled) or
// the two identities match exactly, and refuses otherwise. Asserting it directly documents
// the full truth table independent of the wire round-trip.
func TestVersionAdmits(t *testing.T) {
	cases := []struct {
		name       string
		gate, req  string
		wantAdmits bool
	}{
		{"both empty passes (test escape)", "", "", true},
		{"empty gate disables the check", "", "dev-abc", true},
		{"empty request passes (pre-versioning client)", "dev-abc", "", true},
		{"same identity adopts", "dev-abc", "dev-abc", true},
		{"different dev revisions refuse", "dev-abc", "dev-def", false},
		{"release vs different release refuses", "v0.4.2", "v0.4.1", false},
		{"same release adopts", "v0.4.2", "v0.4.2", true},
		{"release vs dev refuses", "v0.4.2", "dev-abc", false},
	}
	for _, tc := range cases {
		s := &service{gateVersion: tc.gate}
		assert.Equalf(t, tc.wantAdmits, s.versionAdmits(tc.req), "%s", tc.name)
	}
}
