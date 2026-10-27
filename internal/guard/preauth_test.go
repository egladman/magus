package guard

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// serveNext writes one journal entry in the shape the serving side appends, so the two
// sides of the contract are pinned by the same literal a reader can compare to the docs.
func serveNext(t *testing.T, gate hint.Gate, id string, argv ...string) {
	t.Helper()
	path := hint.ServedNextPath(gate.CacheDir())
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
	gate := hint.NewGate(t.TempDir(), "session-1")
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
	gate := hint.NewGate(t.TempDir(), "session-1")
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

	assert.Empty(t, servedNextPreauthorizes(hint.Gate{}, "magus explain target:.:ci"),
		"no workspace, no journal, nothing pre-authorized")
	assert.Empty(t, servedNextPreauthorizes(hint.NewGate(t.TempDir(), "session-1"), "magus explain target:.:ci"),
		"the journal is per checkout: another tree's servings clear nothing here")
}

// TestReadServedNextIsRobustAndBounded covers the three things a journal appended to by a
// concurrent process does: it grows past the window, it carries a torn line, and it
// carries an entry naming no template.
func TestReadServedNextIsRobustAndBounded(t *testing.T) {
	gate := hint.NewGate(t.TempDir(), "session-1")
	for i := range servedNextWindow + 5 {
		serveNext(t, gate, "run-output", "magus", "query", "output", fmt.Sprintf("ref-%d", i))
	}
	assert.Len(t, readServedNext(gate), servedNextWindow,
		"only the recent window stays cleared")
	assert.Empty(t, servedNextPreauthorizes(gate, "magus query output ref-0"),
		"an entry that has aged out of the window is no longer magus's current suggestion")
	assert.Equal(t, "run-output", servedNextPreauthorizes(gate, "magus query output ref-24"))

	f, err := os.OpenFile(hint.ServedNextPath(gate.CacheDir()), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(`{"ts":1,"id":"","argv":["magus","doctor"]}` + "\n" + `{"ts":2,"id":"tor`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	assert.Empty(t, servedNextPreauthorizes(gate, "magus doctor"),
		"an entry naming no template is a clearance nobody can count")
	assert.Equal(t, "run-output", servedNextPreauthorizes(gate, "magus query output ref-24"),
		"a torn final line must not stop the entries behind it from clearing")
	assert.Empty(t, readServedNext(hint.NewGate(filepath.Join(t.TempDir(), "absent"), "s")),
		"a missing journal pre-authorizes nothing")
}

// TestServedNextIsOneJournalPerCheckout covers the producer that reports no session at
// all: `next` printed by a CLI run is what a hook call later meets, so the two doors
// have to read the same file.
func TestServedNextIsOneJournalPerCheckout(t *testing.T) {
	base := t.TempDir()
	cli := hint.NewGate(base, "")
	serveNext(t, cli, "query-explain", "magus", "explain", "target:.:ci")

	hook := hint.NewGate(base, "session-1")
	assert.Equal(t, "query-explain", servedNextPreauthorizes(hook, "./magus explain target:.:ci"),
		"the hook reports a session id; the CLI run that printed the breadcrumb did not")

	serveNext(t, hook, "query-refs", "magus", "refs", "hookCmd")
	assert.Equal(t, "query-refs", servedNextPreauthorizes(hook, "magus refs hookCmd"))
	assert.Equal(t, "query-explain", servedNextPreauthorizes(cli, "magus explain target:.:ci"),
		"a command served to one session clears it for another in the same tree")
}

// The guard and the breadcrumbs are one contract now: a command magus serves as `next` is
// pre-authorized, so a template the guard would refuse is a template that must not ship.
// The obligation the tests below carry is the upstream half of that bargain.
//
// Graded PER ROLE, not once. A command is fine unbound and refused under a worker's write paths
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
	add := func(next []types.NextStep) {
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
		Out: []types.KnowledgeEdgeRef{{Other: "symbol:Evaluate"}},
	}))
	add(hint.NextForFiles([]types.FileEntry{
		{Path: "std/fs.go", Project: ".", Role: "source", SourceOf: []string{"."}},
		{Path: "MAGUS.md", Project: ".", Role: "output", OutputOf: []string{"."},
			Claims: []types.FileClaim{{Role: "output", Project: ".", Target: "generate"}}},
	}))
	add(hint.NextForAffected("ci", []string{"docs"}))
	add(hint.NextForFailure(".", "test", "out84fea3b6ae30"))
	add([]types.NextStep{hint.NextForDenial("grd84fea3b6ae30ab")})

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

