//go:build wasm

package registry

import gen "github.com/egladman/magus/host/gen"

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
	"platform": {gen.RegisterPlatform, true},
	"crypto":   {gen.RegisterCrypto, true},
	"env":      {gen.RegisterEnv, true},
	"json":     {gen.RegisterJson, true},
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
