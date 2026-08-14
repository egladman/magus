package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evalFixtureDir holds the rendered permutations the skill-eval harness measures.
const evalFixtureDir = "../../evals/fixtures"

// updateEvalFixtures re-renders the fixture trees instead of asserting against them.
//
// This test is the fixtures' generator, because nothing else can be. `magus agent
// install` writes one permutation - simple - and the harness runs the two as
// separate working directories, so the full tree is not recoverable from an
// install. Nor is it recoverable from the simple tree's <name>-full twins: the
// twin carries the right BODY under the wrong name, with a description that
// announces itself as a twin, so folding twins onto plain names produces
// something that looks right until you read the frontmatter. The same call the
// assertion compares against therefore writes the file.
//
// An environment variable rather than a test flag, and that is forced: the
// go-test op runs `go test ./...`, so a forwarded `-update-...` flag reaches
// every package's test binary and every one of them rejects it as undefined.
// The whole run fails before this test can act on it.
func updateEvalFixtures() bool { return os.Getenv("MAGUS_UPDATE_EVAL_FIXTURES") != "" }

// TestEvalFixturesMatchTheEmbeddedSkills gates the one generated tree in this
// repo that nothing regenerated.
//
// evals/fixtures/{full,simple} are the rendered skills the eval harness feeds to
// a model, committed so a run is reproducible. They are DECLARED as sources of
// the evals project, not as outputs of any target, so no generate step rewrites
// them and no drift check looked at them - and they went stale exactly the way
// an ungated generated tree does. Found on main: the committed full fixture for
// magus-buzz-write still advertised `magus buzz --workspace`, a flag that had been
// removed, because a skill edit landed without a re-render.
//
// A stale fixture is worse here than in most places. The harness exists to
// answer whether a permutation changes a model's behavior; measuring text the
// binary no longer produces answers that question about a skill nobody ships.
//
// Each tree is compared against RenderedSkills for its variant - what an install
// of that variant would write, twins and all - rather than a per-skill render.
// The two trees are working directories a model is pointed at, so what has to be
// faithful is the whole directory, including the fact that a simple install
// leaves a <name>-full twin beside each skill and a full install does not.
func TestEvalFixturesMatchTheEmbeddedSkills(t *testing.T) {
	for _, v := range []agent.Variant{agent.VariantFull, agent.VariantSimple} {
		skills, err := agentSkills.RenderedSkills(v)
		require.NoError(t, err)

		shipped := make(map[string]bool, len(skills))
		for _, skill := range skills {
			shipped[skill.Name] = true
			// A twin is stamped with ITS variant, not the tree's: inside a simple
			// install the twin is the full body and says so.
			want := agentSkills.StampSkill(agentSkills.RenderSkill(skill), skill.Variant)
			path := filepath.Join(evalFixtureDir, v.String(), ".claude", "skills", skill.Name, "SKILL.md")
			if updateEvalFixtures() {
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(t, os.WriteFile(path, want, 0o644))
				continue
			}
			got, err := os.ReadFile(path)
			if !assert.NoErrorf(t, err, "%s permutation of %s has no committed fixture; re-render with:\n%s",
				v, skill.Name, rerenderHint()) {
				continue
			}
			assert.Equalf(t, string(want), string(got),
				"the committed %s fixture for %s is not what this binary renders.\n"+
					"Re-render rather than hand-editing it:\n%s",
				v, skill.Name, rerenderHint())
		}

		// The reverse direction: a skill that was renamed or dropped leaves its
		// fixture behind, and the harness would go on measuring a body that no
		// longer ships.
		entries, err := os.ReadDir(filepath.Join(evalFixtureDir, v.String(), ".claude", "skills"))
		require.NoError(t, err)
		for _, e := range entries {
			if !e.IsDir() || shipped[e.Name()] {
				continue
			}
			stale := filepath.Join(evalFixtureDir, v.String(), ".claude", "skills", e.Name())
			if updateEvalFixtures() {
				// The generator owns its deletions too. A rename that only ADDED the
				// new tree would leave the old skill in the corpus, and nothing would
				// report it: a drift check compares what a generator declares against
				// what it wrote, and an extra directory is in neither set.
				require.NoError(t, os.RemoveAll(stale))
				continue
			}
			assert.Failf(t, "stale eval fixture",
				"evals/fixtures/%s holds %s, which this binary does not ship; re-render with:\n%s",
				v, e.Name(), rerenderHint())
		}
	}
}

// rerenderHint names the command that rebuilds the fixture trees. Both, always:
// they are one corpus rendered two ways, and refreshing half of it is how the
// pair stops being comparable.
func rerenderHint() string {
	return "  MAGUS_UPDATE_EVAL_FIXTURES=1 magus run go::go-test . -- -run TestEvalFixturesMatchTheEmbeddedSkills"
}
