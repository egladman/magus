package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/spells"
)

type discoverySuite struct {
	suite.Suite
	golang, typescript, bash spells.Descriptor
}

func TestDiscoverySuite(t *testing.T) { suite.Run(t, new(discoverySuite)) }

func (s *discoverySuite) SetupTest() {
	s.golang = spells.Descriptor{
		Name:       "go",
		Needs:      []string{"**/*.go", "go.mod"},
		IgnoreDirs: []string{"vendor"},
		Ops:        map[string]spells.Op{"go-build": {}, "go-test": {}, "go-vet": {}},
	}
	s.typescript = spells.Descriptor{
		Name:       "typescript",
		Needs:      []string{"**/*.ts", "package.json"},
		IgnoreDirs: []string{"node_modules"},
		Ops:        map[string]spells.Op{"tsc-build": {}, "vitest": {}, "eslint": {}},
	}
	s.bash = spells.Descriptor{
		Name:  "bash",
		Needs: []string{"**/*.sh"},
		Ops:   map[string]spells.Op{"shellcheck": {}},
	}
}

func (s *discoverySuite) goRoot() spells.DiscoveredProject {
	return spells.DiscoveredProject{
		Path:    ".",
		Role:    spells.DiscoveryRoleProject,
		Targets: map[string][]string{"build": {"go-build"}, "test": {"go-test"}, "lint": {"go-vet"}},
	}
}

func (s *discoverySuite) tsAt(dir string) spells.DiscoveredProject {
	return spells.DiscoveredProject{
		Path:    dir,
		Role:    spells.DiscoveryRoleProject,
		Targets: map[string][]string{"build": {"tsc-build"}, "test": {"vitest"}, "lint": {"eslint"}},
	}
}

func (s *discoverySuite) bashMember(dir string) spells.DiscoveredProject {
	return spells.DiscoveredProject{Path: dir, Role: spells.DiscoveryRoleMember, Targets: map[string][]string{"lint": {"shellcheck"}}}
}

// merge asks every spell in the suite and merges answers, with no magusfile present.
func (s *discoverySuite) merge(answers ...spellAnswer) discoveryResult {
	d, err := mergeDiscovery(s.asked(), answers, nil)
	s.Require().NoError(err)
	return d
}

func (s *discoverySuite) asked() discoveryInput {
	return discoveryInput{Queries: map[string]spells.DiscoveryQuery{"go": {}, "typescript": {}, "bash": {}}}
}

func (s *discoverySuite) findings(fs []discoveryFinding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.String())
	}
	return out
}

func (s *discoverySuite) TestMixedRootIsOneProjectWithEverySpellBound() {
	d := s.merge(
		spellAnswer{Spell: s.typescript, Claims: []spells.DiscoveredProject{s.tsAt(".")}},
		spellAnswer{Spell: s.golang, Claims: []spells.DiscoveredProject{s.goRoot()}},
		spellAnswer{Spell: s.bash, Claims: []spells.DiscoveredProject{s.bashMember("scripts")}},
	)
	s.Require().Len(d.Projects, 1)
	p := d.Projects[0]
	s.Equal(".", p.Path)
	s.Equal([]string{"bash", "go", "typescript"}, p.Spells)
	s.Equal("magusfile.buzz", p.Magusfile())
	s.Equal(`import "magus";

import "magus/spell/bash";
import "magus/spell/go";
import "magus/spell/typescript";

magus\project({
    "spells": [bash, go, typescript],
});

export fun lint(ctx: magus\Context, args: [str]) > void {
    bash["shellcheck"](ctx);
    go["go-vet"](ctx);
    typescript["eslint"](ctx);
}

export fun build(ctx: magus\Context, args: [str]) > void {
    go["go-build"](ctx);
    typescript["tsc-build"](ctx);
}

export fun test(ctx: magus\Context, args: [str]) > void {
    go["go-test"](ctx);
    typescript["vitest"](ctx);
}

export fun ci(ctx: magus\Context, args: [str]) > void {
    ctx.needs(lint, build, test);
}
`, p.Source)
	s.Empty(d.Gaps)
}

