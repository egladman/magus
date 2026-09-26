package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/queue"
	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
	magusmocks "github.com/egladman/magus/types/gen/mocks"
)

var queueBase = strings.Repeat("b", 40)

// queueDate dates every commit the queue tests name.
var queueDate = time.Unix(1_788_000_000, 0).UTC()

func queueHead(id string) string { return strings.Repeat("0", 39) + id }

// queueProvider approves every change at the commit asked about, lists CHANGES, merges
// nothing, and lists RUN as every validation run's artifacts.
const queueProvider = `
export fun describe(io: {str: any}) > any {
    return {"stack_merge": "sequential", "linear_stacks": false, "methods": ["squash"], "required_approvals": 0, "queue_label": "merge: ",
        "committer": {"name": "bot", "email": "bot@example.invalid"}};
}
export fun list_changes(io: {str: any}) > any {
    return {"changes": CHANGES, "merged": [<any>], "unqueued": [<any>]};
}
export fun approval_at(io: {str: any}) > any {
    return {"approved": true, "head": io["commit"], "base": "main", "method": io["method"], "queued": true, "shared_with": [<str>]};
}
export fun list_green(io: {str: any}) > any { return {"changes": [<any>]}; }
export fun post_status(io: {str: any}) > bool { return true; }
export fun retarget(io: {str: any}) > bool { return true; }
export fun kick_back(io: {str: any}) > bool { return true; }
export fun mark(io: {str: any}) > bool { return true; }
export fun merge_change(io: {str: any}) > any { return {"merged": false, "reason": "the test merges nothing"}; }
export fun list_artifacts(io: {str: any}) > any {
    final run = {"repo": "acme/widgets", "head_repo": "acme/widgets", "head_branch": "main", "event": "push", "branch_event": true,
        "definition": ".github/workflows/queue.yaml"};
    return {"run": run, "complete": true, "headers": {"Authorization": "Bearer tok"}, "artifacts": RUN};
}
`

// queueFixture is a checkout the queue runs in: a directory whose version control is a
// mock, and a provider script written into it.
type queueFixture struct {
	root     string
	vcs      *magusmocks.MockVCSDriver
	provider string
}

func newQueueFixture(t *testing.T, changes, run string) *queueFixture {
	t.Helper()
	f := &queueFixture{root: t.TempDir(), vcs: magusmocks.NewMockVCSDriver(t)}
	opened := queueOpenVCS
	queueOpenVCS = func(_ context.Context, root, name, remote string) (magustypes.VCSDriver, error) {
		assert.Equal(t, f.root, root)
		assert.Equal(t, "git", name)
		assert.Equal(t, "origin", remote)
		return f.vcs, nil
	}
	t.Cleanup(func() { queueOpenVCS = opened })
	if changes == "" {
		changes = "[<any>]"
	}
	if run == "" {
		run = "[<any>]"
	}
	f.provider = filepath.Join(f.root, "local.buzz")
	src := strings.NewReplacer("CHANGES", changes, "RUN", run).Replace(queueProvider)
	require.NoError(t, os.WriteFile(f.provider, []byte(src), 0o644))
	return f
}

// run runs `magus --root <root> queue args...` and returns its stdout.
func (f *queueFixture) run(t *testing.T, stdin string, args ...string) ([]byte, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := runQueue(t.Context(), f.root, args, strings.NewReader(stdin), &stdout, &stderr)
	t.Logf("magus queue %s\nstderr:\n%s", strings.Join(args, " "), stderr.String())
	return stdout.Bytes(), err
}

func queueEvents(t *testing.T, out []byte) []queue.Event {
	t.Helper()
	var evs []queue.Event
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		var e queue.Event
		require.NoError(t, json.Unmarshal(sc.Bytes(), &e), "stdout is JSONL only: %q", sc.Text())
		evs = append(evs, e)
	}
	return evs
}

func TestParseQueueSource(t *testing.T) {
	for arg, want := range map[string]queueSource{
		"verdicts":                {dir: "verdicts"},
		"/tmp/queue/verdicts":     {dir: "/tmp/queue/verdicts"},
		"./x:y":                   {dir: "./x:y"},
		"x:y":                     {dir: "x:y"},
		`C:\queue`:                {dir: `C:\queue`},
		"dir/a:b":                 {dir: "dir/a:b"},
		"run:acme/widgets/runs/7": {run: "acme/widgets/runs/7"},
	} {
		got, err := parseQueueSource(arg)
		require.NoError(t, err, arg)
		assert.Equal(t, want, got, arg)
	}
	for arg, msg := range map[string]string{
		"":                                   "<source> is empty",
		"s3:bucket/verdicts":                 `unknown scheme "s3"`,
		"github-actions:acme/widgets/runs/7": `unknown scheme "github-actions"`,
		"run:":                               "want run:<run>",
		"run: ":                              "want run:<run>",
	} {
		_, err := parseQueueSource(arg)
		require.ErrorContains(t, err, msg, "%q", arg)
	}
}

