package main

import (
	"context"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The guard and the breadcrumbs are one contract now: a command magus serves as `next` is
// pre-authorized, so a template the guard would refuse is a template that must not ship.
// The obligation this test carries is the upstream half of that bargain.
//
// Graded PER ROLE, not once. A command is fine unbound and refused under a worker's lane
// (a `:rw` regeneration is the case the personas found), and grading only the unbound case
// is how that ships. A failure here names the template and the role, which is the signal
// that `next` has to be computed for the acting role rather than filtered afterwards.

// nextIDRe finds the template ids declared in internal/hint/next.go.
//
// Read out of the SOURCE rather than compared against a list here, for the reason
// TestAllDeclaredAreRegistered reads its own file: a list in the test is a second copy to
// forget, and the failure it produces (a template nobody grades) is silent.
var nextIDRe = regexp.MustCompile(`ID:\s*"([a-z-]+)"`)

// servedNextTemplates renders every breadcrumb the tree can serve, by driving the
// functions that build them rather than by reconstructing their commands.
//
// Several calls per function, because capNext trims to three: one fixture per function
// would leave the fourth template of a family unrendered and unguarded, which is exactly
// the hole the id sweep below closes.
func servedNextTemplates(t *testing.T) map[string]string {
	t.Helper()
	rendered := map[string]string{}
	add := func(next []hint.Next) {
		for _, n := range next {
			rendered[n.ID] = n.Run
		}
	}
	add(hint.NextForQuery(types.KnowledgeQueryOutput{Matches: []types.KnowledgeMatch{
		{ID: "doc:docs/guides/agents.md", Kind: types.KindDoc, Label: "agents"},
		{ID: "doc:docs/guides/guard.md", Kind: types.KindDoc, Label: "guard"},
	}}))
	add(hint.NextForQuery(types.KnowledgeQueryOutput{Matches: []types.KnowledgeMatch{
		{ID: "symbol:hookCmd", Kind: types.KindSymbol, Label: "hookCmd"},
	}}))
	add(hint.NextForExplain(types.KnowledgeExplainOutput{
		Node: types.KnowledgeNode{
			ID: "symbol:hookCmd", Kind: types.KindSymbol,
			Label: "hookCmd", Source: "cmd/magus/guard.go:79",
		},
		Out: []types.KnowledgeEdgeRef{{Other: "symbol:evaluateBashGuard"}},
	}))
	add(hint.NextForFiles([]types.FileEntry{
		{Path: "std/fs.go", Project: ".", Role: "source", SourceOf: []string{"."}},
		{Path: "MAGUS.md", Project: ".", Role: "output", OutputOf: []string{"."},
			Claims: []types.FileClaim{{Role: "output", Project: ".", Target: "generate"}}},
	}))
	add(hint.NextForAffected("ci", []string{"docs"}))
	add(hint.NextForFailure(".", "test", "out84fea3b6ae30"))

	declared, err := os.ReadFile("../../internal/hint/next.go")
	require.NoError(t, err)
	var ids []string
	for _, m := range nextIDRe.FindAllStringSubmatch(string(declared), -1) {
		ids = append(ids, m[1])
	}
	require.NotEmpty(t, ids, "nextIDRe no longer matches how internal/hint/next.go declares a template")
	for _, id := range ids {
		require.Contains(t, rendered, id,
			"internal/hint/next.go declares the %q breadcrumb and no fixture here renders it, so nothing grades it through the guard", id)
	}
	return rendered
}

// TestEveryServedNextPassesTheGuardForEveryRole is the structural half of the
// pre-authorization bargain.
func TestEveryServedNextPassesTheGuardForEveryRole(t *testing.T) {
	worker := narrowLease()
	worker.ID, worker.Parent = "harness/worker", "harness/root"

	reviewer := types.Lease{
		ID: "harness/reviewer", Goal: "read the guard surface",
		ReadOnly: true, Focus: []string{"cmd/magus/**"},
		State: types.StateRunning, Registered: 1,
	}
	ctx, _ := fleetFixture(t, worker, reviewer)
	templates := servedNextTemplates(t)

	for _, role := range []struct {
		name  string
		lease string
	}{
		{"unbound", ""},
		{"worker with a narrow lane", worker.ID},
		{"reviewer", reviewer.ID},
	} {
		for id, run := range templates {
			t.Run(role.name+"/"+id, func(t *testing.T) {
				if deny := evaluateBashGuard(run).Deny; deny != "" {
					t.Errorf("the %q breadcrumb serves %q, which the guard denies for every role:\n%s", id, run, deny)
				}
				for _, rule := range []func(context.Context, string, string) string{
					denyLeaseScopedGate, denyLeaseScopedVCS, denyLeaseScopedRebind,
				} {
					if reason := rule(ctx, role.lease, run); reason != "" {
						t.Errorf("the %q breadcrumb serves %q, which the guard denies for a %s.\n"+
							"`next` has to be computed for the acting role, not filtered after the fact:\n%s",
							id, run, role.name, reason)
					}
				}
			})
		}
	}
}

// TestServedNextTemplatesAreRunnable pins the property the pre-authorization reader
// depends on: a breadcrumb is a complete argv, not a form to fill in.
//
// A placeholder passes every rule above and then fails in the shell, which is the failure
// mode measured across the corpus on 2026-09-11: 30 of 30 templates cleared the guard and
// the ones carrying `<path>` broke on the reader's own redirect.
func TestServedNextTemplatesAreRunnable(t *testing.T) {
	for id, run := range servedNextTemplates(t) {
		assert.NotContains(t, run, "<", "the %q breadcrumb still carries a placeholder: %q", id, run)
		cmds, ok := parseGuardCommands(run)
		require.True(t, ok, "the %q breadcrumb does not parse as a shell command: %q", id, run)
		require.Len(t, cmds, 1, "a breadcrumb is one invocation: %q", run)
		assert.True(t, slices.Contains([]string{"magus", "./magus"}, cmds[0].Name),
			"the %q breadcrumb runs %q rather than magus", id, cmds[0].Name)
		assert.False(t, strings.Contains(run, "|") || strings.Contains(run, ">"),
			"the %q breadcrumb filters magus's own output: %q", id, run)
	}
}
