package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type HarnessAssetsSuite struct{ suite.Suite }

func TestHarnessAssets(t *testing.T) { suite.Run(t, new(HarnessAssetsSuite)) }

func (s *HarnessAssetsSuite) TestBinaryPlanSurvivesJSON() {
	root := s.T().TempDir()
	want := []byte{0, 97, 115, 109, 1, 0, 0, 0, 255, 254, 128}
	plan := types.HarnessInstallation{ID: "opencode", Files: map[string]types.HarnessFile{".opencode/magus/plugin.wasm": {Bytes: want}}}
	encoded, err := json.Marshal(plan)
	s.Require().NoError(err)
	var decoded types.HarnessInstallation
	s.Require().NoError(json.Unmarshal(encoded, &decoded))
	paths, err := ApplyHarnessInstallation(context.Background(), root, decoded, false)
	s.Require().NoError(err)
	s.Equal([]string{".opencode/magus/plugin.wasm"}, paths)
	got, err := os.ReadFile(filepath.Join(root, paths[0]))
	s.Require().NoError(err)
	s.Equal(want, got)
}

func (s *HarnessAssetsSuite) TestDryRunWritesNothing() {
	root := s.T().TempDir()
	plan := types.HarnessInstallation{Files: map[string]types.HarnessFile{"runtime.wasm": {Bytes: []byte{0, 255}}}}
	paths, err := ApplyHarnessInstallation(context.Background(), root, plan, true)
	s.Require().NoError(err)
	s.Equal([]string{"runtime.wasm"}, paths)
	_, err = os.Stat(filepath.Join(root, "runtime.wasm"))
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *HarnessAssetsSuite) TestEmptyBinaryFileIsNotJSON() {
	root := s.T().TempDir()
	plan := types.HarnessInstallation{Files: map[string]types.HarnessFile{"empty": {Bytes: []byte{}}}}
	encoded, err := json.Marshal(plan)
	s.Require().NoError(err)
	s.Require().NoError(json.Unmarshal(encoded, &plan))
	_, err = ApplyHarnessInstallation(context.Background(), root, plan, false)
	s.Require().NoError(err)
	body, err := os.ReadFile(filepath.Join(root, "empty"))
	s.Require().NoError(err)
	s.Empty(body)
}

func (s *HarnessAssetsSuite) TestManagedUpgradeAndIdempotence() {
	root := s.T().TempDir()
	d := HarnessDescriptor{ID: "opencode", Assets: []HarnessRuntimeAsset{{Path: "runtime.wasm", Content: []byte{0, 255}}}}
	first := types.HarnessInstallation{}
	s.Require().NoError(planHarnessAssets(root, d, &first))
	_, err := ApplyHarnessInstallation(context.Background(), root, first, false)
	s.Require().NoError(err)
	current := types.HarnessInstallation{}
	s.Require().NoError(planHarnessAssets(root, d, &current))
	s.True(current.Current())
	d.Assets[0].Content = []byte{0, 254}
	next := types.HarnessInstallation{}
	s.Require().NoError(planHarnessAssets(root, d, &next))
	_, err = ApplyHarnessInstallation(context.Background(), root, next, false)
	s.Require().NoError(err)
	body, err := os.ReadFile(filepath.Join(root, "runtime.wasm"))
	s.Require().NoError(err)
	s.Equal([]byte{0, 254}, body)
}

func (s *HarnessAssetsSuite) TestUnknownEditsArePreserved() {
	root := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(root, "runtime.wasm"), []byte("mine"), 0600))
	d := HarnessDescriptor{ID: "opencode", Assets: []HarnessRuntimeAsset{{Path: "runtime.wasm", Content: []byte{0, 255}}}}
	plan := types.HarnessInstallation{}
	s.ErrorContains(planHarnessAssets(root, d, &plan), "unrecognized local changes")
	body, err := os.ReadFile(filepath.Join(root, "runtime.wasm"))
	s.Require().NoError(err)
	s.Equal("mine", string(body))
}

