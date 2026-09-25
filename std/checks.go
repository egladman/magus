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
