package job

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// globMeta are the characters that make a declared path a glob, as [LiteralPrefix]
// reads them.
const globMeta = "*?[{"

// Matching is the first declaration in decls that covers p. The declaration comes back
// rather than a bool because a rejection has to name which one fired.
func Matching(decls []string, p string) (string, bool) {
	for _, d := range decls {
		if Covers(d, p) {
			return d, true
		}
	}
	return "", false
}

// Covers decides whether one declared path covers a written one.
//
// Two rules, because a declaration means two different things. A declaration holding glob
// metacharacters is matched AS a doublestar glob. A literal one names a file or a
// directory, and a directory covers what is under it: `internal/ledger` has to cover
// `internal/ledger/store.go` or every ordinary lease reads as a boundary violation.
//
// The glob arm deliberately does NOT fall back to its literal prefix. That fallback is
// right for [LiteralPrefix]'s caller, which over-reports collisions between two
// declarations on purpose; here over-reporting means silently ACCEPTING a write the
// declaration excludes, and the two directions of error are not symmetrical.
//
// A declaration claim (`run.go#executeStages`) covers its whole file here: which lines of
// the file it covers is the footprint's question.
func Covers(declared, p string) bool {
	declared, _ = SplitClaim(declared)
	return CoversPath(declared, p)
}

// CoversPath is [Covers] for a declaration [SplitClaim] already cut, which must not be
// cut again: its path may hold a literal `#`.
func CoversPath(declared, p string) bool {
	// A blank declaration claims nothing, on PathsIntersect's rule: it cleans to ".",
	// which the whole-tree arm below would read as a claim on everything.
	if declared == "" || strings.TrimSpace(p) == "" {
		return false
	}
	declared, p = path.Clean(declared), path.Clean(strings.TrimSpace(p))
	if p == "." {
		return false
	}
	if declared == "." {
		// The repository root covers every path under it. Refusing "." at declaration
		// instead would leave a single-lease plan over the whole tree undeclarable, and
		// the row would read as owning nothing rather than as owning everything.
		return true
	}
	if strings.ContainsAny(declared, globMeta) {
		ok, _ := doublestar.Match(declared, p)
		return ok
	}
	return p == declared || strings.HasPrefix(p, declared+"/")
}

// CleanPath is a path a version control or a person named, as a slash path.
func CleanPath(p string) string {
	return path.Clean(filepath.ToSlash(strings.TrimSpace(p)))
}

// EntryPath cleans the path an entry names, refusing one that is not workspace-relative.
func EntryPath(rel string) (string, error) {
	clean := CleanPath(rel)
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("job: enter %q: name one workspace-relative path inside the job's write paths", rel)
	}
	return clean, nil
}

// releasedFile reports whether row released file whole, which ends the hold any pattern
// of its write paths had on it.
func releasedFile(row Job, file string) bool {
	return FileRelease(row, file) >= 0
}

// FileRelease is the index of row's whole-file release of file, or -1.
func FileRelease(row Job, file string) int {
	file = path.Clean(file)
	return slices.IndexFunc(row.Releases, func(r Release) bool {
		p, decl := SplitClaim(r.Path)
		return decl == "" && path.Clean(p) == file
	})
}

// Narrowed reports whether the store narrowed row's hold on file: row holds the file by
// its claims alone.
func Narrowed(row Job, file string) bool {
	at := FileRelease(row, file)
	return at >= 0 && row.Releases[at].Narrowed
}

// ReleasedReach reports whether entry, a whole-file write path of row, stops
// short of the file other names. A pattern stops at any release of the file;
// a literal entry only at a narrowed one.
func ReleasedReach(row Job, entry, other string) bool {
	p, decl := SplitClaim(entry)
	if decl != "" {
		return false
	}
	o, _ := SplitClaim(other)
	o = path.Clean(strings.TrimSpace(o))
	if strings.ContainsAny(o, globMeta) || !CoversPath(p, o) {
		return false
	}
	if strings.ContainsAny(p, globMeta) {
		return releasedFile(row, o)
	}
	return Narrowed(row, o)
}

// ClaimsOnFile are the declarations row's write paths claim in file.
func ClaimsOnFile(row Job, file string) []string {
	var out []string
	for _, entry := range row.Paths.Write {
		if p, decl := SplitClaim(entry); decl != "" && path.Clean(p) == file {
			out = append(out, decl)
		}
	}
	return out
}

// heldRequests are row's release requests for files it still holds. Narrowing
// past a file answers the request for it.
func heldRequests(row Job) []ReleaseRequest {
	return slices.DeleteFunc(row.ReleaseRequests, func(r ReleaseRequest) bool {
		return !slices.ContainsFunc(row.HardPaths(), func(entry string) bool {
			return Covers(entry, r.File) && !ReleasedReach(row, entry, r.File)
		})
	})
}

