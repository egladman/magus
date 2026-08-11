//go:build !wasm

package gen

// This file is hand-maintained (not generated). TestModulesMatchStd guards it
// against drift from std.All(). The RegisterFunc / ModuleReg types live in
// types.go (no build tag) so the wasm build can use them too; the
// entries carrying the WASM capability here are mirrored in modules_wasm.go, which the
// wasm build sees instead of this file.

// Modules is the single source of truth for magus's host modules: every bare
// import name a Buzz session resolves beyond Buzz's own stdlib, mapped to its
// generated Register trampoline. Each surface derives its set from this one table
// instead of a hand-kept list: the magusfile engine and `magus buzz` install all
// of them; the browser playground installs the WASMCompatible subset.
//
// The `magus` namespace is intentionally absent. It is not a bare import; it is
// wired onto the magus.* namespace with a magusfile's target context.
var Modules = Set{
	// Context-dependent (process / filesystem / network): full surface only. The
	// browser has no way to provide these, so they are never in the playground.
	"os":      {Register: RegisterOs},
	"fs":      {Register: RegisterFs},
	"vcs":     {Register: RegisterVcs},
	"archive": {Register: RegisterArchive},
	"http":    {Register: RegisterHttp},
	"term":    {Register: RegisterTerm},
	"net":     {Register: RegisterNet},

	// Pure compute: safe everywhere, including the WASM playground. Mirror this
	// exact set in modules_wasm.go, which the wasm build uses in place of this file.
	// uuid is here (its randomness uses the browser's getRandomValues) rather than
	// treated as context-dependent - a deliberate choice to let it run in-browser.
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