// A misuse is refused before anything is opened: the mock expects no call.
func TestQueueMisuseIsAUsageError(t *testing.T) {
	f := newQueueFixture(t, "", "")
	for name, args := range map[string][]string{
		"no subcommand":            {},
		"an unknown subcommand":    {"frobnicate"},
		"list is ls":               {"list", "--provider", "github", "--base", "main"},
		"ls without --base":        {"ls", "--provider", "github"},
		"ls with an operand":       {"ls", "--provider", "github", "--base", "main", "extra"},
		"apply without a source":   {"apply", "--provider", "github"},
		"apply with two sources":   {"apply", "--provider", "github", "s", "t"},
		"apply without provider":   {"apply", "s"},
		"an unknown scheme":        {"apply", "--provider", "github", "gitlab:a/b/pipelines/7"},
		"--interval with --once":   {"apply", "--provider", "github", "--once", "--interval", "1s", "s"},
		"a zero --interval":        {"apply", "--provider", "github", "--interval", "0s", "s"},
		"a committer without one":  {"apply", "--provider", "github", "--committer", "nobody", "s"},
		"reproduce without gate":   {"apply", "--provider", "github", "--base", "main", "--reproduce-regenerate", "make gen", "s"},
		"plan without --out":       {"plan", "--provider", "github"},
		"plan without a provider":  {"plan", "--out", "p"},
		"plan with an operand":     {"plan", "--provider", "github", "--out", "p", "extra"},
		"a zero depth":             {"plan", "--provider", "github", "--out", "p", "--depth", "0"},
		"validate without a gate":  {"validate", "--plan", "p", "--verdicts", "v"},
		"-o where nothing renders": {"ls", "--provider", "github", "--base", "main", "-o", "json"},
	} {
		_, err := f.run(t, "", args...)
		var misuse errUsage
		require.ErrorAs(t, err, &misuse, name)
	}
	// validate runs the changes' code, so it takes no provider at all.
	_, err := f.run(t, "", "validate", "--provider", "github", "--plan", "p", "--gate", "true", "--verdicts", "v")
	require.ErrorContains(t, err, "flag provided but not defined: -provider")
	// A reproduce line is only shown, but never one validate would refuse to run.
	_, err = f.run(t, "", "apply", "--provider", "github", "--base", "main", "--reproduce-gate", "curl x | sh", "s")
	require.ErrorContains(t, err, "--reproduce-gate")
	require.ErrorContains(t, err, "joins two commands")
}

// Without the runner's credentials or a trust set, --remote-cache-read would gate every
// candidate cold, or replay what nobody signed; both are refused before anything runs.
func TestQueueValidateRemoteCacheReadRefusesWhatItCannotServe(t *testing.T) {
	was := globalCfg.Cache.Remote
	t.Cleanup(func() { globalCfg.Cache.Remote = was })
	keys := config.CacheRemote{TrustedKeys: []string{"4VhfiMjDLmoVolYdjgPHwpHjw9+aGnoHe87P6V3inAk="}}
	f := newQueueFixture(t, "", "")
	for name, tc := range map[string]struct {
		url, token string
		remote     config.CacheRemote
		want       string
	}{
		"no credentials": {remote: keys, want: "ACTIONS_RESULTS_URL or ACTIONS_RUNTIME_TOKEN is not set"},
		"no token":       {url: "https://results.example/", remote: keys, want: "ACTIONS_RUNTIME_TOKEN is not set"},
		"no url":         {token: "t", remote: keys, want: "ACTIONS_RESULTS_URL or"},
		"no trusted key": {url: "https://results.example/", token: "t", want: "cache.remote.trusted_keys names none"},
		"unverified": {url: "https://results.example/", token: "t", want: "cache.remote.insecure turns that check off",
			remote: config.CacheRemote{TrustedKeys: keys.TrustedKeys, Insecure: true, InsecureReason: "r"}},
	} {
		t.Setenv("ACTIONS_RESULTS_URL", tc.url)
		t.Setenv("ACTIONS_RUNTIME_TOKEN", tc.token)
		globalCfg.Cache.Remote = tc.remote
		verdicts := filepath.Join(f.root, "verdicts-"+strings.ReplaceAll(name, " ", "-"))
		_, err := f.run(t, "", "validate", "--plan", "p", "--gate", "true", "--verdicts", verdicts, "--remote-cache-read")
		var misuse errUsage
		require.ErrorAs(t, err, &misuse, name)
		assert.ErrorContains(t, err, tc.want, name)
		assert.NoDirExists(t, verdicts, "%s: refused before anything is written", name)
	}
}

