// Package parsecache holds the one Buzz parse cache a magus process lexes through.
package parsecache

import buzz "github.com/egladman/magus/libs/gopherbuzz"

// shared is sized for a process that loads a whole workspace: every .buzz file in
// this repo lexed to about 18 MB on 2026-09-23, and the server may hold several
// revisions of each alongside the host declaration sources.
var shared = buzz.NewParseCache(64 << 20)

// Shared returns the cache every Buzz session magus creates and every parse it makes
// go through, so a module that many magusfiles and spells import is lexed once per
// process. It is safe for concurrent use.
func Shared() *buzz.ParseCache { return shared }
