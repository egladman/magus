package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"text/template"
	"text/template/parse"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLocalSkillNameIsReserved keeps magus out of the one name a workspace is
// told it owns.
//
// The magus-adapt skill teaches workspaces to put their own rules in a skill
// magus does not ship, and names magus-local-development as the convention. Shipping a
// skill by that name later would not conflict loudly: install --force writes
// the shipped body straight over the workspace's file, on every machine, at
// once. Someone would have to reconstruct the rules from git history, if they
// were committed at all.
func TestLocalSkillNameIsReserved(t *testing.T) {
	for _, source := range skillSources {
		assert.NotEqual(t, LocalSkillName, source.name,
			"%q is reserved for a workspace's own rules; magus shipping a skill by that name would overwrite them on the next install --force", LocalSkillName)
	}
}

// TestFullTwinNamesAreReserved keeps the twin namespace collision-free.
//
// A --simple install writes <name>-full beside every skill, so a shipped skill
// whose own name ends in -full would either collide with another skill's twin
// or be shadowed by its own. The collision is silent: WriteSkillTree writes
// whichever entry comes last, so one of the two skills simply vanishes from
// the installed tree with nothing reporting it. Same hazard as
// LocalSkillName, same fix - assert it rather than remember it.
func TestFullTwinNamesAreReserved(t *testing.T) {
	shipped := make(map[string]bool, len(skillSources))
	for _, source := range skillSources {
		shipped[source.name] = true
	}
	for _, source := range skillSources {
		assert.Falsef(t, IsFullTwinName(source.name),
			"%q ends in %q, which is the reserved suffix for a --simple install's always-full twin; rename the skill",
			source.name, fullTwinSuffix)
		assert.Falsef(t, shipped[FullTwinName(source.name)],
			"%q collides with the twin --simple writes for %q; one of the two would silently overwrite the other",
			FullTwinName(source.name), source.name)
	}
}

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	files := make(fstest.MapFS, len(skillSources))
	for _, source := range skillSources {
		files[source.bodyPath] = &fstest.MapFile{Data: []byte("# " + source.name + "\n")}
	}
	return NewCatalog(files, "## Magus\n\nUse the graph.\n", 6)
}

func TestCatalogInstallsAndVerifiesSkillTree(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()
	written, err := catalog.WriteSkillTree(dir, ".agents/skills", false, VariantFull)
	require.NoError(t, err)
	require.Len(t, written, len(skillSources), "a full install writes one file per skill and no twins")

	body, err := os.ReadFile(filepath.Join(dir, ".agents/skills", anchorSkillRel))
	require.NoError(t, err)
	assert.Contains(t, string(body), "license: "+skillLicense)
	assert.Contains(t, string(body), "skill-content: "+catalog.contentDigest)

	statuses := catalog.CheckStatuses(dir)
	require.Len(t, statuses, 1)
	assert.Equal(t, ".agents/skills", statuses[0].Location)
	assert.True(t, statuses[0].Installed)
	assert.False(t, statuses[0].Stale)

	stale := strings.Replace(string(body), "skill-content: "+catalog.contentDigest, "skill-content: 000000000000", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".agents/skills", anchorSkillRel), []byte(stale), 0o644))
	assert.True(t, catalog.CheckStatuses(dir)[0].Stale)
}

// TestWriteSkillTreeRejectsPathEscape guards S-3: the guard rejected an
// absolute dest or a leading "~" but not "..", so dest="../../outside" walked
// filepath.Join right out of dir. The doc comment on WriteSkillTree claims
// magus never silently writes outside the working tree; this is what makes
// that claim true rather than aspirational.
func TestWriteSkillTreeRejectsPathEscape(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()

	_, err := catalog.WriteSkillTree(dir, "../../outside", false, VariantFull)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes the working tree")

	// An ordinary nested destination is unaffected.
	written, err := catalog.WriteSkillTree(dir, "nested/skills", false, VariantFull)
	require.NoError(t, err)
	require.Len(t, written, len(skillSources))
}

