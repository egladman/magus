package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/egladman/magus/internal/marker"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// runDevShims refuses a release while tracked source still holds a
// development-only shim, one whose compat condition is marker.NextRelease.
//
// Usage: magus-utils dev-shims [-root .]
func runDevShims(args []string) error {
	root := "."
	if len(args) == 2 && args[0] == "-root" {
		root = args[1]
	} else if len(args) != 0 {
		return errors.New("usage: magus-utils dev-shims [-root .]")
	}
	ctx := context.Background()
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil {
		return err
	}
	if res.VCS == nil {
		return errors.New("no version control says which files are tracked, so no release can be judged")
	}
	tracked, err := res.VCS.TrackedFiles(ctx, root, []string{"."})
	if err != nil {
		return err
	}
	if err := checkDevShims(os.DirFS(root), tracked); err != nil {
		return err
	}
	fmt.Printf("no development-only shim remains in %d tracked files\n", len(tracked))
	return nil
}

// checkDevShims names every file:line in paths that carries a
// development-only shim. A path that no longer exists is skipped: a tracked
// file deleted in the working tree ships nothing.
func checkDevShims(fsys fs.FS, paths []string) error {
	var left []string
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if bytes.IndexByte(data, 0) >= 0 || !bytes.Contains(data, []byte(marker.Compat)) {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(nil, len(data)+1)
		for n := 1; sc.Scan(); n++ {
			if marker.DevOnly(sc.Text()) {
				left = append(left, fmt.Sprintf("%s:%d", p, n))
			}
		}
	}
	if len(left) == 0 {
		return nil
	}
	return fmt.Errorf("%d development-only shim(s) remain, each marked %s%s): "+
		"delete each shim and its marker before cutting a release:\n  %s",
		len(left), marker.Compat, marker.NextRelease, strings.Join(left, "\n  "))
}
