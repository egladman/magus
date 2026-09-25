package bindings

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/agent"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type skillsWorkspace struct {
	types.WorkspaceRepository
	root string
}

func (w *skillsWorkspace) Root() string        { return w.root }
func (w *skillsWorkspace) Harnesses() []string { return []string{"skills-test"} }

const skillsScript = `
import "magus";

export fun summary() > str !> any {
    var picked = "";
    foreach (s in magus\skills({"form": "full"})) {
        if (s.source == "local" or s.name == "magus-query") {
            picked = picked + "{s.name} {s.source} {s.form} {s.current}\n";
        }
    }
    return picked;
}

export fun unknown() > void !> any {
    magus\skills({"name": "acme-rule"});
}

export fun misspelledOption() > void !> any {
    magus\skills({"nmae": "acme-rules"});
}
`

func skillsSession(t *testing.T, ctx context.Context) *buzz.Session {
	t.Helper()
	sess := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = sess.Close() })
	RegisterModuleSurface(ctx, sess)
	RegisterMagusNamespace(ctx, sess)
	require.NoError(t, sess.Exec(ctx, skillsScript))
	return sess
}

func callSkills(t *testing.T, ctx context.Context, sess *buzz.Session, name string) (string, error) {
	t.Helper()
	fn, ok := sess.Exports()[name]
	require.Truef(t, ok, "exported Buzz entrypoint %s is missing", name)
	got, err := sess.CallValue(ctx, fn, nil)
	if err != nil {
		return "", err
	}
	if got.IsStr() {
		return got.AsString(), nil
	}
	return "", nil
}

// TestSkillsThroughBuzzScript drives magus\skills through the parser, checker and VM
// against the real catalog, installed the way `magus agent install` writes it.
func TestSkillsThroughBuzzScript(t *testing.T) {
	root := t.TempDir()
	agent.RegisterHarnessSpellLoader(func(_ context.Context, id string) (agent.HarnessDescriptor, string, bool, error) {
		if id != "skills-test" {
			return agent.HarnessDescriptor{}, "", false, nil
		}
		return agent.HarnessDescriptor{
			SchemaVersion: 2,
			ID:            "skills-test",
			Display:       agent.HarnessDisplay{Name: "Skills test"},
			Skills:        agent.HarnessSkills{Paths: []string{".agents/skills"}, Form: agent.FormFull},
		}, "spell:skills-test", true, nil
	})
	t.Cleanup(func() { agent.RegisterHarnessSpellLoader(loadHarnessFromSpell) })

	_, _, err := skillCatalog().WriteSkillTree(root, ".agents/skills", false, agent.FormFull)
	require.NoError(t, err)
	local := filepath.Join(root, ".agents/skills", "acme-rules", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(local), 0o755))
	require.NoError(t, os.WriteFile(local, []byte("---\nname: acme-rules\ndescription: Our house rules.\n---\n\n# Acme\n"), 0o644))

	ctx := types.WithWorkspace(t.Context(), &skillsWorkspace{root: root})
	sess := skillsSession(t, ctx)

	got, err := callSkills(t, ctx, sess, "summary")
	require.NoError(t, err)
	assert.Equal(t, "acme-rules local full true\nmagus-query shipped full true\n", got)

	_, err = callSkills(t, ctx, sess, "unknown")
	assert.ErrorContains(t, err, `no skill named "acme-rule"; near matches: acme-rules`)

	_, err = callSkills(t, ctx, sess, "misspelledOption")
	assert.ErrorContains(t, err, `unknown option "nmae" (want name, form)`)
}

func TestSkillsNeedsAWorkspace(t *testing.T) {
	ctx := t.Context()
	sess := skillsSession(t, ctx)
	_, err := callSkills(t, ctx, sess, "summary")
	assert.ErrorContains(t, err, "no workspace on the context")
}
