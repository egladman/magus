//go:build linux

package watch

import "context"

// newDefaultNotifier uses a direct inotify backend on Linux (requires 2.6.36+ for IN_EXCL_UNLINK);
// falls back to fsnotify if InotifyInit1 fails (restricted containers or old kernels).
func newDefaultNotifier(ctx context.Context, roots []string, ignore func(string) bool) (notifier, error) {
	n, err := newInotifyNotifier()
	if err != nil {
		// Graceful fallback: proceed with the portable fsnotify backend.
		fsn, ferr := newFsnotifyNotifier()
		if ferr != nil {
			return nil, ferr
		}
		for _, root := range roots {
			if werr := walkAndWatch(ctx, root, ignore, fsn); werr != nil {
				_ = fsn.Close()
				return nil, werr
			}
		}
		return fsn, nil
	}
	for _, root := range roots {
		if werr := n.addTree(ctx, root, ignore); werr != nil {
			_ = n.Close()
			return nil, werr
		}
	}
	return n, nil
}
