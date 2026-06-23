//go:build !linux

package watch

import "context"

// newDefaultNotifier constructs the fsnotify-backed notifier for non-Linux
// platforms. Mirrors the setup that was previously inline in watch.New.
func newDefaultNotifier(ctx context.Context, roots []string, ignore func(string) bool) (notifier, error) {
	fsn, err := newFsnotifyNotifier()
	if err != nil {
		return nil, err
	}
	for _, root := range roots {
		if werr := walkAndWatch(ctx, root, ignore, fsn); werr != nil {
			_ = fsn.Close()
			return nil, werr
		}
	}
	return fsn, nil
}
