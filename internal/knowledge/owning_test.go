package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func TestOwningProjectPath(t *testing.T) {
	projects := []types.TargetGraphProject{
		{Path: "."},
		{Path: "docs"},
		{Path: "foo"},
		{Path: "foo/bar"},
	}

	cases := []struct {
		file string
		want string
	}{
		{"docs/render.buzz", "docs"}, // nested project wins over root
		{"magusfile.buzz", "."},            // root-level file falls to the root project
		{"foo/bar/x.buzz", "foo/bar"},      // longest matching project wins
		{"foo/x.buzz", "foo"},              // not deep enough for foo/bar
		{"foobar/x.buzz", "."},             // "foo" must not claim "foobar" (path-prefix guard)
	}
	for _, c := range cases {
		got, ok := owningProjectPath(c.file, projects)
		assert.True(t, ok, "file %q should be owned", c.file)
		assert.Equal(t, c.want, got, "owner of %q", c.file)
	}
}

func TestOwningProjectPathNoRootUnowned(t *testing.T) {
	// With no root project, a top-level file belongs to nobody rather than being
	// force-fit into an unrelated nested project.
	_, ok := owningProjectPath("top.buzz", []types.TargetGraphProject{{Path: "docs"}})
	assert.False(t, ok)
}