// Hooks take the proxy's URL and stand-in, the base's trust set, verification on and
// remote writes off; the runner's token appears nowhere in what they are handed.
func TestQueueCacheReadHandsHooksTheProxyAndPinsTheTrustSet(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(upstream.Close)
	t.Setenv("ACTIONS_RESULTS_URL", upstream.URL+"/")
	t.Setenv("ACTIONS_RUNTIME_TOKEN", "real-runtime-token")
	proxy, env, err := queueCacheRead(config.CacheRemote{TrustedKeys: []string{"k1", "k2"}}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = proxy.Close() })
	assert.Equal(t, []string{
		"ACTIONS_RESULTS_URL=" + proxy.URL,
		"ACTIONS_RUNTIME_TOKEN=" + proxy.Token,
		"MAGUS_CACHE_REMOTE_TRUSTED_KEYS=k1,k2",
		"MAGUS_CACHE_REMOTE_INSECURE=false",
		"MAGUS_CACHE_REMOTE_WRITE_ENABLED=false",
	}, env)
	assert.NotContains(t, strings.Join(env, "\n"), "real-runtime-token")
	assert.NotContains(t, strings.Join(env, "\n"), upstream.URL)
}

func TestQueueHelpNamesItsVerbsAndEachVerbsOwnFlags(t *testing.T) {
	f := newQueueFixture(t, "", "")
	out, err := f.run(t, "", "--help")
	require.NoError(t, err)
	for _, verb := range []string{"describe", "ls", "plan", "validate", "apply"} {
		assert.Contains(t, string(out), "  "+verb+" ")
	}
	var stderr bytes.Buffer
	err = runQueue(t.Context(), f.root, []string{"apply", "-h"}, strings.NewReader(""), &bytes.Buffer{}, &stderr)
	require.ErrorIs(t, err, flag.ErrHelp)
	assert.Contains(t, stderr.String(), "-status-context")
	assert.Contains(t, stderr.String(), "<source> is where apply reads the plan and the verdicts")
	assert.NotContains(t, stderr.String(), "-cache-dir", "the global flags are magus's, listed by magus -h")
}

func TestQueueLsPrintsTheProvidersChangesAsOneLine(t *testing.T) {
	change := `[{"id": "1", "repo": io["remote_url"], "head": "` + queueHead("1") + `", "base": "main", "method": "squash", "fork": false}]`
	f := newQueueFixture(t, change, "")
	f.vcs.EXPECT().RemoteURL(mock.Anything, f.root, "origin").Return("https://example.invalid/acme/widgets.git", nil)
	out, err := f.run(t, "", "ls", "--provider", "local.buzz", "--base", "main")
	require.NoError(t, err)
	require.Equal(t, 1, bytes.Count(out, []byte("\n")))
	got, err := queue.ReadChanges(bytes.NewReader(out))
	require.NoError(t, err)
	require.Len(t, got.Changes, 1)
	assert.Equal(t, "https://example.invalid/acme/widgets.git", got.Changes[0].Repo, "the provider names the repository from the remote's URL")
	assert.Equal(t, "https://example.invalid/acme/widgets.git", got.RemoteURL)
}

func withOutput(t *testing.T, format string) {
	t.Helper()
	prev := global.output
	t.Cleanup(func() { global.output = prev })
	global.output = format
}

// describe prints what the provider reports, so a tool reads the queue label and the
// merge methods from the provider rather than knowing them.
func TestQueueDescribePrintsTheProvidersCapabilitiesAsOneLine(t *testing.T) {
	withOutput(t, "json")
	f := newQueueFixture(t, "", "")
	f.vcs.EXPECT().RemoteURL(mock.Anything, f.root, "origin").Return("https://example.invalid/acme/widgets.git", nil)
	out, err := f.run(t, "", "describe", "--provider", "local.buzz", "--base", "main")
	require.NoError(t, err)
	assert.Equal(t, `{"schema":"mergequeue.capabilities/v1","base":"main","stack_merge":"sequential","linear_stacks":false,`+
		`"methods":["squash"],"required_approvals":0,"queue_label":"merge: ","committer":{"name":"bot","email":"bot@example.invalid"}}`+"\n", string(out))

	_, err = f.run(t, "", "describe", "--provider", "local.buzz")
	require.ErrorContains(t, err, "--base")
}