func (s *discoverySuite) TestSameNamedTargetsReportTheirOrderAndTheLineThatChangesIt() {
	d := s.merge(
		spellAnswer{Spell: s.typescript, Claims: []spells.DiscoveredProject{s.tsAt(".")}},
		spellAnswer{Spell: s.golang, Claims: []spells.DiscoveredProject{s.goRoot()}},
		spellAnswer{Spell: s.bash, Claims: []spells.DiscoveredProject{s.bashMember("scripts")}},
	)
	s.Equal([]string{
		".: lint runs bash then go then typescript (several spells contribute lint, and they run in spell-name order); settle: reorder the calls at magusfile.buzz:11",
		".: build runs go then typescript (several spells contribute build, and they run in spell-name order); settle: reorder the calls at magusfile.buzz:17",
		".: test runs go then typescript (several spells contribute test, and they run in spell-name order); settle: reorder the calls at magusfile.buzz:22",
	}, s.findings(d.Notes))
}

func (s *discoverySuite) TestDisagreeingNamesFallBackToThePath() {
	golang := s.goRoot()
	golang.Name = "github.com/x/y"
	ts := s.tsAt(".")
	ts.Name = "@x/web"
	d := s.merge(spellAnswer{Spell: s.golang, Claims: []spells.DiscoveredProject{golang}}, spellAnswer{Spell: s.typescript, Claims: []spells.DiscoveredProject{ts}})
	s.Require().Len(d.Projects, 1)
	s.Empty(d.Projects[0].Name)
	s.NotContains(d.Projects[0].Source, `"name"`)
	s.Contains(s.findings(d.Notes), `.: named by its path (spells disagree on the name: go says "github.com/x/y", typescript says "@x/web"); settle: magus\project({"name": ...}) in magusfile.buzz`)
}

func (s *discoverySuite) TestOneNameIsRendered() {
	golang := s.goRoot()
	golang.Name = "api"
	d := s.merge(spellAnswer{Spell: s.golang, Claims: []spells.DiscoveredProject{golang}})
	s.Equal("api", d.Projects[0].Name)
	s.Contains(d.Projects[0].Source, `    "name": "api",`)
}

func (s *discoverySuite) TestASpanOverANestedProjectBecomesADependency() {
	golang := s.goRoot()
	golang.Spans = []string{"web"}
	d := s.merge(spellAnswer{Spell: s.golang, Claims: []spells.DiscoveredProject{golang}}, spellAnswer{Spell: s.typescript, Claims: []spells.DiscoveredProject{s.tsAt("web")}})
	s.Require().Len(d.Projects, 2)
	s.Equal([]string{"web"}, d.Projects[0].DependsOn)
	s.Contains(d.Projects[0].Source, `    "depends_on": ["web"],`)
	s.Contains(s.findings(d.Notes), ".: depends on web (spell go spans web, which web claims); settle: depends_on in magusfile.buzz")
	s.Empty(d.Projects[1].DependsOn)
}

func (s *discoverySuite) TestASpanNobodyClaimsAddsNoEdge() {
	golang := s.goRoot()
	golang.Spans = []string{"web"}
	d := s.merge(spellAnswer{Spell: s.golang, Claims: []spells.DiscoveredProject{golang}})
	s.Empty(d.Projects[0].DependsOn)
	s.Empty(d.Notes)
}

func (s *discoverySuite) TestASpanThatWouldCloseACycleIsDroppedAndReported() {
	golang := s.goRoot()
	golang.Spans = []string{"web"}
	web := s.tsAt("web")
	web.DependsOn = []string{"."}
	d := s.merge(spellAnswer{Spell: s.golang, Claims: []spells.DiscoveredProject{golang}}, spellAnswer{Spell: s.typescript, Claims: []spells.DiscoveredProject{web}})
	s.Empty(d.Projects[0].DependsOn)
	s.Equal([]string{"."}, d.Projects[1].DependsOn)
	s.Contains(d.Projects[1].Source, `    "depends_on": [".."],`)
	s.Equal([]string{
		".: no depends_on web (spell go spans web, which web claims, but web already reaches ., so the edge would close a cycle); settle: an explicit depends_on in magusfile.buzz",
	}, s.findings(d.Gaps))
}

func (s *discoverySuite) TestADependencyOnNoProjectIsReported() {
	golang := s.goRoot()
	golang.DependsOn = []string{"libs/proto"}
	d := s.merge(spellAnswer{Spell: s.golang, Claims: []spells.DiscoveredProject{golang}})
	s.Empty(d.Projects[0].DependsOn)
	s.Equal([]string{".: no depends_on libs/proto (no project is there); settle: an explicit depends_on in magusfile.buzz"}, s.findings(d.Gaps))
}

