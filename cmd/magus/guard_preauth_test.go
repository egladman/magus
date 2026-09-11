package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveNext writes one journal entry in the shape the serving side appends, so the two
// sides of the contract are pinned by the same literal a reader can compare to the docs.
func serveNext(t *testing.T, gate advisoryGate, id string, argv ...string) {
	t.Helper()
	path := hint.ServedNextPath(gate.base)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	quoted := make([]string, 0, len(argv))
	for _, a := range argv {
		quoted = append(quoted, fmt.Sprintf("%q", a))
	}
	line := fmt.Sprintf(`{"ts":%d,"id":%q,"argv":[%s]}`+"\n",
		time.Now().UnixMilli(), id, strings.Join(quoted, ","))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	_, err = f.WriteString(line)
	require.NoError(t, err)
}

// TestServedNextPreauthorizesTheCommandItServed pins the match, the binary spellings it
// has to see through, and the id it hands back for the metric.
func TestServedNextPreauthorizesTheCommandItServed(t *testing.T) {
	gate := newAdvisoryGate(t.TempDir(), "session-1")
	serveNext(t, gate, "query-explain", "magus", "explain", "target:.:ci")

	for _, command := range []string{
		"magus explain target:.:ci",
		"./magus explain target:.:ci",
		"/opt/bin/magus explain target:.:ci",
	} {
		assert.Equal(t, "query-explain", servedNextPreauthorizes(gate, command), "%q", command)
	}
}

// TestServedNextPreauthorizesNothingElse is the whole safety argument for the rule: the
// clearance covers the command that was served and not one token more.
func TestServedNextPreauthorizesNothingElse(t *testing.T) {
	gate := newAdvisoryGate(t.TempDir(), "session-1")
	serveNext(t, gate, "query-explain", "magus", "explain", "target:.:ci")

	for name, command := range map[string]string{
		"a different argument":       "magus explain target:.:lint",
		"an extra flag":              "magus explain target:.:ci -o json",
		"a missing argument":         "magus explain",
		"another command chained":    "magus explain target:.:ci && git stash",
		"a pipe":                     "magus explain target:.:ci | head",
		"a different program":        "notmagus explain target:.:ci",
		"a line that does not run":   "",
		"a redirect":                 "magus explain target:.:ci > /tmp/out",
		"a clobbering redirect":      "magus explain target:.:ci >| /tmp/out",
		"an appending redirect":      "magus explain target:.:ci >> /tmp/out",
		"stderr folded in":           "magus explain target:.:ci 2>&1",
		"sudo in front":              "sudo magus explain target:.:ci",
		"an env prefix":              "env FOO=1 magus explain target:.:ci",
		"a shell wrapper":            "sh -c 'magus explain target:.:ci'",
		"an assignment prefix":       "FOO=1 magus explain target:.:ci",
		"backgrounded":               "magus explain target:.:ci &",
		"an argument from the shell": "magus explain $TARGET",
	} {
		assert.Empty(t, servedNextPreauthorizes(gate, command), name)
	}

	assert.Empty(t, servedNextPreauthorizes(advisoryGate{}, "magus explain target:.:ci"),
		"no workspace, no journal, nothing pre-authorized")
	assert.Empty(t, servedNextPreauthorizes(newAdvisoryGate(t.TempDir(), "session-1"), "magus explain target:.:ci"),
		"the journal is per checkout: another tree's servings clear nothing here")
}

// TestReadServedNextIsRobustAndBounded covers the three things a journal appended to by a
// concurrent process does: it grows past the window, it carries a torn line, and it
// carries an entry naming no template.
func TestReadServedNextIsRobustAndBounded(t *testing.T) {
	gate := newAdvisoryGate(t.TempDir(), "session-1")
	for i := range servedNextWindow + 5 {
		serveNext(t, gate, "run-output", "magus", "query", "output", fmt.Sprintf("ref-%d", i))
	}
	assert.Len(t, readServedNext(gate), servedNextWindow,
		"only the recent window stays cleared")
	assert.Empty(t, servedNextPreauthorizes(gate, "magus query output ref-0"),
		"an entry that has aged out of the window is no longer magus's current suggestion")
	assert.Equal(t, "run-output", servedNextPreauthorizes(gate, "magus query output ref-24"))

	f, err := os.OpenFile(hint.ServedNextPath(gate.base), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"ts":1,"id":"","argv":["magus","doctor"]}` + "\n" + `{"ts":2,"id":"tor`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	assert.Empty(t, servedNextPreauthorizes(gate, "magus doctor"),
		"an entry naming no template is a clearance nobody can count")
	assert.Equal(t, "run-output", servedNextPreauthorizes(gate, "magus query output ref-24"),
		"a torn final line must not stop the entries behind it from clearing")
	assert.Empty(t, readServedNext(newAdvisoryGate(filepath.Join(t.TempDir(), "absent"), "s")),
		"a missing journal pre-authorizes nothing")
}