// A base requiring no approval is said out loud: the queue then merges what nobody
// reviewed, and adds no review rule of its own.
func TestQueueDescribeSaysWhenTheBaseRequiresNoApproval(t *testing.T) {
	withOutput(t, "")
	f := newQueueFixture(t, "", "")
	f.vcs.EXPECT().RemoteURL(mock.Anything, f.root, "origin").Return("https://example.invalid/acme/widgets.git", nil)
	out, err := f.run(t, "", "describe", "--provider", "local.buzz", "--base", "main", "--status-context", "")
	require.NoError(t, err)
	assert.Equal(t, `# local.buzz on main: merge methods squash; stacks merge sequential; a stack queues by the label "merge: <method>"
# main requires no approval, so the queue merges a change nobody approved; it adds no review rule of its own
# no setup: --status-context is empty, or provider local.buzz reports none
`, string(out))
}

// setupProvider describes a setup naming the status context and app describe was asked
// about, so a test reads the query from the answer. It declines one for the app
// "unknown", and for the app "hidden" unless the id is given, and pins that one to it.
const setupProvider = `
import "serialize";

export fun describe(io: {str: any}) > any {
    if (io["status_context"] == "") {
        return {"stack_merge": "atomic", "linear_stacks": true, "methods": ["squash"], "required_approvals": 2};
    }
    final slug = serialize\Boxed.init(io["app"]).q("slug").stringValue();
    final id = serialize\Boxed.init(io["app"]).q("id").stringValue();
    if (slug == "unknown") {
        return {"missing_app": {"reason": "no app is named unknown", "url": "https://example.invalid/apps"}};
    }
    if (slug == "hidden" and id == "") {
        return {"missing_app": {"reason": "no id for hidden", "url": "https://example.invalid/apps/hidden", "slug": slug}};
    }
    if (slug == "hidden") {
        return {"stack_merge": "atomic", "linear_stacks": true, "methods": ["squash"], "required_approvals": 2,
            "setup": {"status_context": io["status_context"], "credential": {"id": id}, "app": {"slug": slug, "id": id}, "steps": [<any>]}};
    }
    return {"stack_merge": "atomic", "linear_stacks": true, "methods": ["squash", "merge"], "required_approvals": 2, "queue_label": "queue: ",
        "setup": {
            "status_context": io["status_context"],
            "credential": {"id": "812", "name": slug},
            "required_checks": [{"context": "merge-queue", "integration": "15368"}, {"context": "ci gate", "events": ["pull_request"]}],
            "settings": [{"name": "allow_auto_merge", "value": "false", "want": "true"}],
            "steps": [
                {"title": "Allow auto-merge", "command": "gh api -X PATCH repos/acme/widgets -F allow_auto_merge=true"},
                {"title": "Install it", "url": "https://github.com/apps/q/installations/new"},
            ],
        }};
}
`

func newSetupFixture(t *testing.T) *queueFixture {
	t.Helper()
	f := newQueueFixture(t, "", "")
	src, err := os.ReadFile(f.provider)
	require.NoError(t, err)
	src = []byte(strings.Replace(string(src), "export fun describe(", "fun unused(", 1) + setupProvider)
	require.NoError(t, os.WriteFile(f.provider, src, 0o644))
	f.vcs.EXPECT().RemoteURL(mock.Anything, f.root, "origin").Return("https://example.invalid/acme/widgets.git", nil)
	return f
}

// By default describe prints the setup as a script: every fact a comment, every step a
// command or a link. magus runs none of it.
func TestQueueDescribePrintsTheSetupStepsAPersonRuns(t *testing.T) {
	withOutput(t, "")
	f := newSetupFixture(t)
	out, err := f.run(t, "", "describe", "--provider", "local.buzz", "--base", "main", "--app", "q")
	require.NoError(t, err)
	assert.Equal(t, `# local.buzz on main: merge methods squash, merge; stacks merge atomic; a stack queues by the label "queue: <method>"
# main requires 2 approving reviews at the commit a review of a change's head covers
# the queue posts "merge-queue" as 812 (q)
# main requires "merge-queue" from integration 15368 (not seen reported)
# main requires "ci gate" from any source (seen on pull_request)
# allow_auto_merge is false; the queue needs true

# 1. Allow auto-merge
gh api -X PATCH repos/acme/widgets -F allow_auto_merge=true

# 2. Install it
# open https://github.com/apps/q/installations/new
`, string(out))
}