func (s *HarnessAssetsSuite) TestSymlinkDestinationIsRefused() {
	root := s.T().TempDir()
	elsewhere := s.T().TempDir()
	s.Require().NoError(os.Symlink(elsewhere, filepath.Join(root, "runtime")))
	plan := types.HarnessInstallation{Files: map[string]types.HarnessFile{"runtime/plugin.wasm": {Bytes: []byte{0, 255}}}}
	_, err := ApplyHarnessInstallation(context.Background(), root, plan, false)
	s.ErrorContains(err, "symlink")
	_, err = os.Stat(filepath.Join(elsewhere, "plugin.wasm"))
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *HarnessAssetsSuite) TestMissingPluginIsUncovered() {
	d := HarnessDescriptor{ID: "opencode", Guard: "plugin", Probe: []string{"missing-probe"}, Assets: []HarnessRuntimeAsset{{Path: "plugin.wasm", Content: []byte{0, 255}}}}
	got, err := verifyHarnessPlugin(context.Background(), s.T().TempDir(), d, HarnessVerification{})
	s.Require().NoError(err)
	s.Equal(HarnessUncovered, got.Status)
}

type HarnessSourceLinksSuite struct{ suite.Suite }

func TestHarnessSourceLinks(t *testing.T) { suite.Run(t, new(HarnessSourceLinksSuite)) }

func (s *HarnessSourceLinksSuite) write(path, body string) {
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, []byte(body), 0o644))
}

func (s *HarnessSourceLinksSuite) install(root string, links map[string]string) ([]string, error) {
	return InstallHarnessSourceLinks(context.Background(), root, links, false)
}

func (s *HarnessSourceLinksSuite) TestInstallAndAreIdempotent() {
	root := s.T().TempDir()
	s.write(filepath.Join(root, "source", "file.txt"), "source")

	links := map[string]string{".claude/skills/shared": "source"}
	files, err := s.install(root, links)
	s.Require().NoError(err)
	s.Equal([]string{".claude/skills/shared"}, files)
	link, err := os.Readlink(filepath.Join(root, ".claude/skills/shared"))
	s.Require().NoError(err)
	s.False(filepath.IsAbs(link), "new links use relative targets")
	s.FileExists(filepath.Join(root, ".claude/skills/shared/file.txt"))

	files, err = s.install(root, links)
	s.Require().NoError(err)
	s.Empty(files)
}

func (s *HarnessSourceLinksSuite) TestAcceptMatchingRelativeAndAbsoluteSymlinks() {
	root := s.T().TempDir()
	s.write(filepath.Join(root, "src", "file"), "ok")
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "dest"), 0o755))
	s.Require().NoError(os.Symlink("../src", filepath.Join(root, "dest", "relative")))
	s.Require().NoError(os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "dest", "absolute")))

	files, err := s.install(root, map[string]string{"dest/relative": "src", "dest/absolute": "src"})
	s.Require().NoError(err)
	s.Empty(files)
}

func (s *HarnessSourceLinksSuite) TestKeepEqualLegacyFiles() {
	root := s.T().TempDir()
	s.write(filepath.Join(root, "source"), "same")
	destination := filepath.Join(root, "dest", "source")
	s.Require().NoError(os.MkdirAll(filepath.Dir(destination), 0o755))
	s.Require().NoError(os.WriteFile(destination, []byte("same"), 0o600))

	files, err := s.install(root, map[string]string{"dest/source": "source"})
	s.Require().NoError(err)
	s.Empty(files)
	info, err := os.Lstat(destination)
	s.Require().NoError(err)
	s.True(info.Mode().IsRegular())
	s.Equal(os.FileMode(0o600), info.Mode().Perm())
}

// conflict plants an entry at dest/entry and requires the install to refuse it.
func (s *HarnessSourceLinksSuite) conflict(plant func(dest string)) string {
	root := s.T().TempDir()
	s.write(filepath.Join(root, "source"), "new")
	dest := filepath.Join(root, "dest", "entry")
	s.Require().NoError(os.MkdirAll(filepath.Dir(dest), 0o755))
	plant(dest)
	_, err := s.install(root, map[string]string{"dest/entry": "source"})
	s.Require().Error(err)
	return dest
}

