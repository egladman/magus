package std

import "context"

// CheckRead refuses a read of path, resolved against ctx's working directory, that the
// sandbox policy on ctx denies, with the MGS2001 every fs method raises. It is nil when
// ctx carries no policy. For bindings outside this package that wrap Buzz's own stdlib,
// so those reach the same check rather than a copy of it.
func CheckRead(ctx context.Context, path string) error {
	return checkRead(ctx, resolvePath(ctx, path))
}

// CheckWrite is CheckRead for a write, raising MGS2002.
func CheckWrite(ctx context.Context, path string) error {
	return checkWrite(ctx, resolvePath(ctx, path))
}

// EditJudge grades an fs write that rewrites an existing file.
// The content writers consult the one on ctx before they write.
type EditJudge struct {
	// Covers reports the absolute paths the judge grades.
	Covers func(ctx context.Context, path string) bool
	// Grade refuses the change from before to after with an error.
	Grade func(ctx context.Context, path string, before, after []byte) error
}

type editJudgeKey struct{}

// WithEditJudge returns ctx carrying j for the fs writers.
// Without one they ask the workspace on ctx for its judge.
// A ctx with a sandbox policy and neither refuses every content write:
// a door that confines a script grades it too.
func WithEditJudge(ctx context.Context, j EditJudge) context.Context {
	return context.WithValue(ctx, editJudgeKey{}, j)
}

// findWorkspaceRoot is workspace.FindRoot, installed by the root package:
// importing it here closes a cycle through internal/spell's tests.
var findWorkspaceRoot func(dir string) (string, error)

// SetWorkspaceRootFinder installs the root discovery the fs writers use
// to ask only a file's own workspace for its edit judge.
// The root magus package calls it in its init.
func SetWorkspaceRootFinder(f func(dir string) (string, error)) { findWorkspaceRoot = f }

func editJudgeFromContext(ctx context.Context) (EditJudge, bool) {
	j, ok := ctx.Value(editJudgeKey{}).(EditJudge)
	return j, ok
}
