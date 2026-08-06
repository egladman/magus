package types

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathResolveUsesItsOwnBase(t *testing.T) {
	p := Path{Value: "src/main.go", Base: "/repo"}
	resolved := p.Resolve()
	assert.Equal(t, filepath.Join("/repo", "src/main.go"), resolved.Value)
	assert.False(t, resolved.IsDir)
	// An absolute path is measured from nothing, so it keeps no base to be
	// re-resolved against later - Resolve is idempotent.
	assert.Empty(t, resolved.Base, "an absolute result carries no base")
	assert.Equal(t, resolved, resolved.Resolve(), "resolving twice changes nothing")

	rel, err := resolved.RelativeTo("/repo")
	require.NoError(t, err)
	assert.Equal(t, "src/main.go", rel.Value)
	assert.Equal(t, "/repo", rel.Base, "a relative result names what it is relative to")
}

func TestPathResolveEmptyPreservesOptionalPath(t *testing.T) {
	// An optional path left unset must not silently become its base directory.
	assert.Equal(t, Path{IsDir: true, Base: "/repo"}, (Path{IsDir: true, Base: "/repo"}).Resolve())
}

func TestPathAbsoluteValueIgnoresBase(t *testing.T) {
	p := Path{Value: "/etc/hosts", Base: "/repo"}
	assert.Equal(t, "/etc/hosts", p.Resolve().Value, "an absolute value is not joined onto a base")
}

// The regression this whole field exists for: the same Value means two different files
// depending on what produced it. A VCS status path is repo-root-relative; a target's
// footprint entry is project-relative, and a target's cwd is its project directory. As
// bare strings these are indistinguishable, and picking the wrong base fails by silently
// naming a file that does not exist.
func TestPathSameValueDifferentBaseResolvesDifferently(t *testing.T) {
	fromVCS := Path{Value: "docs/conventions.md", Base: "/repo"}
	fromProject := Path{Value: "docs/conventions.md", Base: "/repo/subproject"}

	assert.Equal(t, filepath.Join("/repo", "docs/conventions.md"), fromVCS.Resolve().Value)
	assert.Equal(t, filepath.Join("/repo/subproject", "docs/conventions.md"), fromProject.Resolve().Value)
	assert.NotEqual(t, fromVCS.Resolve().Value, fromProject.Resolve().Value,
		"identical values, different bases, different files - which is the point")
}

func TestPathRebaseReinterprets(t *testing.T) {
	p := Path{Value: "a.txt", Base: "/one"}
	assert.Equal(t, filepath.Join("/two", "a.txt"), p.Rebase("/two").Resolve().Value)
	assert.Equal(t, "/one", p.Base, "Rebase returns a copy and leaves the receiver alone")
}

func TestPathRelativeToCrossesBases(t *testing.T) {
	// Expressed from /repo even though it was measured from /repo/docs.
	p := Path{Value: "conventions.md", Base: "/repo/docs"}
	rel, err := p.RelativeTo("/repo")
	require.NoError(t, err)
	assert.Equal(t, "docs/conventions.md", rel.Value)
	assert.Equal(t, "/repo", rel.Base)
}