// denyRemedyFixture is one refused line whose rule serves a remedy. ident is the constant
// the rule hands hint.NextForDenyRemedy, which is how the sweep below finds a rule with no
// fixture; setup builds whatever the rule reads, in the judged workspace and its cache
// when it reads either, and returns the call, which Judge makes under each role's lease,
// and its remedy.
type denyRemedyFixture struct {
	ident string
	rule  denyRuleName
	setup func(t *testing.T, at location) (call Request, want string)
}

func fixed(command, want string) func(*testing.T, location) (Request, string) {
	return func(*testing.T, location) (Request, string) { return Request{Input: command}, want }
}

func denyRemedyFixtures() []denyRemedyFixture {
	return []denyRemedyFixture{
		{"denyRuleOutputPipe", denyRuleOutputPipe, fixed("magus run lint . | tail -20", "magus run lint . -s")},
		{"denyRuleOutputPipe", denyRuleOutputPipe, fixed("magus ls jobs | wc -l", `magus ls jobs -o "template={{len .jobs}}"`)},
		{"denyRuleOutputRedirect", denyRuleOutputRedirect, fixed("magus ls jobs -o json > jobs.json", "magus ls jobs -o json --tee jobs.json")},
		{"denyRuleOutputRedirect", denyRuleOutputRedirect, fixed("magus ls jobs > jobs.txt", "magus ls jobs -o json --tee jobs.txt")},
		{"denyRuleOutputRedirect", denyRuleOutputRedirect, fixed("magus run lint . > /dev/null 2>&1", "magus run lint . --silent")},
		{"denyRuleStageAll", denyRuleStageAll, fixed("git add -A", "magus vcs ls-files --include-untracked --include-generated -o name | git add --pathspec-from-file=-")},
		{"denyRuleStageAll", denyRuleStageAll, fixed("git add -A -- cmd/magus/main.go", "magus vcs ls-files --include-untracked --include-generated cmd/magus/main.go -o name | git add --pathspec-from-file=-")},
		{"denyRuleProcessPoll", denyRuleProcessPoll, fixed("pgrep -fl magus", "magus status")},
		{"denyRuleProcessSignal", denyRuleProcessSignal, fixed("pkill -f 'magus job fork'", "magus status")},
		{"denyRuleChainedRun", denyRuleChainedRun, fixed("magus run lint . && magus run lint docs", "magus run lint . | magus run lint docs")},
		{"denyRuleChainedRun", denyRuleChainedRun, fixed("magus run generate:rw . && magus run test .", "magus run generate:rw . | magus run test .")},
		{"denyRuleSymbolSearch", denyRuleSymbolSearch, fixed("grep -rn MGS2011 docs/", "magus explain diagnostic:MGS2011")},
		{"denyRuleMagusTimeout", denyRuleMagusTimeout, fixed("timeout 600 magus run lint .", "magus run --timeout 10m lint .")},
		{"denyRuleMagusTimeout", denyRuleMagusTimeout, fixed("timeout 60 magus ls jobs", "magus ls jobs")},
		{"denyRuleGrepReader", denyRuleGrepReader, func(t *testing.T, at location) (Request, string) {
			src := "package api\n\n// HandleRequest serves one request.\nfunc HandleRequest() {}\n"
			require.NoError(t, os.WriteFile(filepath.Join(at.workspace, "handler.go"), []byte(src), 0o644))
			t.Chdir(at.workspace)
			return Request{Input: "grep -n 'func HandleRequest' -A20 handler.go"}, "sed -n 3,4p handler.go"
		}},
		{"denyRuleRawTool", denyRuleRawTool, func(t *testing.T, _ location) (Request, string) {
			spellName := register.Spell(t, "guard-remedy-test",
				spells.WithTargets("check"),
				spells.WithCommandRenderer(func(target string, _ []string) (string, []string, bool, error) {
					return "remedy-tool", []string{"check"}, target == "check", nil
				}),
			)
			return Request{Input: "remedy-tool check"}, fmt.Sprintf("magus run %s::check", spellName)
		}},
		{"denyRuleSiblingCheckout", denyRuleSiblingCheckout, func(t *testing.T, _ location) (Request, string) {
			main, wt := twoCheckouts(t)
			require.NoError(t, os.WriteFile(filepath.Join(wt, "magus"), []byte("#!/bin/sh\n"), 0o755))
			t.Chdir(main)
			// Relative, so the temp dir the fixture lives in does not read as a throwaway copy.
			return Request{Input: fmt.Sprintf("cd ../%s && ./magus describe job harness/worker", filepath.Base(wt))},
				fmt.Sprintf("%s --root %s describe job harness/worker", filepath.Join(wt, "magus"), wt)
		}},
		{"denyRuleSiblingCheckout", denyRuleSiblingCheckout, func(t *testing.T, _ location) (Request, string) {
			main, wt := twoCheckouts(t)
			require.NoError(t, os.WriteFile(filepath.Join(wt, "magus"), []byte("#!/bin/sh\n"), 0o755))
			t.Chdir(main)
			return Request{Input: fmt.Sprintf("cd ../%s && ./magus job exec harness/worker", filepath.Base(wt))},
				fmt.Sprintf("%s --root %s job exec harness/worker", filepath.Join(wt, "magus"), wt)
		}},
		{"denyRuleWorktreeRemove", denyRuleWorktreeRemove, func(t *testing.T, at location) (Request, string) {
			r := newWorktreeRepository(t)
			wt, row := r.workerWorktree(types.StatePass)
			row.ID = "remedy-lock"
			seedRemedyJob(t, at, row)
			writeSpawnedAgent(hint.NewGate(at.cacheDir, FactsKey("claude", "remedy")), "agent-remedy", spawnedAgent{Description: "root/worker remedy-lock"})
			return Request{Input: "git worktree remove " + wt, Dir: r.main, Host: "claude", Session: "remedy"}, "git worktree remove -f -f " + wt
		}},
		{"denyRuleWorktreeRemove", denyRuleWorktreeRemove, func(t *testing.T, at location) (Request, string) {
			r := newWorktreeRepository(t)
			wt, row := r.workerWorktree(types.StateExited)
			row.ID, row.Result = "remedy-wait", &types.JobResult{}
			seedRemedyJob(t, at, row)
			return Request{Input: "git worktree remove " + wt, Dir: r.main}, hint.JobWait.With("remedy-wait")
		}},
	}
}

