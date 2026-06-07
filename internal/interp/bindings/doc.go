// Package bindings registers the Go-backed modules (magus, std: os, platform,
// fs, vcs, env, crypto, json, log, http, archive) available to every magusfile script.
// Blank-import this package so its init() fires before any magusfile runs.
//
// Layout:
//   - buzz.go        registers the magus.* / extra.* host API on a Buzz session.
//   - marshal.go     the Buzz↔Go value boundary: argStr/argStrSlice/argStrMap
//     pull typed Go values out of []buzzeng.Value arguments; strSliceToBuzzList,
//     execRecordToBuzz, and friends (in buzz.go) go the other way.
//   - spell.go, remote_spell.go, fork.go  spell loading, dispatch, and forking.
//
// Every binding is a buzzeng.DirectValue closure of the shape
// func(ctx, []buzzeng.Value) (buzzeng.Value, error); the marshal.go helpers keep
// the argument-decoding at those call sites uniform rather than hand-rolled.
package bindings
