//go:build !wasm

package registry

import gen "github.com/egladman/magus/host/gen"

// This file is hand-maintained (not generated). TestModulesMatchStd guards it
// against drift from std.All(). The RegisterFunc / ModuleReg types live in
// types.go (no build tag) so the wasm build can use them too; the
// WASMCompatible:true entries here are mirrored in registry_wasm.go, which the
// wasm build sees instead of this file.

// Modules is the single source of truth for magus's host modules: every bare
// import name a Buzz session resolves beyond Buzz's own stdlib, mapped to its
// generated Register trampoline. Each surface derives its set from this one table
// instead of a hand-kept list: the magusfile engine and `magus buzz` install all
// of them; the browser playground installs the WASMCompatible subset.
//
// The `magus` namespace is intentionally absent. It is not a bare import; it is
// wired onto the magus.* namespace with a magusfile's target context.
var Modules = map[string]ModuleReg{
	// Context-dependent (process / filesystem / network): full surface only. The
	// browser has no way to provide these, so they are never in the playground.
	"os":      {gen.RegisterOs, false},
	"fs":      {gen.RegisterFs, false},
	"vcs":     {gen.RegisterVcs, false},
	"archive": {gen.RegisterArchive, false},
	"http":    {gen.RegisterHttp, false},

	// Pure compute: safe everywhere, including the WASM playground. Mirror this
	// exact set in registry_wasm.go, which the wasm build uses in place of this file.
	// uuid is here (its randomness uses the browser's getRandomValues) rather than
	// treated as context-dependent - a deliberate choice to let it run in-browser.
	"platform": {gen.RegisterPlatform, true},
	"crypto":   {gen.RegisterCrypto, true},
	"env":      {gen.RegisterEnv, true},
	"json":     {gen.RegisterJson, true},
	"xml":      {gen.RegisterXml, true},
	"time":     {gen.RegisterTime, true},
	"fmt":      {gen.RegisterFmt, true},
	"markdown": {gen.RegisterMarkdown, true},
	"charm":    {gen.RegisterCharm, true},
	"encoding": {gen.RegisterEncoding, true},
	"path":     {gen.RegisterPath, true},
	"strings":  {gen.RegisterStrings, true},
	"semver":   {gen.RegisterSemver, true},
	"yaml":     {gen.RegisterYaml, true},
	"template": {gen.RegisterTemplate, true},
	"toml":     {gen.RegisterToml, true},
	"uuid":     {gen.RegisterUuid, true},
}