// seedRemedyJob files row in the job store the fixtures are judged against.
func seedRemedyJob(t *testing.T, at location, row types.Job) {
	t.Helper()
	_, err := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: at.cacheDir, Root: at.workspace}).Update(t.Context(), row.ID, func(cur *types.Job) { *cur = row })
	require.NoError(t, err)
}

// remedyRe finds the rules that construct a remedy in the guard's own source.
var remedyRe = regexp.MustCompile(`NextForDenyRemedy\(string\((denyRule\w+)\)`)

// TestEveryServedNextCoversEveryDenyRemedy is the sweep: a rule that serves a remedy with
// no fixture above is a remedy nothing grades through the guard.
func TestEveryServedNextCoversEveryDenyRemedy(t *testing.T) {
	covered := map[string]bool{}
	for _, f := range denyRemedyFixtures() {
		covered[f.ident] = true
	}
	sources, err := filepath.Glob("*.go")
	require.NoError(t, err)
	found := 0
	for _, src := range sources {
		if strings.HasSuffix(src, "_test.go") {
			continue
		}
		body, err := os.ReadFile(src)
		require.NoError(t, err)
		for _, m := range remedyRe.FindAllStringSubmatch(string(body), -1) {
			found++
			assert.True(t, covered[m[1]], "%s serves a %s remedy and no fixture in denyRemedyFixtures drives it", src, m[1])
		}
	}
	require.NotZero(t, found, "remedyRe no longer matches how a rule builds its remedy")
}

// servedRole is a caller a next is served to, as Judge reads it: who calls and the lease it
// acts under. The directory is the call's own, since a next runs where its call ran.
type servedRole struct {
	name string
	call Request
	// worker is whether the lease acts as a worker's, which the rules that word a remedy read.
	worker bool
}

