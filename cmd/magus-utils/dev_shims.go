package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
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
// Usage: `magus-utils dev-shims [-root .]`.
func runDevShims(args []string) error {
	fset := flag.NewFlagSet("dev-shims", flag.ContinueOnError)
	root := fset.String("root", ".", "checkout whose tracked files are scanned")
	if err := fset.Parse(args); err != nil {
		return err
	}
	if fset.NArg() != 0 {
		return fmt.Errorf("dev-shims takes no arguments, got %q", fset.Args())
	}
	ctx := context.Background()
	res, err := vcs.Resolve(ctx, *root, "", types.VCSOptions{})
	if err != nil {
		return err
	}
	if res.VCS == nil {
		return errors.New("no version control says which files are tracked, so no release can be judged")
	}
	tracked, err := res.VCS.TrackedFiles(ctx, *root, []string{"."})
	if err != nil {
		return err
	}
	dirs, err := checkDevShims(os.DirFS(*root), tracked)
	if err != nil {
		return err
	}
	fmt.Printf("no development-only shim remains in %d tracked files", len(tracked)-len(dirs))
	if len(dirs) > 0 {
		fmt.Printf("; %d tracked directory entries were not scanned: %s", len(dirs), strings.Join(dirs, ", "))
	}
	fmt.Println()
	return nil
}

// checkDevShims names each file:line in paths with a dev-only shim or a near
// miss of its marker, and returns the directory entries it skipped. A tracked
// file deleted in the working tree ships nothing, so it is skipped too.
func checkDevShims(fsys fs.FS, paths []string) ([]string, error) {
	var left, near, dirs []string
	for _, p := range paths {
		info, err := fs.Stat(fsys, p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			dirs = append(dirs, p)
			continue
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, err
		}
		if bytes.IndexByte(data, 0) >= 0 || !bytes.Contains(data, []byte(marker.Compat)) {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(nil, len(data)+1)
		for n := 1; sc.Scan(); n++ {
			switch line := sc.Text(); {
			case marker.DevOnly(line):
				left = append(left, fmt.Sprintf("%s:%d", p, n))
			case marker.NearDevOnly(line):
				near = append(near, fmt.Sprintf("%s:%d", p, n))
			}
		}
	}
	var errs []error
	if len(left) > 0 {
		errs = append(errs, fmt.Errorf("%d development-only shim(s) remain, each marked %s%s): "+
			"delete each shim and its marker before cutting a release: %s",
			len(left), marker.Compat, marker.NextRelease, strings.Join(left, ", ")))
	}
	if len(near) > 0 {
		errs = append(errs, fmt.Errorf("%d compat marker(s) nearly read %s%s): "+
			"write the condition exactly if the shim is development-only, or name the event that retires it: %s",
			len(near), marker.Compat, marker.NextRelease, strings.Join(near, ", ")))
	}
	return dirs, errors.Join(errs...)
}
