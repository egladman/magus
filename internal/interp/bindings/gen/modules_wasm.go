//go:build wasm

package gen

// Modules is the wasm build's view of the host-module registry: only the
// pure-compute modules whose std Impls and generated trampolines compile for wasm.
// modules.go holds the full set for the native build but is //go:build !wasm (it
// references the IO trampolines), so the browser playground - the sole wasm
// consumer, through internal/dry - needs this parallel table.
//
// Keep this in sync with the WASM-capable entries of modules.go. os, fs,
// vcs, archive, and http are absent because the browser has no process,
// filesystem, or arbitrary network - they are the only host modules left out.
var Modules = Set{
	"platform": {Register: RegisterPlatform, Capabilities: Capabilities(WASM)},
	"crypto":   {Register: RegisterCrypto, Capabilities: Capabilities(WASM)},
	"env":      {Register: RegisterEnv, Capabilities: Capabilities(WASM)},
	"json":     {Register: RegisterJson, Capabilities: Capabilities(WASM), Path: "encoding/json"},
	"xml":      {Register: RegisterXml, Capabilities: Capabilities(WASM), Path: "encoding/xml"},
	"time":     {Register: RegisterTime, Capabilities: Capabilities(WASM)},
	"fmt":      {Register: RegisterFmt, Capabilities: Capabilities(WASM)},
	"markdown": {Register: RegisterMarkdown, Capabilities: Capabilities(WASM)},
	"charm":    {Register: RegisterCharm, Capabilities: Capabilities(WASM)},
	"log":      {Register: RegisterLog, Capabilities: Capabilities(WASM)},
	"diff":     {Register: RegisterDiff, Capabilities: Capabilities(WASM)},
	"sort":     {Register: RegisterSort, Capabilities: Capabilities(WASM)},
	"math":     {Register: RegisterMath, Capabilities: Capabilities(WASM)},
	"base64":   {Register: RegisterBase64, Capabilities: Capabilities(WASM), Path: "encoding/base64"},
	"hex":      {Register: RegisterHex, Capabilities: Capabilities(WASM), Path: "encoding/hex"},
	"csv":      {Register: RegisterCsv, Capabilities: Capabilities(WASM), Path: "encoding/csv"},
	"ini":      {Register: RegisterIni, Capabilities: Capabilities(WASM), Path: "encoding/ini"},
	"url":      {Register: RegisterUrl, Capabilities: Capabilities(WASM), Path: "encoding/url"},
	"path":     {Register: RegisterPath, Capabilities: Capabilities(WASM)},
	"strings":  {Register: RegisterStrings, Capabilities: Capabilities(WASM)},
	"semver":   {Register: RegisterSemver, Capabilities: Capabilities(WASM)},
	"yaml":     {Register: RegisterYaml, Capabilities: Capabilities(WASM), Path: "encoding/yaml"},
	"template": {Register: RegisterTemplate, Capabilities: Capabilities(WASM)},
	"toml":     {Register: RegisterToml, Capabilities: Capabilities(WASM), Path: "encoding/toml"},
	"uuid":     {Register: RegisterUuid, Capabilities: Capabilities(WASM)},
}
