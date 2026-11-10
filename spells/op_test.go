package spells

import (
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// TestSpellOpKind covers OpKind's empty-default resolution and the IsService
// discriminator across the three op shapes (default command, explicit command,
// service).
func TestOpKind(t *testing.T) {
	tests := []struct {
		name     string
		op       Op
		wantKind string
		wantSvc  bool
	}{
		{"empty defaults to command", Op{}, OpKindCommand, false},
		{"explicit command", Op{Kind: OpKindCommand}, OpKindCommand, false},
		{"service", Op{Kind: OpKindService}, OpKindService, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantKind, tt.op.OpKind())
			assert.Equal(t, tt.wantSvc, tt.op.IsService())
		})
	}
}

// TestCommandSecretsJSONRoundTrip pins the descriptor cache-correctness contract: a
// Command's Secrets field carries its own json tag ("secrets") through Marshal and
// Unmarshal untouched, so BuiltinsHash (internal/spell.BuiltinsHash, hashed
// from json.Marshal(Builtins())) and any other descriptor serialization move when a
// declared ref changes: a spell editing which secret an op needs invalidates the
// cache keys that depend on the spell's identity, the same as editing its Bin/Args
// does.
func TestCommandSecretsJSONRoundTrip(t *testing.T) {
	c := Command{
		Bin:     "npm",
		Args:    []string{"publish"},
		Secrets: map[string]string{"NPM_TOKEN": "NPM_TOKEN"},
	}
	b, err := json.Marshal(c)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"secrets":{"NPM_TOKEN":"NPM_TOKEN"}`)

	var got Command
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, c, got)
}

// TestCommandSecretsOmittedWhenEmpty pins the other half of the cache-correctness
// contract: an op declaring no secrets must not move the hash at all, which requires
// the field to actually omit (omitempty), not merely round-trip as an empty map.
func TestCommandSecretsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(Command{Bin: "go", Args: []string{"build"}})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "secrets", "an op with no declared secrets must not carry the key at all")
}

func TestCommandValidateQuiet(t *testing.T) {
	require.NoError(t, Command{Bin: "go"}.Validate())
	require.NoError(t, Command{Bin: "go", Capture: true}.Validate())
	require.NoError(t, Command{Bin: "go", Capture: true, Quiet: true}.Validate())
	require.ErrorContains(t, Command{Bin: "go", Quiet: true}.Validate(), "quiet without capture")
}

func TestCommandValidateSourcesEachWithoutSourcesRefused(t *testing.T) {
	require.ErrorContains(t, Command{Bin: "gofmt", SourcesEach: true}.Validate(), "sources_each without sources")
}

func TestCommandValidateSourcesEachWithSourcesAccepted(t *testing.T) {
	require.NoError(t, Command{Bin: "gofmt", Sources: []string{"**/*.go"}, SourcesEach: true}.Validate())
}

// TestServiceValidate pins the shape rules a Service is resolved against: exactly one
// of Command or Start, and a Start service declares both how to tell it is up and how
// to stop it, since the supervisor holds none of its processes.
func TestServiceValidate(t *testing.T) {
	cmd := Command{Bin: "postgres"}
	start := Command{Bin: "podman", Args: []string{"machine", "start"}}
	probe := Command{Bin: "podman", Args: []string{"info"}}
	stop := Command{Bin: "podman", Args: []string{"machine", "stop"}}
	tests := []struct {
		name    string
		svc     Service
		wantErr string
	}{
		{"command alone", Service{Command: cmd}, ""},
		{"start with readiness and stop", Service{Start: start, Readiness: probe, Stop: stop}, ""},
		{"neither", Service{Readiness: probe}, "service declares neither command nor start, set exactly one"},
		{"both", Service{Command: cmd, Start: start, Readiness: probe, Stop: stop}, "service declares both command and start, set exactly one"},
		{"start without readiness", Service{Start: start, Stop: stop}, "a start service needs readiness: it is how magus learns the service is up without holding its process"},
		{"start without stop", Service{Start: start, Readiness: probe}, "a start service needs stop: magus holds no process to signal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.svc.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

type commandSelectionSuite struct{ suite.Suite }

func TestCommandSelection(t *testing.T) { suite.Run(t, new(commandSelectionSuite)) }
func (s *commandSelectionSuite) TestGoDialectIsValid() {
	s.NoError(Command{TestSelection: "go"}.Validate())
}
func (s *commandSelectionSuite) TestEmptyDeclaresNoSelector() { s.NoError(Command{}.Validate()) }
func (s *commandSelectionSuite) TestUnknownDialectIsRejected() {
	s.ErrorContains(Command{TestSelection: "g0"}.Validate(), `unsupported test_selection "g0"`)
}
func (s *commandSelectionSuite) TestDialectSurvivesJSON() {
	original := Command{Bin: "go", Args: []string{"test"}, TestSelection: "go"}
	data, err := json.Marshal(original)
	s.Require().NoError(err)
	s.Contains(string(data), `"test_selection":"go"`)
	var decoded Command
	s.Require().NoError(json.Unmarshal(data, &decoded))
	s.Equal(original, decoded)
}

type commandTestScopeSuite struct{ suite.Suite }

func TestCommandTestScope(t *testing.T) { suite.Run(t, new(commandTestScopeSuite)) }

func (s *commandTestScopeSuite) TestAcceptsEveryPlaceholder() {
	s.NoError(Command{TestScope: []string{"./{dir}", "{file}", "{dir}/{stem}_test.buzz"}}.Validate())
}

func (s *commandTestScopeSuite) TestRefusesAnUnknownPlaceholder() {
	s.EqualError(Command{TestScope: []string{"./{package}"}}.Validate(),
		`test_scope "./{package}": placeholder {package}, want {dir}, {file} or {stem}`)
}

func (s *commandTestScopeSuite) TestRefusesAnEmptyEntry() {
	s.EqualError(Command{TestScope: []string{""}}.Validate(), "test_scope[0] is empty")
}

func (s *commandTestScopeSuite) TestExpandsEachPlaceholder() {
	c := Command{TestScope: []string{"./{dir}", "{file}", "{dir}/{stem}_test.buzz"}}
	s.Equal([]string{"./std/lib", "std/lib/fmt.buzz", "std/lib/fmt_test.buzz"}, c.ExpandTestScope("std/lib/fmt.buzz"))
}

func (s *commandTestScopeSuite) TestExpandsAFileAtTheProjectRoot() {
	s.Equal([]string{"./."}, Command{TestScope: []string{"./{dir}"}}.ExpandTestScope("main.go"))
}

func (s *commandTestScopeSuite) TestExpandsNothingWhenUndeclared() {
	s.Empty(Command{}.ExpandTestScope("main.go"))
}

type commandPathScopeSuite struct{ suite.Suite }

func TestCommandPathScope(t *testing.T) { suite.Run(t, new(commandPathScopeSuite)) }

func goPackageScope() Command {
	return Command{PathScope: []string{"./{dir}"}, PathScopeFiles: []string{"**/*.go", "!**/testdata/**"}}
}

func (s *commandPathScopeSuite) TestMapsAFileToItsPackage() {
	operands, keep := goPackageScope().ScopePath("buzzgen/load.go")
	s.True(keep)
	s.Equal([]string{"./buzzgen"}, operands)
}

func (s *commandPathScopeSuite) TestMapsAFileAtTheRoot() {
	operands, keep := goPackageScope().ScopePath("main.go")
	s.True(keep)
	s.Equal([]string{"./."}, operands)
}

func (s *commandPathScopeSuite) TestDropsAFileNoGlobMatches() {
	operands, keep := goPackageScope().ScopePath("README.md")
	s.False(keep)
	s.Empty(operands)
}

func (s *commandPathScopeSuite) TestDropsAnExcludedFile() {
	_, keep := goPackageScope().ScopePath("buzzgen/testdata/src/a.go")
	s.False(keep)
}

func (s *commandPathScopeSuite) TestValidates() {
	s.NoError(goPackageScope().Validate())
}

func (s *commandPathScopeSuite) TestRefusesAScopeWithoutFiles() {
	s.EqualError(Command{PathScope: []string{"./{dir}"}}.Validate(),
		"path_scope without path_scope_files maps no file, set the globs of the files it maps")
}

func (s *commandPathScopeSuite) TestRefusesFilesWithoutAScope() {
	s.EqualError(Command{PathScopeFiles: []string{"**/*.go"}}.Validate(),
		"path_scope_files without path_scope maps its files to nothing, set path_scope or drop path_scope_files")
}

func (s *commandPathScopeSuite) TestRefusesOnlyExclusions() {
	s.EqualError(Command{PathScope: []string{"./{dir}"}, PathScopeFiles: []string{"!**/testdata/**"}}.Validate(),
		"path_scope_files holds only exclusions, so it maps no file")
}

func (s *commandPathScopeSuite) TestRefusesAnUnknownPlaceholder() {
	s.EqualError(Command{PathScope: []string{"./{package}"}, PathScopeFiles: []string{"**/*.go"}}.Validate(),
		`path_scope "./{package}": placeholder {package}, want {dir}, {file} or {stem}`)
}

func (s *commandPathScopeSuite) TestRefusesAnInvalidGlob() {
	s.EqualError(Command{PathScope: []string{"./{dir}"}, PathScopeFiles: []string{"[*.go"}}.Validate(),
		`path_scope_files[0] "[*.go" is not a valid glob`)
}

func (s *commandPathScopeSuite) TestSurvivesJSON() {
	original := goPackageScope()
	data, err := json.Marshal(original)
	s.Require().NoError(err)
	s.Contains(string(data), `"path_scope":["./{dir}"],"path_scope_files":["**/*.go","!**/testdata/**"]`)
	var decoded Command
	s.Require().NoError(json.Unmarshal(data, &decoded))
	s.Equal(original, decoded)
}

func (s *commandTestScopeSuite) TestSurvivesJSON() {
	original := Command{Bin: "go", TestScope: []string{"./{dir}"}}
	data, err := json.Marshal(original)
	s.Require().NoError(err)
	s.Contains(string(data), `"test_scope":["./{dir}"]`)
	var decoded Command
	s.Require().NoError(json.Unmarshal(data, &decoded))
	s.Equal(original, decoded)
}
