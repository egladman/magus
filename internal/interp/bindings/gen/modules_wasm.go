//go:build wasm

package gen

// Modules is the wasm build's view of the host-module registry: only the
// pure-compute modules whose std Impls and generated trampolines compile for wasm.
// registry.go holds the full set for the native build but is //go:build !wasm (it
// references the IO trampolines), so the browser playground - the sole wasm
// consumer, through internal/dry - needs this parallel table.
//
// Keep this in sync with the WASM-capable entries of registry.go. os, fs,
// vcs, archive, and http are absent because the browser has no process,
// filesystem, or arbitrary network - they are the only host modules left out.
var Modules = Set{
	"platform": {Register: RegisterPlatform, Capabilities: Capabilities(WASM)},
	"crypto":   {Register: RegisterCrypto, Capabilities: Capabilities(WASM)},
	"env":      {Register: RegisterEnv, Capabilities: Capabilities(WASM)},
	"json":     {Register: RegisterJson, Capabilities: Capabilities(WASM)},
	"xml":      {Register: RegisterXml, Capabilities: Capabilities(WASM)},
	"time":     {Register: RegisterTime, Capabilities: Capabilities(WASM)},
	"fmt":      {Register: RegisterFmt, Capabilities: Capabilities(WASM)},
	"markdown": {Register: RegisterMarkdown, Capabilities: Capabilities(WASM)},
	"charm":    {Register: RegisterCharm, Capabilities: Capabilities(WASM)},
	"encoding": {Register: RegisterEncoding, Capabilities: Capabilities(WASM)},
	"path":     {Register: RegisterPath, Capabilities: Capabilities(WASM)},
	"strings":  {Register: RegisterStrings, Capabilities: Capabilities(WASM)},
	"semver":   {Register: RegisterSemver, Capabilities: Capabilities(WASM)},
	"yaml":     {Register: RegisterYaml, Capabilities: Capabilities(WASM)},
	"template": {Register: RegisterTemplate, Capabilities: Capabilities(WASM)},
	"toml":     {Register: RegisterToml, Capabilities: Capabilities(WASM)},
	"uuid":     {Register: RegisterUuid, Capabilities: Capabilities(WASM)},
}
