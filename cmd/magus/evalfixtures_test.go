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

// TestEvalFixturesMatchTheEmbeddedSkills gates the one generated tree in this
// repo that nothing regenerated.
//
// evals/fixtures/{full,simple} are the rendered skills the eval harness feeds to
// a model, committed so a run is reproducible. They are DECLARED as sources of
// the evals project, not as outputs of any target, so no generate step rewrites
// them and no drift check looked at them - and they went stale exactly the way
// an ungated generated tree does. Found on main: the committed full fixture for
// magus-buzz still advertised `magus buzz --workspace`, a flag that had been
// removed, because a skill edit landed without a re-render.
//
// A stale fixture is worse here than in most places. The harness exists to
// answer whether a permutation changes a model's behavior; measuring text the
// binary no longer produces answers that question about a skill nobody ships.
//
// This compares against exactly what the catalog renders for each skill and
// variant - same render, same stamp - so the fix when it fails is the install
// command in the message rather than a hand edit. Deliberately the CANONICAL
// per-skill render (EmbeddedSkills + Render), not RenderedSkills' install-time
// list: a simple install's <name>-full twin is byte-identical to the full
// fixture already covered here under its plain name, so fixturing it again
// under a second name would duplicate maintenance without adding any new
// behavior for the eval harness to measure.
func TestEvalFixturesMatchTheEmbeddedSkills(t *testing.T) {
	defs, err := agentSkills.EmbeddedSkills()
	require.NoError(t, err)

	for _, v := range []agent.Variant{agent.VariantFull, agent.VariantSimple} {
		skills := make([]agent.AgentSkill, 0, len(defs))
		for _, def := range defs {
			skill, err := agentSkills.Render(def, v)
			require.NoError(t, err)
			skills = append(skills, skill)
		}

		for _, skill := range skills {
			path := filepath.Join(evalFixtureDir, v.String(), ".claude", "skills", skill.Name, "SKILL.md")
			got, err := os.ReadFile(path)
			if !assert.NoErrorf(t, err, "%s permutation of %s has no committed fixture; re-render with:\n"+
				"  magus agent install --dir evals/fixtures/%s .claude/skills --force%s",
				v, skill.Name, v, simpleFlag(v)) {
				continue
			}
			want := agentSkills.StampSkill(agentSkills.RenderSkill(skill), v)
			assert.Equalf(t, string(want), string(got),
				"the committed %s fixture for %s is not what this binary renders.\n"+
					"Re-render rather than hand-editing it:\n"+
					"  magus agent install --dir evals/fixtures/%s .claude/skills --force%s",
				v, skill.Name, v, simpleFlag(v))
		}

		// The reverse direction: a skill that was renamed or dropped leaves its
		// fixture behind, and the harness would go on measuring a body that no
		// longer ships.
		shipped := make(map[string]bool, len(skills))
		for _, s := range skills {
			shipped[s.Name] = true
		}
		entries, err := os.ReadDir(filepath.Join(evalFixtureDir, v.String(), ".claude", "skills"))
		require.NoError(t, err)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			// A simple install also writes each skill's <name>-full twin, so a
			// fixture tree re-rendered with the command in the messages above
			// legitimately holds them. They are byte-identical to the full
			// fixtures already checked under the plain name, so they are
			// tolerated here rather than fixtured and compared twice.
			if agent.IsFullTwinName(e.Name()) {
				continue
			}
			assert.Truef(t, shipped[e.Name()],
				"evals/fixtures/%s holds %s, which this binary does not ship; delete the directory or restore the skill",
				v, e.Name())
		}
	}
}

func simpleFlag(v agent.Variant) string {
	if v.Simple() {
		return " --simple"
	}
	return ""
}