// servedRoles stands up, over one fleet, every caller a next is served to: a person, a root
// holding a row it handed out, a worker and a reviewer in their own checkout, a holder
// resumed on a row it exited, and a magus-scout, which has no Skill tool.
func servedRoles(t *testing.T) (context.Context, []servedRole) {
	t.Helper()
	worker := narrowLease()
	worker.ID, worker.Parent = "harness/worker", "harness/root"
	exited := narrowLease()
	exited.ID, exited.Parent, exited.State = "harness/exited", "harness/root", types.StateExited
	reviewer := types.Job{
		ID: "harness/reviewer", Criteria: "read the guard",
		ReadOnly: true, ReadPaths: []string{"cmd/magus/**"},
		State: types.StateRunning, Registered: 1,
	}
	handed := types.Job{ID: "harness/handed", Criteria: "a row the root handed out", WritePaths: []string{"docs/**"}, State: types.StateDeclared}
	ctx, _ := fleetFixture(t, worker, exited, reviewer, handed)
	at := hookLocation(ctx, Dependencies{})
	const host, session = "claude-code", "served"
	hostGate(at.cacheDir, host).MarkFired(hostAgentsKind)
	facts := hint.NewGate(at.cacheDir, FactsKey(host, session))
	writeCostState(facts, costState{Forked: []string{handed.ID}})
	writeAgentMarker(facts, agentTypeKind("a-scout"), []byte(`{"type":"magus-scout"}`))
	tools := testTools(t)
	as := func(agent, lease string) Request {
		return Request{Host: host, Session: session, Agent: agent, Lease: lease, ObservesSkillLoads: true, Tools: tools}
	}
	groundwork := as("", "")
	groundwork.Input, groundwork.DryRun = "git show HEAD", true
	require.Equal(t, string(denyRuleOrchestratorGroundwork), Judge(ctx, strict(Dependencies{}), groundwork).Rule, "the root is proven to orchestrate")
	require.False(t, Dependencies{facts: facts, caller: job.Caller{Agent: "a-scout"}}.loadsSkills(), "the scout cannot load a skill")
	return ctx, []servedRole{
		{name: "person"},
		{name: "root with a handed-out row", call: as("", "")},
		{name: "worker in its own checkout", call: as("a-worker", worker.ID), worker: true},
		{name: "exited holder resumed", call: as("a-exited", exited.ID), worker: true},
		{name: "reviewer", call: as("a-reviewer", reviewer.ID), worker: true},
		{name: "magus-scout", call: as("a-scout", "")},
	}
}

// servedAs is call made by role: the call's own attribution wins where it set one.
func servedAs(call Request, role servedRole) Request {
	call.Host = cmp.Or(call.Host, role.call.Host)
	call.Session = cmp.Or(call.Session, role.call.Session)
	call.Agent = cmp.Or(call.Agent, role.call.Agent)
	call.Lease = cmp.Or(call.Lease, role.call.Lease)
	call.ObservesSkillLoads = call.ObservesSkillLoads || role.call.ObservesSkillLoads
	call.Tools = role.call.Tools
	return call
}

// gradeServed fails t when the guard refuses run to the caller call names, where call runs.
// A dry run, so grading one next spends nothing a later one reads.
func gradeServed(t *testing.T, ctx context.Context, deps Dependencies, call Request, role, id, run string) {
	t.Helper()
	call.Input, call.DryRun = run, true
	if v := Judge(ctx, deps, call); v.Decision == decisionDeny {
		t.Errorf("the %q next serves %q, which the guard refuses for a %s:\n%s", id, run, role, v.Reason)
	}
}

// servedFixture is a next magus serves outside Judge's own remedies: setup returns the call
// the caller made and the lines served for it. A role the call is refused to is never served
// them, so they are graded only for the roles the call passes.
type servedFixture struct {
	name  string
	setup func(t *testing.T, ctx context.Context, deps Dependencies, role servedRole) (call Request, next []string)
}

// rootMergeServesBaseScript is the merge-base script line orchestrator-groundwork serves a
// root's `git merge`, with its placeholder filled.
func rootMergeServesBaseScript(t *testing.T, ctx context.Context, deps Dependencies, role servedRole) (Request, []string) {
	if role.name != "root with a handed-out row" {
		return Request{}, nil
	}
	workspace := hookLocation(ctx, Dependencies{}).workspace
	servesScript(t, workspace, "hack/dev/merge-base-into-branches.buzz", "bring the base into each branch", "replaces:git merge")
	call := servedAs(Request{Input: "git merge origin/main"}, role)
	call.DryRun = true
	v := Judge(ctx, deps, call)
	require.Equal(t, string(denyRuleOrchestratorGroundwork), v.Rule, v.Reason)
	var next []string
	for _, m := range backtickRe.FindAllStringSubmatch(v.Reason+v.Context, -1) {
		if strings.Contains(m[1], "merge-base-into-branches.buzz") {
			next = append(next, placeholderRe.ReplaceAllString(m[1], "guard-merge-policy"))
		}
	}
	require.NotEmpty(t, next, "the refusal names the merge-base script:\n%s%s", v.Reason, v.Context)
	return Request{}, next
}

