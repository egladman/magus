package spells

import (
	"embed"
	"io/fs"
)

// shipped holds every spell directory the binary carries, whole: go:embed of a
// directory is recursive, so a file beside spell.buzz (golang/gomod.buzz) ships with it.
// go:embed reads the disk while a release packs only what the VCS tracks, so
// TestShippedMatchesTrackedFiles holds the two to the same file set.
//
//go:embed bash buf buzz cosign docker endoflife-date golang markdown onepassword podman python rust system-keychain typescript
var shipped embed.FS

// Shipped returns the source of every spell magus carries, one directory per spell
// named as in the repository (golang, not go), each holding a spell.buzz. It is Buzz
// source, not bytecode: internal/spell compiles the ones that are built-ins at load, and
// `magus spell pull magus/spell/<name>` copies any of them out.
func Shipped() fs.FS { return shipped }