// TestCatalogAgentsBlockIsSelfDelimitedAndStable pins what a developer pastes:
// exactly one marker pair, a stamp CheckStatuses can grade, and the same bytes
// on every call so re-running to refresh a stale block produces a clean diff.
func TestCatalogAgentsBlockIsSelfDelimitedAndStable(t *testing.T) {
	catalog := testCatalog(t)
	block := catalog.AgentsBlock()

	assert.True(t, strings.HasPrefix(block, "<!-- magus:skills:begin "))
	assert.True(t, strings.HasSuffix(block, "<!-- magus:skills:end -->\n"))
	assert.Equal(t, 1, strings.Count(block, "magus:skills:begin"))
	assert.Contains(t, block, "skill-content: "+catalog.contentDigest)
	assert.Equal(t, block, catalog.AgentsBlock(), "AgentsBlock must be byte-stable across calls")

	// The block a developer pastes is the block CheckStatuses grades: round-trip
	// it through a hand-owned AGENTS.md and it must read as current, not stale.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Local rules\n\nkeep this\n\n"+block), 0o644))
	statuses := catalog.CheckStatuses(dir)
	require.Len(t, statuses, 1)
	assert.Equal(t, "AGENTS.md", statuses[0].Location)
	assert.False(t, statuses[0].Stale, statuses[0].Detail)
}