// A provider that needs the app, or its id, says what and where; describe adds the
// command that runs it again with the flags it was given.
func TestQueueDescribeRendersTheRerunAMissingAppNeeds(t *testing.T) {
	withOutput(t, "")
	f := newSetupFixture(t)
	root := shellWord(f.root)
	_, err := f.run(t, "", "describe", "--provider", "local.buzz", "--base", "main", "--app", "hidden")
	var missing *types.MissingAppError
	require.ErrorAs(t, err, &missing)
	assert.EqualError(t, err, "magus queue describe: no id for hidden: https://example.invalid/apps/hidden; then run: magus --root "+root+
		" queue describe --provider local.buzz --base main --app hidden:<id>")
	_, err = f.run(t, "", "describe", "--status-context", "my gate", "--remote", "origin", "--vcs", "git", "--app", "unknown",
		"--base", "main", "--provider", "local.buzz")
	assert.EqualError(t, err, "magus queue describe: no app is named unknown: https://example.invalid/apps; then run: magus --root "+root+
		" queue describe --provider local.buzz --base main --status-context 'my gate' --remote origin --vcs git --app <slug>")
}

// The id rides in --app after the slug and reaches the provider in the same record.
func TestQueueDescribeTakesTheAppsIDInTheAppFlag(t *testing.T) {
	withOutput(t, "")
	f := newSetupFixture(t)
	out, err := f.run(t, "", "describe", "--provider", "local.buzz", "--base", "main", "--app", "hidden:2034567")
	require.NoError(t, err)
	assert.Equal(t, `# local.buzz on main: merge methods squash; stacks merge atomic
# main requires 2 approving reviews at the commit a review of a change's head covers
# the queue posts "merge-queue" as 2034567
# main requires no check
# app hidden is integration 2034567
# nothing left to set up
`, string(out))
}

func TestQueueRefusesAnAppFlagOfAnotherShape(t *testing.T) {
	withOutput(t, "")
	f := newQueueFixture(t, "", "")
	for _, app := range []string{":2034567", "hidden:", ":"} {
		_, err := f.run(t, "", "describe", "--provider", "local.buzz", "--base", "main", "--app", app)
		var misuse errUsage
		require.ErrorAs(t, err, &misuse, app)
		assert.EqualError(t, err, "magus queue describe: --app "+strconv.Quote(app)+" is not <slug> or <slug>:<id>")
		_, err = f.run(t, "", "apply", "--provider", "local.buzz", "--base", "main", "--app", app, "verdicts")
		assert.EqualError(t, err, "magus queue apply: --app "+strconv.Quote(app)+" is not <slug> or <slug>:<id>")
	}
	_, err := f.run(t, "", "describe", "--provider", "local.buzz", "--base", "main", "--app", "q", "--app-id", "2034567")
	assert.ErrorContains(t, err, "flag provided but not defined: -app-id")
}

// -o json carries the setup as the capabilities document's structure.
func TestQueueDescribeJSONCarriesTheSetup(t *testing.T) {
	withOutput(t, "")
	f := newSetupFixture(t)
	out, err := f.run(t, "", "describe", "-o", "json", "--provider", "local.buzz", "--base", "main", "--status-context", "gate")
	require.NoError(t, err)
	var doc struct {
		Schema string      `json:"schema"`
		Setup  types.Setup `json:"setup"`
	}
	require.NoError(t, json.Unmarshal(out, &doc))
	assert.Equal(t, types.SchemaCapabilities, doc.Schema)
	assert.Equal(t, types.Setup{
		StatusContext: "gate",
		Credential:    types.Integration{ID: "812"},
		RequiredChecks: []types.RequiredCheck{
			{Context: "merge-queue", Integration: "15368"},
			{Context: "ci gate", Events: []string{"pull_request"}},
		},
		Settings: []types.Setting{{Name: "allow_auto_merge", Value: "false", Want: "true"}},
		Steps: []types.SetupStep{
			{Title: "Allow auto-merge", Command: "gh api -X PATCH repos/acme/widgets -F allow_auto_merge=true"},
			{Title: "Install it", URL: "https://github.com/apps/q/installations/new"},
		},
	}, doc.Setup)
}

// An empty --status-context asks for no setup: the merge-queue advisor reads the
// capabilities on a pull request token that cannot read checks.
func TestQueueDescribeWithoutAStatusContextReadsNoSetup(t *testing.T) {
	withOutput(t, "")
	f := newSetupFixture(t)
	out, err := f.run(t, "", "describe", "-o", "json", "--status-context", "", "--provider", "local.buzz", "--base", "main")
	require.NoError(t, err)
	assert.Equal(t, `{"schema":"mergequeue.capabilities/v1","base":"main","stack_merge":"atomic","linear_stacks":true,"methods":["squash"],"required_approvals":2}`+"\n", string(out))
}

