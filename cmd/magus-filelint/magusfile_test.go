package main

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installMagusfile declares one target that installs packages, with targets as
// its project's policy block.
func installMagusfile(targets string) []byte {
	return []byte(`final project = {"targets": {` + targets + `}};

export fun install(ctx: magus\Context, args: [str]) > void !> any {
    proc\exec("pnpm", ["install", "--frozen-lockfile"]);
}

export fun build(ctx: magus\Context, args: [str]) > void !> any {
    ctx.needs(install);
}
`)
}

func TestPackageInstallTargetsNeverReplay(t *testing.T) {
	clean := fstest.MapFS{
		"console/magusfile.buzz":          {Data: installMagusfile(`"install": {"skip_cache": "writes node_modules"}`)},
		"testdata/magusfile.buzz":         {Data: installMagusfile(``)},
		"node_modules/pkg/magusfile.buzz": {Data: installMagusfile(``)},
	}
	assert.Empty(t, packageInstallTargetsNeverReplay(clean))

	seeded := fstest.MapFS{"console/magusfile.buzz": {Data: installMagusfile(``)}}
	got := packageInstallTargetsNeverReplay(seeded)
	require.Len(t, got, 1)
	assert.Equal(t, "console/magusfile.buzz", got[0].path)
	assert.Equal(t, 3, got[0].line)
	assert.Equal(t, `target "install" installs packages but may replay`, got[0].problem)

	none := fstest.MapFS{"magusfile.buzz": {Data: []byte("export fun build(ctx: magus\\Context, args: [str]) > void {}\n")}}
	assert.Equal(t, []string{"no install target found"}, problems(packageInstallTargetsNeverReplay(none)))
}

func TestWholeTreeTargetsKeyOnTheGoTree(t *testing.T) {
	assert.Empty(t, wholeTreeTargetsKeyOnTheGoTree(repoFS(t, rootMagusfile)))

	fsys := repoFS(t, rootMagusfile)
	seed(t, fsys, rootMagusfile, `ctx.readsFiles("**/*.go", "**/*.s",`, `ctx.readsFiles("**/*.s",`)
	got := wholeTreeTargetsKeyOnTheGoTree(fsys)
	require.Len(t, got, 1)
	assert.Equal(t, `target "lint" declares a ctx.readsFiles footprint that does not name "**/*.go"`, got[0].problem)
	assert.Equal(t, lineOf(string(fsys[rootMagusfile].Data), "export fun lint("), got[0].line)
}

func TestRootProjectDeclaresTheConfigsItsToolsRead(t *testing.T) {
	assert.Empty(t, rootProjectDeclaresTheConfigsItsToolsRead(repoFS(t, rootMagusfile)))

	fsys := repoFS(t, rootMagusfile)
	seed(t, fsys, rootMagusfile, `"Dockerfile.static",`, ``)
	assert.Equal(t, []string{"Dockerfile.static is read by image-build's static multi-arch variant but no root source glob names it"},
		problems(rootProjectDeclaresTheConfigsItsToolsRead(fsys)))
}

// skillsFS is the root magusfile plus one SKILL.md per shipped skill in the tree.
func skillsFS(t *testing.T) fstest.MapFS {
	t.Helper()
	fsys := repoFS(t, rootMagusfile)
	entries, err := os.ReadDir(filepath.Join(repoRoot, embeddedSkillDir))
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() {
			fsys[embeddedSkillDir+"/"+e.Name()+"/SKILL.md"] = &fstest.MapFile{}
		}
	}
	return fsys
}

func TestSkillsGenerateDeclaresEveryShippedSkill(t *testing.T) {
	assert.Empty(t, skillsGenerateDeclaresEveryShippedSkill(skillsFS(t)))

	unshipped := skillsFS(t)
	unshipped[embeddedSkillDir+"/magus-brand-new/SKILL.md"] = &fstest.MapFile{}
	got := problems(skillsGenerateDeclaresEveryShippedSkill(unshipped))
	assert.Len(t, got, 6, "the primary and its twin in each of three destinations")
	assert.Contains(t, got, "internal/agent/skills ships magus-brand-new, but no declared output covers .opencode/skills/magus-brand-new-full/SKILL.md")

	claimed := skillsFS(t)
	seed(t, claimed, rootMagusfile, `".claude/skills/magus-run*/**",`, `".claude/skills/magus-run*/**", ".claude/skills/**",`)
	assert.Equal(t, []string{
		"declares .claude/skills/magus-skill-authoring/SKILL.md as a generated output, but magus never writes it",
		"declares .claude/skills/magus-local-development/SKILL.md as a generated output, but magus never writes it",
		"declares .claude/skills/land-pull-requests/SKILL.md as a generated output, but magus never writes it",
	}, problems(skillsGenerateDeclaresEveryShippedSkill(claimed)))
}

func TestGenerateDriftThrowNamesTheEngineDriftCode(t *testing.T) {
	assert.Empty(t, generateDriftThrowNamesTheEngineDriftCode(repoFS(t, rootMagusfile)))

	fsys := repoFS(t, rootMagusfile)
	seed(t, fsys, rootMagusfile, `"MGS4006: generate: regeneration`, `"generate: regeneration`)
	assert.Equal(t, []string{"the whole-tree drift throw does not carry MGS4006"},
		problems(generateDriftThrowNamesTheEngineDriftCode(fsys)))

	seed(t, fsys, rootMagusfile, "regeneration changed generated files", "outputs moved")
	assert.Equal(t, []string{"the whole-tree drift throw moved or was reworded"},
		problems(generateDriftThrowNamesTheEngineDriftCode(fsys)))
}
