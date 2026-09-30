package spell

import _ "embed"

// This file holds the generated Buzz `object` mirrors of every host-METHOD return type
// (target.go's mirrors are what a spell op WRITES). Each ships with the declarations of
// the import path whose method returns it (proc.exec returns ExecResult, so ExecResult
// ships with "os"), so a spell already doing `import "os";` can annotate `> ExecResult`
// with no second import.
//
// Ordering within each bundle matters: a struct-valued field mirrors as its Go type's
// bare name, which must be declared before the object referencing it.

// ExecResultSource is the generated Buzz `object ExecResult` mirror of
// types.ExecResult (see cmd/magus-utils types). Ships with "os": proc.exec /
// os.execSh return it, and magus's own describe-style methods (magus.build, ...)
// that also return ExecResult reuse the same mirror once "os" is imported for the
// exec call that produced it in the first place.
//
//go:generate go run ../../cmd/magus-utils types -type ExecResult -out gen/types/execresult.buzz
//go:generate go run ../../cmd/magus-utils types -type ShellCommand -out gen/types/shellcommand.buzz
//go:embed gen/types/execresult.buzz
var ExecResultSource string

// FileInfoSource is the generated Buzz mirror of the fs.stat result. Ships with "fs".
//
//go:generate go run ../../cmd/magus-utils types -type FileInfo -out gen/types/fileinfo.buzz
//go:generate go run ../../cmd/magus-utils types -type Status -out gen/types/status.buzz
//go:embed gen/types/fileinfo.buzz
var FileInfoSource string

// HTTPResponseSource is the generated Buzz mirror of an http.* response. Ships
// with "http".
//
//go:generate go run ../../cmd/magus-utils types -type HttpResponse -out gen/types/httpresponse.buzz
//go:embed gen/types/httpresponse.buzz
var HTTPResponseSource string

// URLSource is the generated Buzz mirror of encoding.parseUrl's result. Ships
// with "encoding".
//
//go:generate go run ../../cmd/magus-utils types -type URL -out gen/types/url.buzz
//go:embed gen/types/url.buzz
var URLSource string

// SemverVersionSource is the generated Buzz mirror of semver.parse's result. Ships with
// "semver".
//
// It is ALSO co-located into the "vcs" bundle, since a vcs tag's version is a
// SemverVersion and `import "vcs";` alone still needs it in scope. An import line inside
// vcs's bundle cannot reach "semver" (a synthetic module's companion source is only
// collected, never executed, so an import inside it is inert), hence duplicating the
// generated string into both bundles at assembly time.
//
//go:generate go run ../../cmd/magus-utils types -type SemverVersion -out gen/types/semverversion.buzz
//go:embed gen/types/semverversion.buzz
var SemverVersionSource string

// CommitAuthorSource / CommitSource are the generated Buzz mirrors of types.CommitAuthor
// and types.CommitRecord. Ship with "vcs": vcs.commit/vcs.history return Commit.
// CommitAuthor must precede Commit (Commit.author is CommitAuthor).
//
//go:generate go run ../../cmd/magus-utils types -type CommitAuthor -out gen/types/commitauthor.buzz
//go:embed gen/types/commitauthor.buzz
var CommitAuthorSource string

//go:generate go run ../../cmd/magus-utils types -type Commit -out gen/types/commit.buzz
//go:embed gen/types/commit.buzz
var CommitSource string

// TargetRunSource / RunSource are the generated Buzz mirrors of one run and the
// targets in it (types.StatusTargetRun and types.StatusRun), the same shape
// `magus status` reports. They exist so a caller can ITERATE a run: each target's
// state (queued/running/passed/failed/cached), how long it took, and the output ref
// it minted, rather than parsing magus's own console output back out of a string.
//
// TargetRun precedes Run, because Run.targets is a list of it and a struct-valued
// field mirrors as its bare type name, which must already be declared.
//
//go:generate go run ../../cmd/magus-utils types -type TargetRun -out gen/types/targetrun.buzz
//go:embed gen/types/targetrun.buzz
var TargetRunSource string

//go:generate go run ../../cmd/magus-utils types -type Run -out gen/types/run.buzz
//go:embed gen/types/run.buzz
var RunSource string