func TestQueueDescribeRefusesAnOutputItDoesNotRender(t *testing.T) {
	withOutput(t, "yaml")
	f := newQueueFixture(t, "", "")
	_, err := f.run(t, "", "describe", "--provider", "local.buzz", "--base", "main")
	var misuse errUsage
	require.ErrorAs(t, err, &misuse)
	assert.ErrorContains(t, err, "-o yaml is not supported")
}

// The three steps as a workflow drives them, over nothing to merge: every path the
// flags name resolves against the checkout.
func TestQueueStepsResolvePathsAgainstTheCheckout(t *testing.T) {
	f := newQueueFixture(t, "", "")
	f.vcs.EXPECT().FetchRef(mock.Anything, f.root, "origin", "refs/heads/main").Return(queueBase, nil)
	f.vcs.EXPECT().FindCommit(mock.Anything, f.root, queueBase).Return(magustypes.Commit{ID: queueBase, Date: queueDate}, nil)
	f.vcs.EXPECT().Checkouts(mock.Anything, f.root).Return(nil, nil)
	var changes bytes.Buffer
	require.NoError(t, queue.WriteChanges(&changes, types.Changes{Base: "main"}))

	out, err := f.run(t, changes.String(), "plan", "--provider", "local.buzz", "--out", "plan.json", "--facts", "true")
	require.NoError(t, err)
	evs := queueEvents(t, out)
	require.Len(t, evs, 1)
	assert.Equal(t, "no change carries merge intent against main", evs[0].Reason)
	require.FileExists(t, filepath.Join(f.root, "plan.json"))

	_, err = f.run(t, "", "validate", "--plan", "plan.json", "--verdicts", "verdicts", "--gate", "true", "--facts", "true")
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(f.root, "verdicts", queue.PlanFile))
	assert.FileExists(t, filepath.Join(f.root, "verdicts", queue.DoneFile))

	f.vcs.EXPECT().RemoteURL(mock.Anything, f.root, "origin").Return("", nil)
	_, err = f.run(t, "", "apply", "--provider", "local.buzz", "--base", "main", "--facts", "true", "--once", "verdicts")
	require.NoError(t, err)
}

// validated writes a plan of one change and its green verdict into dir, the way
// validate leaves a directory.
func validated(t *testing.T, dir string) {
	t.Helper()
	c := types.Change{ID: "1", Head: queueHead("1"), Base: "main", Method: types.MethodSquash, Affected: []string{"app"}}
	vd := &queue.VerdictDir{Path: dir}
	require.NoError(t, vd.WritePlan(types.Plan{Schema: types.SchemaPlan, Base: "main", BaseCommit: queueBase, CommitDate: queueDate, Depth: 1, Partitions: [][]types.Change{{c}}}))
	require.NoError(t, vd.Record(types.Verdict{BaseCommit: queueBase, Change: c, Decision: types.DecisionMerge, Onto: queueBase,
		CandidateCommit: strings.Repeat("c", 40), Method: types.MethodSquash, Depth: 1}))
	require.NoError(t, vd.MarkDone())
}

func dryRun(t *testing.T) {
	was := globalCfg.DryRun
	globalCfg.DryRun = true
	t.Cleanup(func() { globalCfg.DryRun = was })
}

// A directory is a whole source: apply reads the plan validate wrote into it, and one
// without a plan is an error rather than an empty queue.
func TestQueueApplyReadsThePlanFromADirectoryAndRefusesOneWithout(t *testing.T) {
	f := newQueueFixture(t, "", "")
	f.vcs.EXPECT().Checkouts(mock.Anything, f.root).Return(nil, nil)
	f.vcs.EXPECT().RemoteURL(mock.Anything, f.root, "origin").Return("", nil)
	dryRun(t)
	validated(t, filepath.Join(f.root, "verdicts"))
	out, err := f.run(t, "", "apply", "--provider", "local.buzz", "--base", "main", "--facts", "true", "--once", "verdicts")
	require.NoError(t, err)
	assert.Contains(t, string(out), "dry run: would merge candidate cccccccccccc")

	require.NoError(t, os.Remove(filepath.Join(f.root, "verdicts", queue.PlanFile)))
	for _, mode := range [][]string{{"--once"}, {"--interval", "10ms"}} {
		args := append(append([]string{"apply", "--provider", "local.buzz", "--base", "main", "--facts", "true"}, mode...), "verdicts")
		_, err = f.run(t, "", args...)
		require.ErrorContains(t, err, "magus queue apply: "+filepath.Join(f.root, "verdicts")+" holds no plan.json", "%v", mode)
	}
}

