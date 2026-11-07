package types

import (
	"slices"
	"strings"
)

// ShareKind is one kind of rule a used workspace lends its consumer.
// The set is what magus implements. A kind outside it is a load error,
// never accepted and ignored.
type ShareKind string

// The kinds a `use` entry may share.
const (
	// ShareLint is the used workspace's lint config and custom linters.
	ShareLint ShareKind = "lint"
)

// Values lists every kind magus implements.
// An entry naming no share list takes all of them.
func (ShareKind) Values() []string { return []string{string(ShareLint)} }

// Valid reports whether k is one of [ShareKind.Values].
func (k ShareKind) Valid() bool { return slices.Contains(k.Values(), string(k)) }

// UsedWorkspace is one magus.yaml `use` entry resolved at load.
// Root is absolute and holds a workspace. Share is never empty.
type UsedWorkspace struct {
	Name  string      `json:"name"  yaml:"name"`
	Root  string      `json:"root"  yaml:"root"`
	Share []ShareKind `json:"share" yaml:"share"`
}

// UseLinkDir, under a workspace root, links each used root by its use
// reference. The module search takes it as a root, so an import of a
// reference resolves. It is this checkout's state, never committed.
const UseLinkDir = ".magus/use"

// useRefPrefix opens a path into a used workspace. Only [UseRef] and
// [CutUseRef] spell it, so a rename touches those two.
const useRefPrefix = "@"

// UseRef spells rel, a slash path under the used workspace name's root,
// as a read pattern, an import or a cache key line carries it.
// An empty rel names the root itself.
func UseRef(name, rel string) string {
	if rel == "" {
		return useRefPrefix + name
	}
	return useRefPrefix + name + "/" + rel
}

// CutUseRef splits a reference [UseRef] wrote into the name and the path
// under its root. ok is false for any other path, and for a bare
// reference naming no path under the root.
func CutUseRef(ref string) (name, rel string, ok bool) {
	rest, found := strings.CutPrefix(ref, useRefPrefix)
	if !found {
		return "", "", false
	}
	name, rel, found = strings.Cut(rest, "/")
	if !found || name == "" || rel == "" {
		return "", "", false
	}
	return name, rel, true
}
