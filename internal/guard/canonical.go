package guard

import (
	"os"
	"path/filepath"
	"strings"
)

// maxSymlinkHops is Linux's MAXSYMLINKS. Past it the kernel answers
// ELOOP, so the path opens nothing.
const maxSymlinkHops = 40

// canonicalPath is the file the kernel opens for p. It resolves each
// symlink before reading the next component, so `a/l/../x` lands
// where l points; filepath.Clean would say `a/x`. A dangling last
// link is followed, and a missing component ends resolution.
func canonicalPath(p string) string {
	if !filepath.IsAbs(p) {
		wd, err := os.Getwd()
		if err != nil {
			// With no directory, only the lexical reading is left.
			return filepath.Clean(p)
		}
		p = wd + string(filepath.Separator) + p
	}
	vol := filepath.VolumeName(p)
	resolved := vol + string(filepath.Separator)
	pending := pathComponents(p[len(vol):])
	hops, exists := 0, true
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, c)
		if !exists {
			resolved = next
			continue
		}
		info, err := os.Lstat(next)
		if err != nil {
			exists = false
			resolved = next
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 || hops >= maxSymlinkHops {
			resolved = next
			continue
		}
		target, err := os.Readlink(next)
		if err != nil {
			resolved = next
			continue
		}
		hops++
		if filepath.IsAbs(target) {
			tv := filepath.VolumeName(target)
			resolved = tv + string(filepath.Separator)
			target = target[len(tv):]
		}
		pending = append(pathComponents(target), pending...)
	}
	return resolved
}

func pathComponents(p string) []string {
	return strings.Split(filepath.ToSlash(p), "/")
}

// placedPath is absolute p with each `..` read where the kernel reads
// it. Otherwise the spelling is kept, so a denial names what the
// caller wrote. Only a symlink before a `..` makes cleaning wrong.
func placedPath(p string) string {
	comps := pathComponents(p)
	last := -1
	for i, c := range comps {
		if c == ".." {
			last = i
		}
	}
	if last < 0 {
		return filepath.Clean(p)
	}
	raw := filepath.FromSlash(strings.Join(comps[:last+1], "/"))
	head := filepath.Clean(raw)
	if resolved := canonicalPath(raw); resolved != canonicalPath(head) {
		head = resolved
	}
	return filepath.Join(append([]string{head}, comps[last+1:]...)...)
}