// artifactRun serves each artifact, the zip of a directory, to a request carrying the
// provider's credential, and returns the provider's listing of them as a Buzz list.
func artifactRun(t *testing.T, artifacts map[string]string) string {
	t.Helper()
	zips := map[string][]byte{}
	var rows []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, ok := zips[req.URL.Path]
		if !ok || req.Header.Get("Authorization") != "Bearer tok" {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	queueDownloads = srv.Client()
	t.Cleanup(func() { queueDownloads = nil })
	for name, dir := range artifacts {
		p := "/" + strconv.Itoa(len(zips)) + ".zip"
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		require.NoError(t, zw.AddFS(os.DirFS(dir)))
		require.NoError(t, zw.Close())
		zips[p] = buf.Bytes()
		rows = append(rows, `{"name": "`+name+`", "url": "`+srv.URL+p+`"}`)
	}
	if len(rows) == 0 {
		return ""
	}
	return "[" + strings.Join(rows, ", ") + "]"
}

// The apply workflow's shape: apply reads the plan and the verdicts from a validation
// run's artifacts, as upload-artifact packed validate's output.
func TestQueueApplyFollowsARun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "verdicts")
	validated(t, dir)
	planDir := t.TempDir()
	require.NoError(t, os.Rename(filepath.Join(dir, queue.PlanFile), filepath.Join(planDir, queue.PlanFile)))
	run := artifactRun(t, map[string]string{
		queue.PlanArtifact:                planDir,
		queue.VerdictArtifactPrefix + "1": filepath.Join(dir, "1"),
	})
	f := newQueueFixture(t, "", run)
	f.vcs.EXPECT().Checkouts(mock.Anything, f.root).Return(nil, nil)
	f.vcs.EXPECT().RemoteURL(mock.Anything, f.root, "origin").Return("", nil)
	dryRun(t)
	out, err := f.run(t, "", "apply", "--provider", "local.buzz", "--base", "main", "--workflow", queueWorkflow, "--facts", "true", "--interval", "10ms", "run:acme/widgets/runs/7")
	require.NoError(t, err)
	assert.Contains(t, string(out), "dry run: would merge candidate cccccccccccc")
}

func TestQueueApplyFromARunThatPlannedNothingMergesNothing(t *testing.T) {
	f := newQueueFixture(t, "", artifactRun(t, nil))
	f.vcs.EXPECT().RemoteURL(mock.Anything, f.root, "origin").Return("", nil)
	out, err := f.run(t, "", "apply", "--provider", "local.buzz", "--base", "main", "--workflow", queueWorkflow, "--facts", "true", "--interval", "10ms", "run:acme/widgets/runs/7")
	require.NoError(t, err)
	evs := queueEvents(t, out)
	require.Len(t, evs, 1)
	assert.Equal(t, queue.EventNotice, evs[0].Kind)
	assert.Equal(t, "run acme/widgets/runs/7 completed without a plan; nothing to apply", evs[0].Reason)
}

// A provider that cannot list artifacts is refused before anything is read, rather than
// following a run it can never see complete.
func TestQueueApplyFromARunNeedsAProviderThatListsArtifacts(t *testing.T) {
	f := newQueueFixture(t, "", "")
	src, err := os.ReadFile(f.provider)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.provider, []byte(strings.Split(string(src), "export fun list_artifacts")[0]), 0o644))
	_, err = f.run(t, "", "apply", "--provider", "local.buzz", "--base", "main", "--workflow", queueWorkflow, "run:acme/widgets/runs/7")
	require.ErrorContains(t, err, "does not export list_artifacts, which reading run:acme/widgets/runs/7 needs")
}

// queueWorkflow is the definition a followed run must have run.
const queueWorkflow = ".github/workflows/queue.yaml"

