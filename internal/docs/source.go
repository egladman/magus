package docs

// RepoBlob is the GitHub source base for the docs' inline source links. It is
// pinned to the default branch rather than a commit hash on purpose: the committed
// docs embed these links, so a raw HEAD hash would rewrite on every commit and trip
// the `generate` drift gate. When a release tag exists this can point at it.
const RepoBlob = "https://github.com/egladman/magus/blob/main"

// SourceURL is the RepoBlob link to path, a slash-separated path from the module
// root. It names the file and never a line: a line anchor would rewrite the
// committed docs whenever the code it points at moved.
func SourceURL(path string) string { return RepoBlob + "/" + path }
