//go:build wasm

package gen

// Modules is the wasm build's view of the host-module registry: only the
// pure-compute modules whose std Impls and generated trampolines compile for wasm.
// registry.go holds the full set for the native build but is //go:build !wasm (it
// references the IO trampolines), so the browser playground - the sole wasm
// consumer, through internal/dry - needs this parallel table.
//
// Keep this in sync with the WASMCompatible:true entries of registry.go. os, fs,
// vcs, archive, and http are absent because the browser has no process,
// filesystem, or arbitrary network - they are the only host modules left out.
var Modules = map[string]ModuleReg{
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
