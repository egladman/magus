//go:build !wasm

package gen

import (
	"context"

	buzz "github.com/egladman/gopherbuzz"
	vm "github.com/egladman/gopherbuzz/vm"
)

// This file is hand-maintained (not generated). TestModulesMatchStd guards it
// against drift from std.All().

// RegisterFunc installs a host module on a Buzz session and returns its module map.
type RegisterFunc func(context.Context, *buzz.Session) vm.Value

// ModuleReg is one host module's registration: how to install it, and whether it
// is safe under WASM (pure compute, no filesystem, process, network, or OS
// randomness), which the browser playground requires.
type ModuleReg struct {
	Register       RegisterFunc
	WASMCompatible bool
}

// Modules is the single source of truth for magus's host modules: every bare
// import name a Buzz session resolves beyond Buzz's own stdlib, mapped to its
// generated Register trampoline. Each surface derives its set from this one table
// instead of a hand-kept list: the magusfile engine and `magus buzz` install all
// of them; the browser playground installs the WASMCompatible subset.
//
// The `magus` namespace is intentionally absent. It is not a bare import; it is
// wired onto the magus.* namespace with a magusfile's target context.
var Modules = map[string]ModuleReg{
	// Context-dependent (sandbox / process / network / randomness): full surface
	// only, never the browser playground.
	"os":      {RegisterOs, false},
	"fs":      {RegisterFs, false},
	"vcs":     {RegisterVcs, false},
	"archive": {RegisterArchive, false},
	"http":    {RegisterHttp, false},
	"uuid":    {RegisterUuid, false},

	// Pure compute: safe everywhere, including the WASM playground.
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
}
