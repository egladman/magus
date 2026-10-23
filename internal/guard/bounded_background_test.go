package guard

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
)

// bgCall is bgSearchCall and its result with command in place of the search.
func bgCall(command string) []string {
	const search = `cd ~/.claude/projects; grep -rhoE 'denied' --include='*.jsonl' . | sort | uniq -c`
	return []string{strings.Replace(bgSearchCall, search, command, 1), bgSearchResult}
}

// A subagent's stop is not refused over the bounded run check-timeout sent it to background,
// nor over its own commit the host moved to the background: each ends by itself, and its
// notice is how the agent hears the result. Any other live task still holds the stop.
func TestJudgeStopLetsWorkThatEndsOnItsOwnRun(t *testing.T) {
	started := bgTime(t, "2026-10-13T11:37:41.120Z")
	stop := func(lines ...string) bool {
		_, refused := judgeStop(Dependencies{}, hint.Gate{}, subagentStop(t, "a05d44b006f8f68af", writeTranscript(t, lines...)), started.Add(time.Minute))
		return refused
	}
	for _, command := range []string{
		"cd /repo && ./magus job exec --timeout 11m -- ./magus run go::go-test . -- -run TestLedger",
		"git commit -q -m 'add the ledger'",
		"git -C /repo merge --no-edit feature",
	} {
		assert.False(t, stop(bgCall(command)...), command)
	}
	for _, command := range []string{
		"./magus job exec -- ./magus run go::go-test .",
		"./magus run go::go-test .",
		"git commit -m x; sleep 600",
		"while true; do git commit -m x; done",
	} {
		assert.True(t, stop(bgCall(command)...), command)
	}
}

// check-timeout refuses once per target per session, as it advises: the first run of the
// target is refused naming the timeout, and the rerun narrowed to one test goes through.
func TestCheckTimeoutRefusesOncePerTarget(t *testing.T) {
	row := figuresLease()
	ctx, root := fleetFixture(t, row)
	recordRun(t, root, "docs", "diagrams-generate", 5*time.Minute)
	req := Request{Input: "./magus run diagrams_generate docs", Lease: row.ID, Host: "h", Session: "s1", Agent: "a1b2c3",
		HostForegroundDefault: 2 * time.Minute, HostForegroundMax: 10 * time.Minute}

	first := Judge(ctx, Dependencies{}, req)
	require.Equal(t, verdictWithRule("deny", string(advisoryCheckTimeout)), unworded(first))

	req.Input = "./magus run diagrams_generate docs -- -run TestFlow"
	narrowed := Judge(ctx, Dependencies{}, req)
	assert.NotEqual(t, string(advisoryCheckTimeout), narrowed.Rule, narrowed.Reason)

	other := req
	other.Session = "s2"
	assert.Equal(t, string(advisoryCheckTimeout), Judge(ctx, Dependencies{}, other).Rule, "another session is told again")
}

// A run naming no project and one naming the root are one target to check-timeout.
func TestCheckTimeoutKeyReadsNoProjectAsTheRoot(t *testing.T) {
	assert.Equal(t, checkTimeoutKey(targetRun{verb: "run", target: "test", projects: []string{"."}}), checkTimeoutKey(targetRun{verb: "run", target: "test"}))
}
