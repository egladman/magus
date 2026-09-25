package types

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var base = strings.Repeat("b", 40)

func head(id string) string { return fmt.Sprintf("%040x", id) }

func change(id string) Change {
	return Change{ID: id, Head: head(id), Base: "main", Method: MethodSquash}
}

// An id names a directory the verdicts are written to, so ".." would make the
// directory's parent the target of a RemoveAll.
func TestCheckIDRefusesIDsThatEscapeTheirDirectory(t *testing.T) {
	for _, id := range []string{"..", ".", "a/b", `a\b`, ".hidden", "-x", "", strings.Repeat("1", 129)} {
		require.ErrorContains(t, CheckID(id), "change id", "%q", id)
	}
	for _, id := range []string{"1", "pr-482", "a.b_c", strings.Repeat("1", 128)} {
		require.NoError(t, CheckID(id), id)
	}
}

// Every field reaches a version control command line, so what could read as an option
// or a refspec is refused.
func TestChangeCheckRefusesWhatGitCouldReadAsAnOption(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*Change)
		want string
	}{
		"short head":      {func(c *Change) { c.Head = "abc" }, `head "abc" is not a full commit id`},
		"upper-case head": {func(c *Change) { c.Head = strings.Repeat("A", 40) }, "is not a full commit id"},
		"stack base":      {func(c *Change) { c.StackBase = "-x" }, `stack base "-x" is not a full commit id`},
		"no method":       {func(c *Change) { c.Method = "" }, `merge method "", want merge, squash or rebase`},
		"ref outside":     {func(c *Change) { c.Ref = "heads/x" }, "does not start with refs/"},
		"ref option":      {func(c *Change) { c.Ref = "refs/--upload-pack=x y" }, "ref:"},
		"no base":         {func(c *Change) { c.Base = "" }, "base: empty branch name"},
		"branch option":   {func(c *Change) { c.Branch = "-f" }, "branch:"},
		"parent":          {func(c *Change) { c.Parent = "../x" }, "parent:"},
		"below":           {func(c *Change) { c.Below = "-1" }, "below:"},
	} {
		c := change("1")
		tc.edit(&c)
		require.ErrorContains(t, c.Check(), tc.want, name)
	}
	require.NoError(t, change("1").Check())
}

// The grammar is git's own; before, a leading dot, a ".lock" component in the middle and
// "HEAD" passed.
func TestCheckBranchFollowsGitsRules(t *testing.T) {
	for _, ok := range []string{"main", "feat/x", "release-1.2", "a.b/c", "HEADS"} {
		assert.NoError(t, checkBranch(ok), ok)
	}
	for _, bad := range []string{"", "-x", "a..b", "a:b", "a b", "a/", "/a", "a//b", "x.lock", "a.lock/b", "refs/heads/main",
		"a/.b", ".a", "@", "a@{b", "a.", "a~b", "a^b", "a?b", "a*b", "a[b", `a\b`, "a\x7fb", "HEAD"} {
		assert.Error(t, checkBranch(bad), bad)
	}
}

func TestIsObjectIDAcceptsFullLowerCaseSHA1AndSHA256(t *testing.T) {
	assert.True(t, IsObjectID(strings.Repeat("a", 40)))
	assert.True(t, IsObjectID(strings.Repeat("0", 64)))
	for _, bad := range []string{"", strings.Repeat("a", 39), strings.Repeat("A", 40), strings.Repeat("g", 40), strings.Repeat("a", 41)} {
		assert.False(t, IsObjectID(bad), bad)
	}
}