// seatWaitServesTheRun is MGS3009's remedy: the wait piped into the run that spends the seat.
func seatWaitServesTheRun(*testing.T, context.Context, Dependencies, servedRole) (Request, []string) {
	const run = "magus run go::go-test . -- ./internal/ledger/"
	return Request{Input: run}, []string{fmt.Sprintf("%s | %s", hint.Status.With("--wait"), run)}
}

func servedFixtures() []servedFixture {
	return []servedFixture{
		{"a root's merge names the script that brings the base in", rootMergeServesBaseScript},
		{"MGS3009 waits in the call that spends the seat", seatWaitServesTheRun},
		{"a stale index's rebuild", func(t *testing.T, _ context.Context, _ Dependencies, role servedRole) (Request, []string) {
			standBudget(t, types.MachineSnapshot{BudgetSlots: 1, HeldSlots: 1})
			root := recordedSearchTree(t)
			cacheDir := t.TempDir()
			require.NoError(t, knowledge.WriteGuardIndex(cacheDir, root, knowledge.NewGraph(), true, knowledge.GuardCheckout{}))
			stale := strict(Dependencies{
				SymbolDefined: func(name string) (bool, bool) { return name == "CheckStatus", false },
				CacheDir:      func(string) (string, error) { return cacheDir, nil },
				Spells:        spellCatalog,
				scope:         workspaceScope{root: root},
				callDir:       root,
				lease:         role.call.Lease,
				worker:        role.worker,
			})
			v := Evaluate(stale, "grep -rn CheckStatus internal")
			require.Equal(t, denyRule{Name: denyRuleSymbolSearch, Arg: "CheckStatus"}, v.Rule)
			var next []string
			for _, n := range v.Next {
				next = append(next, n.Run)
			}
			if !role.worker {
				require.NotEmpty(t, next, "a caller that may wait for a seat is served the wait")
			}
			return Request{}, next
		}},
		{"gh pr checks names a failure read the caller can run", func(t *testing.T, ctx context.Context, deps Dependencies, role servedRole) (Request, []string) {
			deps.CommandRule = workspacePolicy(t).CommandRule()
			call := servedAs(Request{Input: "gh pr checks 183"}, role)
			call.DryRun = true
			v := Judge(ctx, deps, call)
			require.Equal(t, decisionDeny, v.Decision, "gh pr checks is refused to every role")
			// The refusal names more than one read; the caller needs one it can run.
			var runnable []string
			for _, m := range backtickRe.FindAllStringSubmatch(v.Reason, -1) {
				read := placeholderRe.ReplaceAllString(m[1], "0123abcd")
				if !strings.Contains(read, "show-failures") && !strings.Contains(read, "--log-failed") {
					continue
				}
				call.Input = read
				if Judge(ctx, deps, call).Decision != decisionDeny {
					runnable = append(runnable, read)
				}
			}
			require.NotEmpty(t, runnable, "the refusal names a failure read a %s can run:\n%s", role.name, v.Reason)
			return Request{}, nil
		}},
		{"a subagent's stop names the call that stops each task", func(t *testing.T, _ context.Context, _ Dependencies, role servedRole) (Request, []string) {
			if role.call.Agent == "" {
				return Request{}, nil
			}
			started := bgTime(t, "2026-10-13T11:37:41.120Z")
			v, ok := judgeStop(strict(Dependencies{}), hint.Gate{}, subagentStop(t, role.call.Agent, writeTranscript(t, bgSearchCall, bgSearchResult)), "", started.Add(time.Hour))
			require.True(t, ok)
			require.Equal(t, decisionDeny, v.Decision)
			assert.NotContains(t, v.Reason, "completion notice", "a subagent refused its stop cannot wait for a notice that comes after it")
			var next []string
			for _, m := range taskStopRe.FindAllStringSubmatch(v.Reason, -1) {
				next = append(next, hookJSON(t, map[string]any{
					"session_id": role.call.Session, "agent_id": role.call.Agent, "hook_event_name": "PreToolUse",
					"tool_name": "TaskStop", "tool_input": map[string]any{"task_id": m[1]},
				}))
			}
			require.NotEmpty(t, next, "the refusal names a stop call per task:\n%s", v.Reason)
			return Request{}, next
		}},
	}
}