func (s *HarnessSourceLinksSuite) TestRefuseConflictingFile() {
	dest := s.conflict(func(dest string) { s.Require().NoError(os.WriteFile(dest, []byte("keep"), 0o644)) })
	data, err := os.ReadFile(dest)
	s.Require().NoError(err)
	s.Equal("keep", string(data))
}

func (s *HarnessSourceLinksSuite) TestRefuseConflictingLink() {
	dest := s.conflict(func(dest string) { s.Require().NoError(os.Symlink("other", dest)) })
	link, err := os.Readlink(dest)
	s.Require().NoError(err)
	s.Equal("other", link)
}

func (s *HarnessSourceLinksSuite) TestRefuseDanglingLink() {
	dest := s.conflict(func(dest string) { s.Require().NoError(os.Symlink("missing", dest)) })
	link, err := os.Readlink(dest)
	s.Require().NoError(err)
	s.Equal("missing", link)
}

// unsafeRoot is a workspace whose escape and escape-parent point outside it.
func (s *HarnessSourceLinksSuite) unsafeRoot() string {
	root := s.T().TempDir()
	s.write(filepath.Join(root, "source"), "ok")
	outside := s.T().TempDir()
	s.Require().NoError(os.Symlink(outside, filepath.Join(root, "escape")))
	s.Require().NoError(os.Symlink(outside, filepath.Join(root, "escape-parent")))
	return root
}

func (s *HarnessSourceLinksSuite) TestRefuseParentTraversalDestination() {
	_, err := s.install(s.unsafeRoot(), map[string]string{"../outside": "source"})
	s.Error(err)
}

func (s *HarnessSourceLinksSuite) TestRefuseParentTraversalSource() {
	_, err := s.install(s.unsafeRoot(), map[string]string{"safe": "../outside"})
	s.Error(err)
}

func (s *HarnessSourceLinksSuite) TestRefuseAbsoluteDestination() {
	root := s.unsafeRoot()
	_, err := s.install(root, map[string]string{filepath.Join(root, "safe"): "source"})
	s.Error(err)
}

func (s *HarnessSourceLinksSuite) TestRefuseGitComponent() {
	_, err := s.install(s.unsafeRoot(), map[string]string{".git/hooks/link": "source"})
	s.Error(err)
}

func (s *HarnessSourceLinksSuite) TestRefuseSymlinkEscape() {
	_, err := s.install(s.unsafeRoot(), map[string]string{"safe": "escape"})
	s.Error(err)
}

func (s *HarnessSourceLinksSuite) TestRefuseDestinationParentSymlinkEscape() {
	_, err := s.install(s.unsafeRoot(), map[string]string{"escape-parent/link": "source"})
	s.Error(err)
}

func (s *HarnessSourceLinksSuite) TestRefuseMissingSource() {
	_, err := s.install(s.unsafeRoot(), map[string]string{"safe": "missing"})
	s.Error(err)
}

func (s *HarnessSourceLinksSuite) TestRejectSpecialFileSources() {
	device := filepath.Join(string(filepath.Separator), "dev", "null")
	info, err := os.Stat(device)
	if err != nil || info.Mode().IsRegular() || info.IsDir() {
		s.T().Skip("platform has no /dev/null device file")
	}
	root := s.T().TempDir()
	s.Require().NoError(os.Symlink(device, filepath.Join(root, "device")))
	_, err = s.install(root, map[string]string{"destination": "device"})
	s.Require().Error(err)
	s.Contains(err.Error(), `source "device" must be a regular file or directory`)
	s.NoFileExists(filepath.Join(root, "destination"))
}

func (s *HarnessSourceLinksSuite) TestPreflightWholeBatchBeforeWriting() {
	root := s.T().TempDir()
	s.write(filepath.Join(root, "source"), "new")
	s.write(filepath.Join(root, "existing"), "keep")
	s.write(filepath.Join(root, "z", "conflict"), "keep")
	_, err := s.install(root, map[string]string{"a/new/link": "source", "z/conflict": "source"})
	s.Require().Error(err)
	s.NoDirExists(filepath.Join(root, "a"))
	data, err := os.ReadFile(filepath.Join(root, "existing"))
	s.Require().NoError(err)
	s.Equal("keep", string(data))
}

