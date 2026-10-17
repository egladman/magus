package guard

import (
	"os"
	"path/filepath"
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
		b.WriteString("\nfunc F" + strconv.Itoa(i) + "() {\n")
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