var (
	backtickRe    = regexp.MustCompile("`([^`]+)`")
	placeholderRe = regexp.MustCompile(`<[a-z-]+>`)
	taskStopRe    = regexp.MustCompile(`TaskStop \{"task_id": "([^"]+)"\}`)
)

// workspacePolicy is the command, spawn and write rules the root magusfile registers,
// loaded once for the package's tests the way the hook loads them.
var workspacePolicy = func() func(t *testing.T) *WorkspaceRules {
	var (
		once  sync.Once
		rules *WorkspaceRules
		err   error
	)
	return func(t *testing.T) *WorkspaceRules {
		t.Helper()
		once.Do(func() {
			var root string
			if root, err = workspace.FindRoot(""); err == nil {
				rules, err = LoadWorkspaceRules(t.Context(), root, types.VCSOptions{})
			}
		})
		require.NoError(t, err)
		return rules
	}
}()

// TestEveryServedNextPassesTheGuardForEveryRole is the structural half of the
// pre-authorization bargain: every next magus serves is graded through the whole of Judge,
// as the caller it is served to, where that caller runs.
func TestEveryServedNextPassesTheGuardForEveryRole(t *testing.T) {
	ctx, roles := servedRoles(t)
	templates := servedNextTemplates(t)
	readScript, writeScript := scriptNexts(t, hookLocation(ctx, Dependencies{}).workspace)
	deps := strict(loadingWorkspace(t))
	deps.Spells = testDependencies().Spells
	deps.GraphIDs = diagnosticGraph
	// A bound worker's raw tool is worker-check-only's by default; lowered, the raw-tool
	// remedy is what this role is served.
	deps.Builtins[string(denyRuleWorkerCheckOnly)] = builtin.Setting{Decision: builtin.Advise}

	for _, role := range roles {
		for id, run := range templates {
			t.Run(fmt.Sprintf("%s/%s", role.name, id), func(t *testing.T) { gradeServed(t, ctx, deps, servedAs(Request{}, role), role.name, id, run) })
		}
		t.Run(role.name+"/scripts", func(t *testing.T) {
			kind, writePaths := hint.LeaseRole(fixtureRows(t, ctx), role.call.Lease)
			served := hint.FilterServable(kind, writePaths, []types.NextStep{readScript, writeScript})
			require.Contains(t, served, readScript, "a read script is served to every role")
			if kind == hint.RoleReviewer {
				assert.NotContains(t, served, writeScript, "a write script is never served to a reviewer")
			}
			for _, n := range served {
				gradeServed(t, ctx, deps, servedAs(Request{}, role), role.name, n.ID, n.Run)
			}
		})
		// A deny's remedy is graded as Judge serves it to this role: whatever it drops was
		// never served, and whatever it keeps must pass.
		for _, f := range denyRemedyFixtures() {
			t.Run(fmt.Sprintf("%s/deny-%s", role.name, string(f.rule)), func(t *testing.T) {
				call, want := f.setup(t, hookLocation(ctx, Dependencies{}))
				call = servedAs(call, role)
				call.DryRun = true
				v := Judge(ctx, deps, call)
				if role.call.Host == "" {
					shape := verdictWithRule("deny", string(f.rule))
					shape.Next = v.Next
					require.Equal(t, shape, unworded(v), call.Input)
					require.Len(t, v.Next, 1, "unbound, %q serves its remedy", call.Input)
					assert.Equal(t, want, v.Next[0].Run)
				}
				for _, n := range v.Next {
					assert.Equal(t, "deny-"+string(f.rule), n.ID)
					gradeServed(t, ctx, deps, call, role.name, n.ID, n.Run)
				}
			})
		}
		for _, f := range servedFixtures() {
			t.Run(fmt.Sprintf("%s/%s", role.name, f.name), func(t *testing.T) {
				call, next := f.setup(t, ctx, deps, role)
				call = servedAs(call, role)
				if call.Input != "" {
					probe := call
					probe.DryRun = true
					if Judge(ctx, deps, probe).Decision == decisionDeny {
						return
					}
				}
				for _, run := range next {
					gradeServed(t, ctx, deps, call, role.name, f.name, run)
				}
			})
		}
	}
}

