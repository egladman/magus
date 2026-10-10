package guard

import (
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clientScript is the client tool input running script.
func clientScript(script string) map[string]any { return map[string]any{"script": script} }

// TestClientPutRendersEveryMergedField holds the guard's view of a put to the job
// store's own. A field job.ParseMerge applies and the rendering drops reaches the row with
// no rule having read it, and the rebind rule then clears a rewrite of it as a plain shrink.
//
// The accepted set is PROBED rather than restated: job.ParseMerge exports no key list, and a
// second hand-written one is forgotten in the same direction as the first.
func TestClientPutRendersEveryMergedField(t *testing.T) {
	merged := 0
	for _, field := range jobJSONFields() {
		if !jobMergeApplies(field) {
			continue
		}
		merged++
		line := buildCall(hint.ToolClient.String(), clientScript(`import "magus"; magus\job.put("a/b", opts: {"`+field+`": "x"});`), "")
		assert.Contains(t, mcpParams(strings.Fields(line)), field,
			"job.ParseMerge applies %q, so a put carrying it has to be judged", field)
	}
	require.NotZero(t, merged, "the probe found no merged field at all, so it is measuring nothing")
}

// jobJSONFields are the row's wire names, which is the vocabulary both job doors speak.
func jobJSONFields() []string {
	t := reflect.TypeFor[types.Job]()
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

// jobMergeApplies reports whether a put naming key changes the row. Several values are
// tried because the merge is typed: a list, a boolean, a string that is also a valid
// state, and a rendered run line cover every shape it accepts, and a key it ignores leaves
// the row untouched under all four.
func jobMergeApplies(key string) bool {
	// The last value is a well-formed goal. Without it the probe reports goals as
	// unmerged, because none of the scalar shapes decodes into one.
	for _, value := range []any{
		"declared", "magus run test .", []any{"x"}, true,
		[]any{map[string]any{"id": "g", "check": map[string]any{"target": "test", "project": "."}}},
	} {
		apply, err := job.ParseMerge(map[string]any{key: value})
		if err != nil {
			continue
		}
		var row types.Job
		apply(&row)
		if !reflect.DeepEqual(row, types.Job{}) {
			return true
		}
	}
	return false
}

// TestMagusToolCallMatchesOnlyMagusTools: magus's tool names are plain words, so only a
// name addressed to the magus server is a magus call. A bare `status` is as likely
// another server's tool, and a suffix match let any server's tool decode as magus's.
func TestMagusToolCallMatchesOnlyMagusTools(t *testing.T) {
	assert.Equal(t, "client", magusToolCall("mcp__magus__client"))
	assert.Equal(t, "status", magusToolCall("mcp__magus__status"))

	for _, name := range []string{
		"status",
		"client",
		"mcp__other__status",
		"mcp__other__client",
		"mcp__mcp__magus__client",
		"filesystem__write_file",
		"mcp__magus__magus_job",
		"mcp__magus__nonexistent",
		"",
	} {
		assert.Empty(t, magusToolCall(name), "%q is not a call to magus", name)
	}

	req, ok := decodeHookEnvelope(`{"tool_name":"mcp__other__client","tool_input":{"script":"import \"magus\"; magus\\run([\"ci\"]);"}}`)
	require.True(t, ok)
	assert.NotContains(t, req.Value, "magus run ci", "another server's client tool is not graded as magus's")
}

// TestBuildCallCoversTools is why the decode arm keys on the tool name. Requiring
// `op` left the tools that do not carry one reaching no rule at all.
func TestBuildCallCoversTools(t *testing.T) {
	for _, tool := range hint.AllToolNames {
		line := buildCall(tool.String(), map[string]any{"op": "list"}, "")
		require.NotEmpty(t, line, "%s builds nothing", tool)

		cmds, ok := ParseCommands(line)
		require.True(t, ok, "%s builds %q, which does not parse", tool, line)
		require.Len(t, cmds, 1, "%s builds %q, which is not one command", tool, line)

		if _, hasCLI := mcpCLIEquivalents[tool]; hasCLI {
			assert.Equal(t, "magus", path.Base(cmds[0].Name),
				"%s has a CLI equivalent, so it must build that argv: %q", tool, line)
			continue
		}
		assert.Equal(t, tool.String(), cmds[0].Name,
			"%s has no CLI equivalent, so it builds its own name and is recorded rather than judged", tool)
	}
}

// TestBuildCall pins the lines a rule keys on. The script is parsed, so a report about
// ci is not a run of it, and what the parse cannot settle reads as the refusing answer.
func TestBuildCall(t *testing.T) {
	const imp = `import "magus"; `
	unread := "magus run ci\nclient op=unread"
	for name, tc := range map[string]struct {
		input map[string]any
		want  string
	}{
		"an affected run":              {clientScript(imp + `return magus\cmd("affected", ["ci"]);`), "magus affected ci"},
		"a named run":                  {clientScript(imp + `magus\run(["ci"]);`), "magus run ci"},
		"a charmed gate":               {clientScript(imp + `magus\run(["ci:rw", "."]);`), "magus run ci:rw ."},
		"an uppercase gate":            {clientScript(imp + `magus\run(["CI"]);`), "magus run CI"},
		"a shard plan":                 {clientScript(imp + `magus\cmd("affected", ["ci", "--plan"]);`), "client"},
		"a plan flag in comment":       {clientScript(imp + "// --plan\nmagus\\run([\"ci\"]);"), "magus run ci"},
		"a computed argv":              {clientScript(imp + `final a = ["c" + "i"]; magus\run(a);`), "magus run ci"},
		"a computed target":            {clientScript(imp + `magus\run(["c" + "i"]);`), "magus run ci"},
		"a stored member":              {clientScript(imp + `final r = magus\run; r(["ci"]);`), unread},
		"an aliased import":            {clientScript(`import "magus" as m; m\run(["ci"]);`), "magus run ci"},
		"a flat import":                {clientScript(`import "magus" as _; run(["ci"]);`), "magus run ci"},
		"another tool's ci":            {clientScript(imp + `import "proc"; proc\run(["echo", "ci"]);`), "client"},
		"an unrelated run":             {clientScript(imp + `magus\run(["test", "."]);`), "client"},
		"no script":                    {map[string]any{"op": "list"}, "client"},
		"a script with no calls":       {clientScript("fun main(args: [str]) > int { return 1; }"), "client"},
		"a script that does not parse": {clientScript("fun ("), unread},
		"a path nobody can read":       {map[string]any{"path": filepath.Join(t.TempDir(), "gone.buzz")}, unread},
		"a job put": {
			clientScript(imp + `magus\job.put("a/b", opts: {"parent": "a", "write_paths": ["x/**", "y/**"], "read_only": false});`),
			"client op=put id=a/b parent=a read_only=false write_paths=x/**,y/**",
		},
		"an anonymous object": {
			clientScript(imp + `magus\job.put("a/b", .{ parent = "a", read_only = true });`),
			"client op=put id=a/b parent=a read_only=true",
		},
		"a backslash put":   {clientScript(imp + `magus\job\put("a/b", opts: {"parent": "a"});`), "client op=put id=a/b parent=a"},
		"a flat put":        {clientScript(`import "magus" as _; job.put("a/b", opts: {"parent": "a"});`), "client op=put id=a/b parent=a"},
		"an elided value":   {clientScript(imp + `magus\job.put("a/b", opts: {"criteria": "ship the thing"});`), "client op=put id=a/b criteria=..."},
		"a computed id":     {clientScript(imp + `final id = "a/b"; magus\job.put(id);`), "client op=put id=%unread%"},
		"a computed opts":   {clientScript(imp + `final o = {"parent": "a"}; magus\job.put("a/b", opts: o);`), "client op=put id=a/b opts=%unread%"},
		"a computed value":  {clientScript(imp + `final p = "a"; magus\job.put("a/b", opts: {"parent": p});`), "client op=put id=a/b parent=%unread%"},
		"a register":        {clientScript(imp + `magus\job.register("a/b", reported_base: "abc");`), "client op=register id=a/b"},
		"a clear":           {clientScript(imp + `magus\job.clear();`), "client op=clear"},
		"a wait":            {clientScript(imp + `magus\job.wait("a/b");`), "magus job wait a/b"},
		"a nested job fork": {clientScript(imp + `magus\cmd("job", ["fork", "a/b", "--write-paths", "x/**"]);`), "magus job fork a/b --write-paths x/**"},
		"a job read":        {clientScript(imp + `magus\job.list();`), "client"},
		"two writes": {
			clientScript(imp + `magus\job.clear(); magus\run(["ci"]);`),
			"client op=clear\nmagus run ci",
		},
	} {
		assert.Equal(t, tc.want, buildCall(hint.ToolClient.String(), tc.input, ""), name)
	}

	for name, tc := range map[string]struct {
		tool  hint.ToolName
		input map[string]any
		want  string
	}{
		"a buzz script": {hint.ToolBuzz, map[string]any{"path": "a b.buzz"}, `magus buzz "a b.buzz"`},
		"a status":      {hint.ToolStatus, nil, "magus status"},
	} {
		assert.Equal(t, tc.want, buildCall(tc.tool.String(), tc.input, ""), name)
	}
}

// A client script named by path is read and judged like an inline one.
func TestBuildCallReadsAClientScriptByPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gate.buzz"), []byte(`import "magus"; magus\run(["ci"]);`), 0o644))
	assert.Equal(t, "magus run ci", clientCall(map[string]any{"path": "gate.buzz"}, dir))
}

// Dropping enter from the rendering turns an entry beneath the holder's own job into a
// bare put of another row, which the rebind rule denies.
func TestClientEntryReachesTheRebindRule(t *testing.T) {
	me := narrowLease().ID
	child := types.Job{ID: me + "/child", Parent: me, WritePaths: []string{"cmd/magus/x/**"}, State: types.StateDeclared}
	ctx, _ := fleetFixture(t, narrowLease(), child)

	entry := buildCall(hint.ToolClient.String(), clientScript(enterCall(child.ID, "cmd/magus/x/a.go")), "")
	assert.Equal(t, "client op=put id="+child.ID+" enter=cmd/magus/x/a.go", entry)
	assert.Empty(t, denyLeaseScopedRebind(ctx, Dependencies{}, me, entry))

	stray := buildCall(hint.ToolClient.String(), clientScript(enterCall("harness/other", "a.go")), "")
	assert.Contains(t, denyLeaseScopedRebind(ctx, Dependencies{}, me, stray), "enter a job not forked beneath")
}

// A holder's client script that writes another job reaches the rebind rule, through the
// job member and through a nested `magus job` command alike.
func TestClientJobWritesReachTheRebindRule(t *testing.T) {
	ctx, _ := fleetFixture(t, narrowLease())
	me := narrowLease().ID
	for script, what := range map[string]string{
		`import "magus"; magus\job.put("harness/other", opts: {"write_paths": ["**"]});`:  "write another job",
		`import "magus"; magus\job.put("` + me + `", opts: {"write_paths": ["**"]});`:     "rewrite the job it holds",
		`import "magus"; magus\job.clear();`:                                              "drop every job",
		`import "magus"; magus\job.register("harness/other", reported_base: "abc");`:      "write another job",
		`import "magus"; magus\cmd("job", ["exec", "harness/other"]);`:                    "take the lease on another job",
		`import "magus"; final j = magus\job; j.clear();`:                                 "cannot read",
		`import "magus" as m; m\job.put("harness/other", opts: {"write_paths": ["**"]});`: "write another job",
		`import "magus"; magus\job\put("harness/other", opts: {"write_paths": ["**"]});`:  "write another job",
		`import "magus" as _; job.put("harness/other", opts: {"write_paths": ["**"]});`:   "write another job",
	} {
		line := buildCall(hint.ToolClient.String(), clientScript(script), "")
		assert.Contains(t, denyLeaseScopedRebind(ctx, Dependencies{}, me, line), what, "%q renders %q", script, line)
	}
}

// TestDiffToolReadsOnlyForStateAndThread pins which diff tool ops a binary that cannot load the
// tree still lets through: the two that only read, and none that writes into the session.
func TestDiffToolReadsOnlyForStateAndThread(t *testing.T) {
	name := hint.ToolDiff.String()
	for op, want := range map[string]bool{
		"": true, "state": true, "thread": true, " thread ": true,
		"comment": false, "suggest": false, "resolve": false, "outline": false,
	} {
		in := map[string]any{}
		if op != "" {
			in["op"] = op
		}
		assert.Equal(t, want, mcpReadsOnly(name, in), "op %q", op)
	}
}