// TestSimpleInstallWritesTwinsStampedFull pins the mixed-batch property: one
// VariantSimple install produces entries of BOTH variants, so the stamp has to
// follow each entry's own Variant rather than the variant that was requested.
// Keying off the request instead would stamp every twin "simple" - mislabelling
// the copy a delegated model was handed precisely because it needed full.
func TestSimpleInstallWritesTwinsStampedFull(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()
	written, err := catalog.WriteSkillTree(dir, ".claude/skills", false, VariantSimple)
	require.NoError(t, err)
	require.Len(t, written, 2*len(skillSources), "simple writes one primary plus one twin per skill")

	primary, err := os.ReadFile(filepath.Join(dir, ".claude/skills", anchorSkillRel))
	require.NoError(t, err)
	assert.Contains(t, string(primary), "skill-variant: simple")

	base := strings.TrimSuffix(anchorSkillRel, "/SKILL.md")
	twin, err := os.ReadFile(filepath.Join(dir, ".claude/skills", FullTwinName(base), "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(twin), "skill-variant: full",
		"a twin inside a simple install must stamp itself full")
	// One source body, so both still report the same digest and go stale together.
	assert.Contains(t, string(twin), "skill-content: "+catalog.contentDigest)
}

func TestCatalogSkillTarIsByteStable(t *testing.T) {
	catalog := testCatalog(t)
	a, err := catalog.SkillTar(".claude/skills", VariantFull)
	require.NoError(t, err)
	b, err := catalog.SkillTar(".claude/skills", VariantFull)
	require.NoError(t, err)
	assert.Equal(t, a, b, "SkillTar must be reproducible; no embedded timestamps in the body")
	assert.NotEmpty(t, a)
}

func TestCatalogSkillBytesByName(t *testing.T) {
	catalog := testCatalog(t)
	body, err := catalog.SkillBytes("magus-architecture", VariantFull)
	require.NoError(t, err)
	assert.Contains(t, string(body), "name: magus-architecture")
	assert.Contains(t, string(body), "skill-content: "+catalog.contentDigest)

	ultra, err := catalog.SkillBytes("magus-delegate-ultra", VariantFull)
	require.NoError(t, err)
	assert.Contains(t, string(ultra), "name: magus-delegate-ultra")

	_, err = catalog.SkillBytes("does-not-exist", VariantFull)
	assert.ErrorContains(t, err, "unknown skill")
}

func TestDelegateUltraVariantsKeepTheSameSafetyContract(t *testing.T) {
	catalog := NewCatalog(os.DirFS(filepath.Join("..", "..", "cmd", "magus")), "", 7)
	full, err := catalog.SkillBytes("magus-delegate-ultra", VariantFull)
	require.NoError(t, err)
	simple, err := catalog.SkillBytes("magus-delegate-ultra", VariantSimple)
	require.NoError(t, err)

	for _, body := range [][]byte{full, simple} {
		text := string(body)
		assert.Contains(t, text, "acceptance criteria")
		assert.Contains(t, text, "magus affected <target> --plan")
		assert.Contains(t, text, "magus status --watch=15s")
		assert.Contains(t, text, "Nested delegation is allowed")
		assert.NotContains(t, text, "{{")
	}
	assert.Contains(t, string(full), "natural evolution of loop engineering")
	assert.NotContains(t, string(simple), "natural evolution of loop engineering")
	assert.LessOrEqual(t, len(simple)*5, len(full)*4,
		"--simple must remove at least one fifth of the full skill while preserving its safety contract")
}

func TestCatalogRenderAndStamp(t *testing.T) {
	catalog := testCatalog(t)
	rendered := catalog.RenderSkill(AgentSkill{Name: "magus-test", Description: "Does one thing.", Body: "# Test"})
	assert.Equal(t, "---\nname: magus-test\ndescription: \"Does one thing.\"\n---\n\n# Test\n", string(rendered))
	stamped := string(catalog.StampSkill(rendered, VariantFull))
	assert.Contains(t, stamped, "metadata:\n  source: magus\n")
	assert.Equal(t, 1, strings.Count(stamped, "generated by: magus agent install"))
}

// TestApplyVariantKeepsBothPermutationsWellFormed pins the branching contract.
func TestApplyVariantKeepsBothPermutationsWellFormed(t *testing.T) {
	body := "Do the thing{{if .Full}} - because the alternative silently corrupts output{{end}}.\n" +
		"\n{{if .Full}}A whole paragraph of rationale.{{end}}\n\nNext step."

	full, err := applyVariant("s", body, VariantFull)
	require.NoError(t, err)
	assert.Equal(t, "Do the thing - because the alternative silently corrupts output.\n\nA whole paragraph of rationale.\n\nNext step.", full)
	assert.NotContains(t, full, "{{", "no template action may reach an installed file")

	simple, err := applyVariant("s", body, VariantSimple)
	require.NoError(t, err)
	assert.Equal(t, "Do the thing.\n\nNext step.", simple,
		"the full-only branches go, the sentence still ends in a period, and the emptied paragraph leaves no blank-line run")
}

// TestApplyVariantDoesNotTouchUnelidedContent ensures rendering only evaluates
// template actions and never rewrites ordinary text.
func TestApplyVariantDoesNotTouchUnelidedContent(t *testing.T) {
	body := "Never run `git checkout .` or `git clean`{{if .Full}} - it destroys untracked work{{end}}.\n" +
		"\nKeep ( these ) spaces and this : colon."

	for _, v := range []Variant{VariantFull, VariantSimple} {
		got, err := applyVariant("s", body, v)
		require.NoError(t, err)
		assert.Contains(t, got, "`git checkout .`", "%s must not rewrite an unelided command", v)
		assert.Contains(t, got, "( these ) spaces and this : colon.", "%s must not retouch unelided punctuation", v)
	}
}

// TestApplyVariantSwapsTwoWordings pins the else idiom that lets both
// permutations express one instruction at different lengths.
func TestApplyVariantSwapsTwoWordings(t *testing.T) {
	body := "Scope explicitly{{if .Full}}, because a bare command acts on whichever project holds " +
		"your current directory and therefore means something different depending on where you " +
		"happen to be standing{{else}} (magus is CWD-relative){{end}}."

	full, err := applyVariant("s", body, VariantFull)
	require.NoError(t, err)
	assert.Equal(t, "Scope explicitly, because a bare command acts on whichever project holds your "+
		"current directory and therefore means something different depending on where you happen "+
		"to be standing.", full)

	simple, err := applyVariant("s", body, VariantSimple)
	require.NoError(t, err)
	assert.Equal(t, "Scope explicitly (magus is CWD-relative).", simple)

	for _, got := range []string{full, simple} {
		assert.NotContains(t, got, "{{", "no template action may reach an installed file")
	}
}

// TestApplyVariantTerseAloneIsSimpleOnly covers a simple-only branch.
func TestApplyVariantTerseAloneIsSimpleOnly(t *testing.T) {
	body := "Step one.{{if .Simple}} See the docs for why.{{end}}"

	full, err := applyVariant("s", body, VariantFull)
	require.NoError(t, err)
	assert.Equal(t, "Step one.", full)

	simple, err := applyVariant("s", body, VariantSimple)
	require.NoError(t, err)
	assert.Equal(t, "Step one. See the docs for why.", simple)
}

// TestApplyVariantRefusesMalformedTemplate keeps a malformed body from installing.
func TestApplyVariantRefusesMalformedTemplate(t *testing.T) {
	for name, body := range map[string]string{
		"missing end":   "Do it{{if .Full}} because.",
		"unknown field": "Do it {{.Unknown}}.",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := applyVariant("magus-x", body, VariantSimple)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "magus-x", "the error must name the skill that is malformed")
		})
	}
}