func (s *discoverySuite) TestANestedDependencyIsDotRelative() {
	d := s.merge(spellAnswer{Spell: s.golang, Claims: []spells.DiscoveredProject{
		{Path: "libs/a", Role: spells.DiscoveryRoleProject, DependsOn: []string{"libs/b"}, Targets: map[string][]string{"build": {"go-build"}}},
		{Path: "libs/b", Role: spells.DiscoveryRoleProject, Targets: map[string][]string{"build": {"go-build"}}},
	}})
	s.Contains(d.Projects[0].Source, `    "depends_on": ["../../libs/b"],`)
}

func (s *discoverySuite) TestAProjectWithNoTargetIsAGap() {
	d := s.merge(spellAnswer{Spell: s.typescript, Claims: []spells.DiscoveredProject{{Path: "web", Role: spells.DiscoveryRoleProject}}})
	s.Require().Len(d.Projects, 1)
	s.Equal([]string{"web: no lifecycle target (spell typescript formed the project and inferred none); settle: write the targets in web/magusfile.buzz"}, s.findings(d.Gaps))
}

func (s *discoverySuite) TestAProjectWithNoCITargetIsAGap() {
	d := s.merge(spellAnswer{Spell: s.bash, Claims: []spells.DiscoveredProject{{Path: ".", Role: spells.DiscoveryRoleProject, Targets: map[string][]string{"format": {"shellcheck"}}}}})
	s.NotContains(d.Projects[0].Source, "export fun ci")
	s.Equal([]string{".: no ci (none of lint, build, test was inferred); settle: write ci in magusfile.buzz"}, s.findings(d.Gaps))
}

func (s *discoverySuite) TestNotInferredIsAGapNamingTheFile() {
	ts := s.tsAt("web")
	ts.NotInferred = []spells.DiscoveryGap{{File: "web/package.json", Why: "scripts.dev: meaning unknown"}}
	d := s.merge(spellAnswer{Spell: s.typescript, Claims: []spells.DiscoveredProject{ts}})
	s.Equal([]string{"web/package.json: scripts.dev: meaning unknown (spell typescript saw it and could not infer a target); settle: a target in web/magusfile.buzz"}, s.findings(d.Gaps))
}

func (s *discoverySuite) TestAMagusfileWins() {
	d, err := mergeDiscovery(s.asked(), []spellAnswer{
		{Spell: s.golang, Claims: []spells.DiscoveredProject{s.goRoot()}},
		{Spell: s.bash, Claims: []spells.DiscoveredProject{s.bashMember("scripts")}},
	}, []string{"."})
	s.Require().NoError(err)
	s.Empty(d.Projects)
	s.Equal([]string{`magusfile.buzz: discovered claims not rendered (spell bash, go claimed this directory, and its magusfile wins); settle: bind them there: magus\project({"spells": [bash, go]})`}, s.findings(d.Notes))
}

func (s *discoverySuite) TestAMemberWithNoOwnerFormsTheRoot() {
	d := s.merge(spellAnswer{Spell: s.bash, Claims: []spells.DiscoveredProject{s.bashMember("scripts")}})
	s.Require().Len(d.Projects, 1)
	s.Equal(".", d.Projects[0].Path)
	s.Contains(s.findings(d.Notes), ".: root project formed (no project owns the member claims of bash at scripts); settle: a magusfile in the directory that should own them")
}

func (s *discoverySuite) TestAMemberAttachesToTheLongestPrefix() {
	d := s.merge(
		spellAnswer{Spell: s.golang, Claims: []spells.DiscoveredProject{s.goRoot()}},
		spellAnswer{Spell: s.typescript, Claims: []spells.DiscoveredProject{s.tsAt("web")}},
		spellAnswer{Spell: s.bash, Claims: []spells.DiscoveredProject{s.bashMember("web/scripts")}},
	)
	s.Equal([]string{"go"}, d.Projects[0].Spells)
	s.Equal([]string{"bash", "typescript"}, d.Projects[1].Spells)
}

func (s *discoverySuite) TestAnAliasedSpellHandle() {
	tool := spells.Descriptor{Name: "my-tool", Ops: map[string]spells.Op{"run": {}}}
	in := discoveryInput{Queries: map[string]spells.DiscoveryQuery{"my-tool": {}}}
	d, err := mergeDiscovery(in, []spellAnswer{{Spell: tool, Claims: []spells.DiscoveredProject{{Path: ".", Role: spells.DiscoveryRoleProject, Targets: map[string][]string{"build": {"run"}}}}}}, nil)
	s.Require().NoError(err)
	s.Contains(d.Projects[0].Source, `import "magus/spell/my-tool" as my_tool_spell;`)
	s.Contains(d.Projects[0].Source, `my_tool_spell["run"](ctx);`)
}