func (s *HarnessSourceLinksSuite) TestDryRunWritesNothing() {
	root := s.T().TempDir()
	s.write(filepath.Join(root, "source"), "ok")
	files, err := InstallHarnessSourceLinks(context.Background(), root, map[string]string{"new/dir/link": "source"}, true)
	s.Require().NoError(err)
	s.Equal([]string{"new/dir/link"}, files)
	s.NoDirExists(filepath.Join(root, "new"))
}

func (s *HarnessSourceLinksSuite) TestRespectCancellationBeforeWrites() {
	root := s.T().TempDir()
	s.write(filepath.Join(root, "source"), "ok")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := InstallHarnessSourceLinks(ctx, root, map[string]string{"new/link": "source"}, false)
	s.Require().Error(err)
	s.NoDirExists(filepath.Join(root, "new"))
}

func (s *HarnessSourceLinksSuite) TestSkipSameActualDirectory() {
	root := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "dir"), 0o755))
	files, err := s.install(root, map[string]string{"dir": "dir"})
	s.Require().NoError(err)
	s.Empty(files)
	info, err := os.Lstat(filepath.Join(root, "dir"))
	s.Require().NoError(err)
	s.True(info.IsDir())
}

func (s *HarnessSourceLinksSuite) TestRejectDestinationAncestorOverlap() {
	root := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "src"), 0o755))
	_, err := s.install(root, map[string]string{"dest": "src", "dest/child": "src"})
	s.Error(err)
	s.NoDirExists(filepath.Join(root, "dest"))
}

func (s *HarnessSourceLinksSuite) TestPreserveProtectedPolicyBoundary() {
	root := s.T().TempDir()
	s.write(filepath.Join(root, "source", "skill.md"), "skill")
	policy := filesystem.Ruleset{Rules: []filesystem.Rule{{Path: filesystem.ResolveRulePath(filepath.Join(root, "source")), Read: true, Write: true}}}
	destination := filepath.Join(root, ".claude/skills/team")
	s.ErrorIs(policy.Check(destination, filesystem.Write), filesystem.ErrDenied,
		"ordinary filesystem writes stay inside the policy's declared source path")
	files, err := s.install(root, map[string]string{".claude/skills/team": "source"})
	s.Require().NoError(err)
	s.Equal([]string{".claude/skills/team"}, files)
	s.FileExists(filepath.Join(root, ".claude/skills/team/skill.md"))
	expected, err := filepath.EvalSymlinks(filepath.Join(root, "source"))
	s.Require().NoError(err)
	resolved, err := filepath.EvalSymlinks(destination)
	s.Require().NoError(err)
	s.Equal(expected, resolved, "the explicit installer links only to the in-root source")
}

func (s *HarnessSourceLinksSuite) TestRefuseGitSourceAliasBeforeWriting() {
	root := s.T().TempDir()
	s.write(filepath.Join(root, ".git", "private", "source"), "private")
	s.write(filepath.Join(root, "source"), "safe")
	s.Require().NoError(os.Symlink(filepath.Join(".git", "private", "source"), filepath.Join(root, "git-source")))

	_, err := s.install(root, map[string]string{"a/new/link": "source", "z/hidden-source": "git-source"})
	s.Require().Error(err)
	s.NoDirExists(filepath.Join(root, "a"))
}

func (s *HarnessSourceLinksSuite) TestRefuseGitParentAliasBeforeWriting() {
	root := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(root, ".git", "private"), 0o755))
	s.write(filepath.Join(root, "source"), "safe")
	s.Require().NoError(os.Symlink(filepath.Join(".git", "private"), filepath.Join(root, "git-parent")))

	_, err := s.install(root, map[string]string{"a/new/link": "source", "git-parent/link": "source"})
	s.Require().Error(err)
	s.NoDirExists(filepath.Join(root, "a"))
	s.NoFileExists(filepath.Join(root, ".git", "private", "link"))
}