// TestServedNextIsOneJournalPerCheckout covers the producer that reports no session at
// all: `next` printed by a CLI run is what a hook call later meets, so the two doors
// have to read the same file.
func TestServedNextIsOneJournalPerCheckout(t *testing.T) {
	base := t.TempDir()
	cli := newAdvisoryGate(base, "")
	serveNext(t, cli, "query-explain", "magus", "explain", "target:.:ci")

	hook := newAdvisoryGate(base, "session-1")
	assert.Equal(t, "query-explain", servedNextPreauthorizes(hook, "./magus explain target:.:ci"),
		"the hook reports a session id; the CLI run that printed the breadcrumb did not")

	serveNext(t, hook, "query-refs", "magus", "refs", "hookCmd")
	assert.Equal(t, "query-refs", servedNextPreauthorizes(hook, "magus refs hookCmd"))
	assert.Equal(t, "query-explain", servedNextPreauthorizes(cli, "magus explain target:.:ci"),
		"a command served to one session clears it for another in the same tree")
}

// TestHookCmdStandsDownOnAServedNext is the rule in place: the same command is denied by a
// role-scoped rule and cleared once magus is the one that suggested it.
func TestHookCmdStandsDownOnAServedNext(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	ctx, _ := fleetFixture(t, narrowLease())
	gate := newAdvisoryGate(hookActivityTrail(ctx).base, "session-preauth")
	command := "./magus affected ci --no-default-charms"

	var denied bytes.Buffer
	err := hookCmd(ctx, strings.NewReader(command), &denied,
		[]string{"--lease", narrowLease().ID, "--session", "session-preauth", "-o", "name"})
	require.Error(t, err, "the gate rule refuses a lease that was handed a narrower check")
	assert.Equal(t, "deny\n", denied.String())

	serveNext(t, gate, "gate-run", "magus", "affected", "ci", "--no-default-charms")

	var cleared bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(command), &cleared,
		[]string{"--lease", narrowLease().ID, "--session", "session-preauth", "-o", "name"}))
	assert.Equal(t, "pass\n", cleared.String(),
		"magus refusing the command magus served is the tool disagreeing with itself")
}

// TestHookCmdNeverPreauthorizesAWorkspaceWideDeny is the carve-out. These refuse work that
// cannot be undone or that silently discards an exit status, and they protect everyone, so
// a journal entry naming one buys nothing.
func TestHookCmdNeverPreauthorizesAWorkspaceWideDeny(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	ctx, _ := fleetFixture(t)
	gate := newAdvisoryGate(hookActivityTrail(ctx).base, "session-carveout")

	for _, command := range []string{"git stash", "magus affected ci | tail -5", "go test ./..."} {
		serveNext(t, gate, "fabricated", strings.Fields(command)...)
		var out bytes.Buffer
		err := hookCmd(ctx, strings.NewReader(command), &out,
			[]string{"--session", "session-carveout", "-o", "name"})
		require.Error(t, err, "%q", command)
		assert.Equal(t, "deny\n", out.String(), "%q", command)
	}
}

// The guard and the breadcrumbs are one contract now: a command magus serves as `next` is
// pre-authorized, so a template the guard would refuse is a template that must not ship.
// The obligation the tests below carry is the upstream half of that bargain.
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
var nextIDRe = regexp.MustCompile(`breadcrumb\("([a-z-]+)"`)

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
		ReadOnly: true, ReadPaths: []string{"cmd/magus/**"},
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
					// Pre-authorization stands the focus rule down too, so a template
					// whose operands leave a reviewer's focus would clear with nothing
					// having graded it.
					func(ctx context.Context, lease, command string) string {
						if grade := gradeFocusRead(ctx, lease, command); grade.Decision == "deny" {
							return grade.Reason
						}
						return ""
					},
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
