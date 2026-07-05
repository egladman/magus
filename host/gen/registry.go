//go:build !wasm

package gen

// This file is hand-maintained (not generated). TestModulesMatchStd guards it
// against drift from std.All(). The RegisterFunc / ModuleReg types live in
// module_reg.go (no build tag) so the wasm build can use them too; the
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
	"os":      {RegisterOs, false},
	"fs":      {RegisterFs, false},
	"vcs":     {RegisterVcs, false},
	"archive": {RegisterArchive, false},
	"http":    {RegisterHttp, false},

	// Pure compute: safe everywhere, including the WASM playground. Mirror this
	// exact set in registry_wasm.go, which the wasm build uses in place of this file.
	// uuid is here (its randomness uses the browser's getRandomValues) rather than
	// treated as context-dependent - a deliberate choice to let it run in-browser.
	"platform": {RegisterPlatform, true},
	"crypto":   {RegisterCrypto, true},
	"env":      {RegisterEnv, true},
	"json":     {RegisterJson, true},
	"time":     {RegisterTime, true},
	"fmt":      {RegisterFmt, true},
	"markdown": {RegisterMarkdown, true},
	"charm":    {RegisterCharm, true},
	"encoding": {RegisterEncoding, true},
	"path":     {RegisterPath, true},
	"strings":  {RegisterStrings, true},
	"semver":   {RegisterSemver, true},
	"yaml":     {RegisterYaml, true},
	"template": {RegisterTemplate, true},
	"toml":     {RegisterToml, true},
	"uuid":     {RegisterUuid, true},
}
