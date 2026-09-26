package risk

import "context"

// Prover is one language's proofs about changed files, beyond what comment syntax alone
// shows. Assess asks a prover only for proofs that lower a path's tier, so a prover that
// cannot answer leaves the path where the classes put it.
type Prover interface {
	// Equivalent reports whether cur, the changed content of path, means the same as
	// old, its content at the base (a comment or format edit), and why.
	Equivalent(path, old, cur string) (bool, string)
	// Place maps workspace-relative paths to the packages that compile or embed them.
	// root is the absolute workspace root. A path the prover does not know is absent
	// from the result; an error means nothing it would have placed can be trusted.
	Place(ctx context.Context, root string, paths []string) (Placement, error)
}

// Placement is where a prover put changed paths.
type Placement struct {
	// Packages maps each placed path to the package that compiles or embeds it.
	Packages map[string]PackageHit
	// Unplaced maps a path the prover knows but cannot bound to the reason: a package
	// that failed to load, code only a generator reaches, a file this platform does not
	// build. Such a path gates full.
	Unplaced map[string]string
	// Closure returns every package whose build or tests can observe a change to the
	// changed packages, plus testOnly (packages whose test files alone changed), within
	// the module those packages belong to.
	Closure func(changed, testOnly []string) []string
	// Narrows is the spell op (`go::go-test`) whose package arguments a scoped gate
	// narrows to the closure. The project's test target runs whole around it, so the
	// env and flags its body gives the op still apply.
	Narrows string
}

// PackageHit is one placed path.
type PackageHit struct {
	// Package is the package's import path.
	Package string
	// Module is the workspace-relative directory of the module holding Package; the
	// project rooted there runs its narrowed tests.
	Module string
	// TestOnly is true for a file only the package's tests compile.
	TestOnly bool
	// Why names what placed it, for the path's evidence line.
	Why string
}
