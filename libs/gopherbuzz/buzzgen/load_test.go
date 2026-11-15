package buzzgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type LoadSuite struct {
	suite.Suite
	dir string
}

func TestLoad(t *testing.T) { suite.Run(t, new(LoadSuite)) }

func (s *LoadSuite) write(name, text string) {
	path := filepath.Join(s.dir, name)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, []byte(text), 0o644))
}

func (s *LoadSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.write("go.mod", "module example.com/m\n\ngo 1.21\n")
	s.write("envelope/envelope.go", "package envelope\n\ntype Kind string\n\ntype Phase string\n")
	s.write("spec/spec.go", `package spec

import "example.com/m/envelope"

const (
	KindJob   envelope.Kind = "Job"
	KindAgent envelope.Kind = "Agent"
)
`)
}

func (s *LoadSuite) program(text string) *Program {
	cfg, err := ParseConfig(strings.NewReader(text))
	s.Require().NoError(err)
	program, err := Load(s.dir, cfg)
	s.Require().NoError(err)
	return program
}

const casesFromConfig = `version: v1alpha1
packages:
  example.com/m/envelope:
    types:
      Kind: {config: {casesFrom: [example.com/m/spec], zeroCase: ""}}
      Phase: {config: {casesFrom: [example.com/m/spec], zeroCase: ""}}
`

func (s *LoadSuite) TestEnumReadsCasesFromAListedPackage() {
	program := s.program(casesFromConfig)
	refs := program.Config.Types()
	enum, err := program.Enum(refs[0])
	s.Require().NoError(err)
	s.Equal("Kind", enum.Name)
	s.Require().Len(enum.Cases, 2)
	s.Equal("job", enum.Cases[0].Name)
	s.Equal("Job", enum.Cases[0].Value)
	s.Equal("KindAgent", enum.Cases[1].Const)
}

func (s *LoadSuite) TestAnEnumWithNoCasesInTheListedPackagesIsRefused() {
	program := s.program(casesFromConfig)
	_, err := program.Enum(program.Config.Types()[1])
	s.ErrorContains(err, "declares no constants to derive cases from in example.com/m/spec")
}

func (s *LoadSuite) TestUnsetCasesFromReadsOnlyTheTypesOwnPackage() {
	program := s.program(`version: v1alpha1
packages:
  example.com/m/envelope:
    types:
      Kind: {config: {zeroCase: ""}}
`)
	_, err := program.Enum(program.Config.Types()[0])
	s.ErrorContains(err, "declares no constants to derive cases from in example.com/m/envelope")
}
