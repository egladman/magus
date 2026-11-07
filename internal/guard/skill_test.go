package guard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/hint"
)

type SkillGateSuite struct {
	suite.Suite
	workspace string
	markers   hint.Gate
}

func TestSkillGateSuite(t *testing.T) { suite.Run(t, new(SkillGateSuite)) }

func (s *SkillGateSuite) SetupTest() {
	s.workspace = s.T().TempDir()
	s.markers = hint.NewGate(s.T().TempDir(), "session-1")
}

func (s *SkillGateSuite) install(dir, skill string) {
	path := filepath.Join(s.workspace, dir, skill, "SKILL.md")
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, []byte("# skill\n"), 0o644))
}

func (s *SkillGateSuite) deny() string {
	return denyUntilSkillLoaded(s.markers, true, s.workspace, architectureSkill, "acting", "It carries the layers.")
}

func (s *SkillGateSuite) TestAnInstalledSkillNamesItsFileToRead() {
	s.install(".claude/skills", architectureSkill.String())
	s.Contains(s.deny(), "load Skill(magus-architecture-review) or read .claude/skills/magus-architecture-review/SKILL.md, then retry.")
}

func (s *SkillGateSuite) TestNoInstallNamesOnlyTheSkill() {
	s.Contains(s.deny(), "load Skill(magus-architecture-review), then retry.")
}

func (s *SkillGateSuite) TestAReadOfTheSkillFileClearsTheGate() {
	s.install(".claude/skills", architectureSkill.String())
	read := envelopeSkillRead(map[string]any{"file_path": filepath.Join(s.workspace, ".claude/skills/magus-architecture-review/SKILL.md")})
	s.Require().True(recordSkillLoad(s.markers, read))
	s.Empty(s.deny())
}
