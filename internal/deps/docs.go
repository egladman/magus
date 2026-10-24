package deps

import "fmt"

// DocsURL is the canonical documentation page for one release of a package, derived
// from its manager, name and version and never fetched: fetching would be a network
// act, and provider I/O belongs to Buzz, not to Go. It returns "", false for an empty
// version or a manager with no reader here.
//
// A caller skips a Replaced package: its name is the original's and its version the
// replacement's, so the pair names a page that describes neither.
//
// What derivation cannot know: a private Go module or npm registry has no public page,
// a docs.rs build can fail, and a PyPI project name need not be the import name. The
// URL is where the page would be, not proof it exists.
func DocsURL(manager, name, version string) (string, bool) {
	if name == "" || version == "" {
		return "", false
	}
	switch manager {
	case managerGo:
		return fmt.Sprintf("https://pkg.go.dev/%s@%s", name, version), true
	case managerNode:
		return fmt.Sprintf("https://www.npmjs.com/package/%s/v/%s", name, version), true
	case managerPython:
		return fmt.Sprintf("https://pypi.org/project/%s/%s/", normalizePython(name), version), true
	case managerCargo:
		return fmt.Sprintf("https://docs.rs/%s/%s", name, version), true
	}
	return "", false
}
