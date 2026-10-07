package spells

import (
	"embed"
	"io/fs"
)

// shipped holds every spell directory the binary carries, whole: go:embed of a
// directory is recursive, so a file beside spell.buzz (golang/gomod.buzz) ships with it.
// The embed reads the disk while a release packs only what the VCS tracks, so
// TestShippedMatchesTrackedFiles holds the two to the same file set. experimental/ stays
// out: nothing there ships.
//
//go:embed aws bash buf buzz cosign docker endoflife-date github gitlab golang harness markdown onepassword podman python rust system-keychain typescript vale
var shipped embed.FS

// Shipped returns the source of every spell magus carries, each a directory holding a
// spell.buzz, named as in the repository (golang, not go; harness/cursor). A top-level
// directory is a built-in candidate; a nested one (harness/cursor, github/actions) is a
// provider a workspace imports by path, and ships as source only. It is Buzz source, not
// bytecode: internal/spell compiles the built-ins at load, and
// `magus spell pull magus/spell/<name>` copies any of them out.
func Shipped() fs.FS { return shipped }