func (s *discoverySuite) TestABadClaimFailsTheMerge() {
	golang := s.goRoot()
	golang.Targets = map[string][]string{"build": {"cargo-build"}}
	_, err := mergeDiscovery(s.asked(), []spellAnswer{{Spell: s.golang, Claims: []spells.DiscoveredProject{golang}}}, nil)
	s.ErrorContains(err, `spell "go": discover_projects: .: target "build": op "cargo-build" is not one the spell declares`)
}

func (s *discoverySuite) TestAnAnswerFromASpellNotAskedFailsTheMerge() {
	_, err := mergeDiscovery(discoveryInput{}, []spellAnswer{{Spell: s.golang, Claims: []spells.DiscoveredProject{s.goRoot()}}}, nil)
	s.ErrorContains(err, `spell "go" answered, but it was not asked`)
}

func (s *discoverySuite) TestANameABuzzStringCannotHoldFailsTheMerge() {
	golang := s.goRoot()
	golang.Name = "{x}"
	_, err := mergeDiscovery(s.asked(), []spellAnswer{{Spell: s.golang, Claims: []spells.DiscoveredProject{golang}}}, nil)
	s.ErrorContains(err, `discovery: .: name: "{x}" holds a quote`)
}

func (s *discoverySuite) write(root string, files map[string]string) {
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		s.Require().NoError(os.MkdirAll(filepath.Dir(abs), 0o755))
		s.Require().NoError(os.WriteFile(abs, []byte(content), 0o600))
	}
}

func (s *discoverySuite) TestCollectPassesMarkerContentAndSourcePathsOnly() {
	root := s.T().TempDir()
	s.write(root, map[string]string{"go.mod": "module x\n", "web/go.mod": "module y\n", "main.go": "package main\n", "README.md": "x"})
	in, err := collectDiscovery(root, []spells.Descriptor{s.golang})
	s.Require().NoError(err)
	files := in.Queries["go"].Files
	s.Require().Len(files, 3)
	s.Equal("go.mod", files[0].Path)
	s.Equal("module x\n", *files[0].Content)
	s.Equal("main.go", files[1].Path)
	s.Nil(files[1].Content)
	s.Equal("web/go.mod", files[2].Path)
	s.Equal("module y\n", *files[2].Content)
	s.Equal(root, in.Queries["go"].Root)
	s.Empty(in.Notes)
}

func (s *discoverySuite) TestCollectReadsADoubleStarMarkerAtAnyDepth() {
	root := s.T().TempDir()
	s.write(root, map[string]string{"apps/web/package.json": `{"name": "web"}`})
	ts := s.typescript
	ts.Needs = []string{"**/package.json"}
	in, err := collectDiscovery(root, []spells.Descriptor{ts})
	s.Require().NoError(err)
	files := in.Queries["typescript"].Files
	s.Require().Len(files, 1)
	s.Equal("apps/web/package.json", files[0].Path)
	s.Equal(`{"name": "web"}`, *files[0].Content)
}

func (s *discoverySuite) TestCollectNotesADirectoryOneSpellIgnoresAndAnotherDoesNot() {
	root := s.T().TempDir()
	s.write(root, map[string]string{"vendor/x/package.json": "{}", "package.json": "{}"})
	in, err := collectDiscovery(root, []spells.Descriptor{s.golang, s.typescript})
	s.Require().NoError(err)
	s.Equal([]string{"package.json"}, []string{in.Queries["typescript"].Files[0].Path})
	s.Len(in.Queries["typescript"].Files, 1)
	s.Equal([]string{"vendor: not collected (spell go ignores vendor, so typescript never saw a claim inside); settle: a magusfile in that directory; a magusfile wins"}, s.findings(in.Notes))
}

func (s *discoverySuite) TestCollectNotesReachTheMerge() {
	in := s.asked()
	in.Notes = []discoveryFinding{{Path: "vendor", What: "not collected", Why: "w", Settle: "s"}}
	d, err := mergeDiscovery(in, nil, nil)
	s.Require().NoError(err)
	s.Equal([]string{"vendor: not collected (w); settle: s"}, s.findings(d.Notes))
}
