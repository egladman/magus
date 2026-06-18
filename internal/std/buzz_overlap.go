package std

// nativeBuzzEquiv maps host "module.method" entries (snake_case, as declared in
// the std.Module descriptors) to the Buzz stdlib call that covers the same need.
//
// Historically these were *omitted* from the generated Buzz surface so `extra`
// stayed a strict delta over Buzz's stdlib. We dropped that: `extra` is now
// self-complete — every method is emitted onto both the Teal and Buzz surfaces.
// Rationale:
//
//   - Predictability over non-duplication. With the delta model a single
//     concept was split across two namespaces (in Buzz, fs.exists was native
//     but fs.join/glob/basename were extra.fs), so authors had to memorize which
//     side each call lived on. Self-complete extra means one import covers a
//     whole domain: reach extra.fs.* (or require("magus.extra.fs")) and never
//     wonder "is this native or extra?". It does not shadow Buzz's stdlib —
//     extra.fs.exists and native fs.exists are different namespaces.
//   - Sandbox-awareness. Several host forms enforce the magus sandbox policy
//     that the bare stdlib bypasses: extra.env.get/lookup honor the env
//     allowlist (a stripped secret reads as unset), whereas Buzz's os.env is raw
//     os.LookupEnv. Inside a sandbox the extra form is the safer surface.
//
// The map now serves a single, informational purpose: docs and `magus describe
// module` cross-reference the native Buzz equivalent so authors who prefer the
// stdlib know it exists. It no longer drives code generation.
//
// Only genuine duplicates are listed. Entries whose magus behavior the stdlib
// can't reproduce are deliberately absent: os.exit raises a lifecycle ExitError
// (Buzz's os.exit hard-exits the process), os.sleep is cancellable (Buzz's
// blocks), and crypto.*_file hashes a file (Buzz's hash only takes a string).
var nativeBuzzEquiv = map[string]string{
	"fs.exists":              "fs.exists",
	"fs.mkdirall":            "fs.makeDirectory",
	"fs.remove_all":          "fs.delete",
	"fs.list_dir":            "fs.list",
	"crypto.sha256_hex":      "crypto.hash(HashAlgorithm.Sha256, …)",
	"crypto.sha512_hex":      "crypto.hash(HashAlgorithm.Sha512, …)",
	"crypto.sha1_hex":        "crypto.hash(HashAlgorithm.Sha1, …)",
	"crypto.md5_hex":         "crypto.hash(HashAlgorithm.Md5, …)",
	"json.parse":             "serialize.jsonDecode",
	"json.stringify":         "serialize.jsonEncode",
	"env.get":                "os.env",
	"env.lookup":             "os.env (returns null when unset)",
	"encoding.base64_encode": `str.encodeBase64 (built-in string method)`,
	"encoding.hex_encode":    `str.hex (built-in string method)`,
}

// NativeBuzzEquiv reports whether module.method has a Buzz stdlib call that
// covers the same need, returning that native call. The method is still on the
// extra surface (extra is self-complete); this is an informational pointer for
// authors who prefer Buzz's own stdlib. Both names are the snake_case descriptor
// names.
func NativeBuzzEquiv(module, method string) (string, bool) {
	e, ok := nativeBuzzEquiv[module+"."+method]
	return e, ok
}