// TestSkillTemplatesUseOnlyBranching keeps the permutations from diverging
// structurally. The old marker scheme could only swap spans, so "both
// permutations describe the same behaviour" was true by construction; with a
// general template engine it has to be asserted.
func TestSkillTemplatesUseOnlyBranching(t *testing.T) {
	for _, source := range skillSources {
		t.Run(source.name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("..", "..", "cmd", "magus", source.bodyPath))
			require.NoError(t, err)
			tmpl, err := template.New(source.name).Parse(string(body))
			require.NoError(t, err)
			require.NoError(t, validateTemplateNode(tmpl.Root))
		})
	}
}

func validateTemplateNode(node parse.Node) error {
	switch node := node.(type) {
	case *parse.ListNode:
		for _, child := range node.Nodes {
			if err := validateTemplateNode(child); err != nil {
				return err
			}
		}
		return nil
	case *parse.TextNode:
		return nil
	case *parse.IfNode:
		if err := validateBranchPipe(node.Pipe); err != nil {
			return err
		}
		if err := validateTemplateNode(node.List); err != nil {
			return err
		}
		if node.ElseList != nil {
			return validateTemplateNode(node.ElseList)
		}
		return nil
	case *parse.ActionNode:
		return validateActionPipe(node.Pipe)
	default:
		return fmt.Errorf("template node %T is not permitted", node)
	}
}

func validateBranchPipe(pipe *parse.PipeNode) error {
	if len(pipe.Cmds) != 1 {
		return fmt.Errorf("branch pipeline has %d commands", len(pipe.Cmds))
	}
	args := pipe.Cmds[0].Args
	if len(args) == 1 {
		field, ok := args[0].(*parse.FieldNode)
		if ok && len(field.Ident) == 1 && (field.Ident[0] == "Full" || field.Ident[0] == "Simple") {
			return nil
		}
	}
	if len(args) == 2 {
		field, fieldOK := args[0].(*parse.FieldNode)
		_, stringOK := args[1].(*parse.StringNode)
		if fieldOK && stringOK && len(field.Ident) == 1 && field.Ident[0] == "Is" {
			return nil
		}
	}
	return fmt.Errorf("branch must be .Full, .Simple, or .Is \"name\"")
}

func validateActionPipe(pipe *parse.PipeNode) error {
	if len(pipe.Cmds) != 1 || len(pipe.Cmds[0].Args) != 1 {
		return fmt.Errorf("template action must be one bare field or string")
	}
	switch node := pipe.Cmds[0].Args[0].(type) {
	case *parse.StringNode:
		return nil
	case *parse.FieldNode:
		if len(node.Ident) == 1 {
			return nil
		}
	}
	return fmt.Errorf("template action must be one bare field or string")
}

// TestStampNamesTheVariantButSharesTheDigest pins the versioning property the
// single-source design exists for: both permutations come from one body, so they
// must report the same content digest and go stale together. A per-variant digest
// would let a simple install look current against a source its sibling outgrew.
func TestStampNamesTheVariantButSharesTheDigest(t *testing.T) {
	catalog := testCatalog(t)
	full, err := catalog.SkillBytes("magus-vcs", VariantFull)
	require.NoError(t, err)
	simple, err := catalog.SkillBytes("magus-vcs", VariantSimple)
	require.NoError(t, err)

	assert.Contains(t, string(full), "skill-variant: full")
	assert.Contains(t, string(simple), "skill-variant: simple")

	digest := footerDigestRe.FindStringSubmatch(string(full))
	require.Len(t, digest, 2)
	assert.Contains(t, string(simple), "skill-content: "+digest[1],
		"one source body, one digest - the permutations version together")
}
