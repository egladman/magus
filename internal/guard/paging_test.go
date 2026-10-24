package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pagedFile writes a Go file of six twenty-line functions, 1-120 with the package clause.
func pagedFile(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("package paged\n")
	for i := range 6 {
		fmt.Fprintf(&b, "\nfunc F%d() {\n", i)
		for range 17 {
			b.WriteString("\t_ = 0\n")
		}
		b.WriteString("}\n")
	}
	path := filepath.Join(root, "paged.go")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
	return path
}

func TestReadPagingRefusesTheThirdWindow(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := hookLocation(ctx, Dependencies{}).workspace
	pagedFile(t, root)
	deps := withSetting(string(denyRuleReadPaging), builtin.Deny)
	read := func(agent, line string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: line, Session: "paging", Agent: agent, Host: "claude-code", Dir: root})
	}

	assert.NotEqual(t, "deny", read("a1", "sed -n 1,40p paged.go").Decision)
	assert.NotEqual(t, "deny", read("a1", "sed -n 1,40p paged.go").Decision, "a window read again does not count")
	assert.NotEqual(t, "deny", read("a1", "sed -n 3,20p paged.go").Decision, "a window inside one function is targeted")
	assert.NotEqual(t, "deny", read("a1", "sed -n 41,80p paged.go").Decision)
	v := read("a1", "sed -n 81,121p paged.go")
	require.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleReadPaging)}, judgedAs(v))
	assert.Contains(t, v.Reason, "paged.go")
	assert.NotEqual(t, "deny", read("a2", "sed -n 81,121p paged.go").Decision, "each caller's windows are its own")
}

func TestReadPagingSuggestedSpanIsNotAWindow(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := hookLocation(ctx, Dependencies{}).workspace
	pagedFile(t, root)
	deps := withSetting(string(denyRuleReadPaging), builtin.Deny)
	read := func(line string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: line, Session: "suggested", Agent: "a1", Host: "claude-code", Dir: root})
	}
	read("sed -n 1,40p paged.go")
	read("sed -n 41,80p paged.go")
	v := read("sed -n 81,121p paged.go")
	require.Equal(t, "deny", v.Decision)

	span := regexp.MustCompile(`sed -n (\d+),(\d+)p paged\.go`).FindStringSubmatch(v.Reason)
	require.NotNil(t, span, "the refusal names a span to read: %s", v.Reason)
	first, _ := strconv.Atoi(span[1])
	last, _ := strconv.Atoi(span[2])

	assert.NotEqual(t, "deny", read(fmt.Sprintf("sed -n %s,%sp paged.go", span[1], span[2])).Decision, "the span it names reads")
	assert.NotEqual(t, "deny", read(fmt.Sprintf("sed -n %d,%dp paged.go", first-1, last+1)).Decision,
		"so does the span the Read tool maps it onto, one line wider at each end")
}

func TestFileMapCoveringNearAllowsOneLineOfSlack(t *testing.T) {
	m := fileMap{entries: []mapEntry{{name: "A", first: 10, last: 20}, {name: "B", first: 21, last: 40}}}

	_, exact := m.covering(9, 21)
	assert.False(t, exact, "the strict test refuses a span one line past the entry")
	e, near := m.coveringNear(9, 21)
	assert.True(t, near)
	assert.Equal(t, "A", e.name)
	_, wide := m.coveringNear(8, 20)
	assert.False(t, wide, "two lines past is paging")

	assert.Equal(t, "A", m.suggest(15).name, "the entry holding the line")
	assert.Equal(t, "B", m.suggest(100).name, "past the end, the last entry")
}

func TestReadNavigationRefusesACallerOncePerFile(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := hookLocation(ctx, Dependencies{}).workspace
	pagedFile(t, root)
	deps := withSetting(string(denyRuleReadNavigation), builtin.Deny)
	read := func(agent string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: "cat paged.go", Session: "navigation", Agent: agent, Host: "claude-code", Dir: root})
	}
	first := read("a1")
	require.Equal(t, types.GuardJudgment{Decision: "deny", Rule: string(denyRuleReadNavigation)}, judgedAs(first))
	assert.NotEqual(t, "deny", read("a1").Decision, "asked again with the map in hand, the whole read passes")
	assert.Equal(t, "deny", read("a2").Decision, "another caller has not seen the map")
}