// fixtureRows reads the job store the fixture's context pins.
func fixtureRows(t *testing.T, ctx context.Context) []types.Job {
	t.Helper()
	rows, err := leaseRows(ctx, hookLocation(ctx, Dependencies{}))
	require.NoError(t, err)
	return rows
}

// loadingWorkspace is a root whose generate target writes cmd/magus/gen, inside the
// narrow worker's write paths. It loads, so a write template is judged on the
// workspace's declarations rather than refused as a run nothing could check.
func loadingWorkspace(t *testing.T) Dependencies {
	t.Helper()
	const output = "cmd/magus/gen/*.go"
	root := &types.Project{
		Path: ".", MagusfileTargets: []string{"generate"},
		TargetOutputs: map[string][]types.OutputRef{"generate": {{Glob: output}}},
	}
	ws := typestest.NewMockWorkspaceRepository(t)
	ws.EXPECT().ClassifyFiles(mock.Anything, mock.Anything).Return([]types.FileEntry{{
		Path: "cmd/magus/gen/flags.go", Project: ".", Role: "output",
		Claims: []types.FileClaim{{Project: ".", Target: "generate", Role: "output", Glob: output}},
	}}, nil).Maybe()
	ws.EXPECT().TargetGraph(mock.Anything).Return(types.TargetGraphOutput{}, nil).Maybe()
	ws.EXPECT().Get(".").Return(root).Maybe()
	ws.EXPECT().Get(mock.Anything).Return(nil).Maybe()
	ws.EXPECT().All().Return([]*types.Project{root}).Maybe()
	ws.EXPECT().Where(mock.Anything).Return(root, true).Maybe()
	return Dependencies{Inspect: func(context.Context, string) (types.WorkspaceRepository, error) { return ws, nil }}
}

// scriptNexts serves one read and one write script out of an index written under root,
// through the same lookup the serving sites use.
func scriptNexts(t *testing.T, root string) (read, write types.NextStep) {
	t.Helper()
	entry := func(rel, effect string, args ...string) hint.ScriptServe {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte("export fun main() > void {}\n"), 0o644))
		info, err := os.Stat(abs)
		require.NoError(t, err)
		return hint.ScriptServe{Path: rel, Args: args, Effect: effect, MtimeNs: info.ModTime().UnixNano(), Size: info.Size()}
	}
	cacheDir := t.TempDir()
	require.NoError(t, hint.WriteScriptsIndex(cacheDir, hint.ScriptsIndex{Root: root, Serves: map[string][]hint.ScriptServe{
		"next:run-output": {
			entry("hack/dev/ls-test-failures.buzz", hint.ScriptEffectRead, "--ref", "{ref}"),
			entry("hack/dev/merge-job-branches.buzz", hint.ScriptEffectWrite),
		},
	}}))
	next, err := hint.MatchScripts(cacheDir, "next:run-output", map[string]string{"ref": "out84fea3b6ae30"})
	require.NoError(t, err)
	require.Len(t, next, 2)
	require.True(t, next[0].Reads)
	require.False(t, next[1].Reads)
	return next[0], next[1]
}

// TestServedNextTemplatesAreRunnable pins the property the pre-authorization reader
// depends on: a breadcrumb is a complete argv, not a form to fill in.
//
// A placeholder passes every rule above and then fails in the shell, which is the failure
// mode measured across the templates on 2026-09-11: 30 of 30 templates cleared the guard and
// the ones carrying `<path>` broke on the reader's own redirect.
func TestServedNextTemplatesAreRunnable(t *testing.T) {
	for id, run := range servedNextTemplates(t) {
		assert.NotContains(t, run, "<", "the %q breadcrumb still carries a placeholder: %q", id, run)
		cmds, ok := ParseCommands(run)
		require.True(t, ok, "the %q breadcrumb does not parse as a shell command: %q", id, run)
		require.Len(t, cmds, 1, "a breadcrumb is one invocation: %q", run)
		assert.True(t, slices.Contains([]string{"magus", "./magus"}, cmds[0].Name),
			"the %q breadcrumb runs %q rather than magus", id, cmds[0].Name)
		assert.False(t, strings.Contains(run, "|") || strings.Contains(run, ">"),
			"the %q breadcrumb filters magus's own output: %q", id, run)
	}
}