// Apply's base is its own, never the plan's, and a run is followed only against the
// definition it must have run; neither has a default, and a directory, the caller's own,
// takes no definition.
func TestQueueApplyRequiresItsBaseAndARunsDefinition(t *testing.T) {
	f := newQueueFixture(t, "", "")
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no base":                   {[]string{"--provider", "local.buzz", "verdicts"}, "magus queue apply: --base is required"},
		"a run without a workflow":  {[]string{"--provider", "local.buzz", "--base", "main", "run:acme/widgets/runs/7"}, "a run: source needs --workflow"},
		"a directory with workflow": {[]string{"--provider", "local.buzz", "--base", "main", "--workflow", queueWorkflow, "verdicts"}, "--workflow has no effect on a directory source"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.run(t, "", append([]string{"apply"}, tc.args...)...)
			var misuse errUsage
			require.ErrorAs(t, err, &misuse)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

// A run the base's own queue workflow did not make is refused before anything of it is
// downloaded: here a pull request's run of the same file.
func TestQueueApplyRefusesARunAPullRequestStarted(t *testing.T) {
	f := newQueueFixture(t, "", artifactRun(t, nil))
	src, err := os.ReadFile(f.provider)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(f.provider, []byte(strings.Replace(string(src), `"event": "push", "branch_event": true`, `"event": "pull_request", "branch_event": false`, 1)), 0o644))
	f.vcs.EXPECT().RemoteURL(mock.Anything, f.root, "origin").Return("", nil)
	_, err = f.run(t, "", "apply", "--provider", "local.buzz", "--base", "main", "--workflow", queueWorkflow, "--facts", "true", "--once", "run:acme/widgets/runs/7")
	var diag *magustypes.DiagnosticError
	require.ErrorAs(t, err, &diag)
	assert.Equal(t, magustypes.QueueRunUntrusted, diag.Code)
}

// Without --facts, plan asks the magus workspace at the checkout, loaded once in process.
func TestQueuePlanAsksTheMagusWorkspaceByDefault(t *testing.T) {
	changes := `[{"id": "1", "repo": "r", "head": "` + queueHead("1") + `", "base": "main", "method": "squash", "fork": false},
	             {"id": "2", "repo": "r", "head": "` + queueHead("2") + `", "base": "main", "method": "squash", "fork": false}]`
	f := newQueueFixture(t, changes, "")
	for rel, body := range map[string]string{"magusfile.buzz": "", "app/magusfile.buzz": "", "lib/magusfile.buzz": "",
		"app/a.txt": "x\n", "lib/b.txt": "x\n"} {
		require.NoError(t, os.MkdirAll(filepath.Join(f.root, filepath.Dir(rel)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(f.root, rel), []byte(body), 0o644))
	}
	paths := map[string]string{queueHead("1"): "app/a.txt", queueHead("2"): "lib/b.txt"}
	f.vcs.EXPECT().RemoteURL(mock.Anything, f.root, "origin").Return("https://example.invalid/r.git", nil)
	f.vcs.EXPECT().FetchRef(mock.Anything, f.root, "origin", "refs/heads/main").Return(queueBase, nil)
	f.vcs.EXPECT().FindCommit(mock.Anything, f.root, queueBase).Return(magustypes.Commit{ID: queueBase, Date: queueDate}, nil)
	for head, path := range paths {
		f.vcs.EXPECT().FetchCommit(mock.Anything, f.root, "origin", head).Return(nil)
		f.vcs.EXPECT().RangeCommits(mock.Anything, f.root, queueBase, head, []string(nil)).Return([]magustypes.Commit{{ID: head, Parents: []string{queueBase}}}, nil)
		f.vcs.EXPECT().FindCommit(mock.Anything, f.root, head).Return(magustypes.Commit{ID: head, Parents: []string{queueBase}, Date: queueDate}, nil)
		f.vcs.EXPECT().IsAncestor(mock.Anything, f.root, head, queueBase).Return(false, nil)
		f.vcs.EXPECT().RangeFiles(mock.Anything, f.root, queueBase, head, []string(nil)).Return([]string{path}, nil)
		f.vcs.EXPECT().MergeTrees(mock.Anything, f.root, magustypes.TreeMerge{Ours: queueBase, Theirs: head}).Return(magustypes.TreeMergeResult{Tree: "t"}, nil)
	}
	listed, err := f.run(t, "", "ls", "--provider", "local.buzz", "--base", "main")
	require.NoError(t, err)
	out, err := f.run(t, string(listed), "plan", "--provider", "local.buzz", "--out", "plan.json")
	require.NoError(t, err)
	var parts [][]string
	for _, e := range queueEvents(t, out) {
		if e.Kind == queue.EventPartition {
			parts = append(parts, e.Changes)
		}
	}
	assert.Equal(t, [][]string{{"1"}, {"2"}}, parts, "app and lib are disjoint projects")
	pl, err := queue.ReadPlanFile(filepath.Join(f.root, "plan.json"))
	require.NoError(t, err)
	assert.Equal(t, []string{"app"}, pl.Partitions[0][0].Affected)
}

func TestParseCommitter(t *testing.T) {
	got, err := parseCommitter("Merge Bot <bot@example.invalid>")
	require.NoError(t, err)
	assert.Equal(t, magustypes.Person{Name: "Merge Bot", Email: "bot@example.invalid"}, got)
	got, err = parseCommitter("")
	require.NoError(t, err)
	assert.Zero(t, got)
	for _, bad := range []string{"bot@example.invalid", "<bot@example.invalid>", "Bot <>", "Bot <a b>", "Bot <x"} {
		_, err := parseCommitter(bad)
		require.Error(t, err, bad)
	}
}
