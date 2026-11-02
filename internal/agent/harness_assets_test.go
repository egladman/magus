package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/json"
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
