package std

// buzzStdlibEquiv maps host "module.method" entries (snake_case, as declared in
// the std.Module descriptors) to the Buzz stdlib call that covers the same need.
// Its purpose is informational: docs and `magus describe module` cross-reference
// the Buzz stdlib equivalent so authors who prefer the stdlib know it exists. It
// does not drive code generation — every host method is emitted regardless.
//
// magus keeps its own form even where a Buzz equivalent exists, because several
// are sandbox-aware where the bare stdlib is not: env.get/lookup honor the env
// allowlist (a stripped secret reads as unset), whereas Buzz's os.env is raw
// os.LookupEnv. Inside a sandbox the magus form is the safer surface.
//
// Only genuine duplicates are listed. Entries whose magus behavior the stdlib
// can't reproduce are deliberately absent: os.exit raises a lifecycle ExitError
// (Buzz's os.exit hard-exits the process), os.sleep is cancellable (Buzz's
// blocks), crypto.*_file hashes a file (Buzz's hash only takes a string), and
// crypto.*_hex returns hex where Buzz's crypto.hash returns the RAW digest
// bytes - the equivalence once claimed here is what shipped v0.4.2's
// SHA256SUMS as raw digests.
var buzzStdlibEquiv = map[string]string{
	"fs.exists":              "fs.exists",
	"fs.mkdir_all":           "fs.makeDirectory",
	"fs.remove_all":          "fs.delete",
	"fs.list_dir":            "fs.list",
	"json.parse":             "serialize.jsonDecode",
	"json.stringify":         "serialize.jsonEncode",
	"env.get":                "os.env",
	"env.lookup":             "os.env (returns null when unset)",
	"encoding.base64_encode": `str.encodeBase64 (built-in string method)`,
	"encoding.hex_encode":    `str.hex (built-in string method)`,
}

// BuzzStdlibEquiv reports whether module.method has a Buzz stdlib call that
// covers the same need, returning that Buzz call. The magus method is still
// emitted; this is an informational pointer for authors who prefer Buzz's own
// stdlib. Both names are the snake_case descriptor names.
func BuzzStdlibEquiv(module, method string) (string, bool) {
	e, ok := buzzStdlibEquiv[module+"."+method]
	return e, ok
}
