package interp

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var ErrNoMagusfile = errors.New("magusfile: not found")
var ErrUnknownTarget = errors.New("magusfile: unknown target")

var scriptExts = []string{"*.tl", "*.bzz"}

// engineForExt maps a file extension to an engine name.
func engineForExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".bzz":
		return "buzz"
	default:
		return "lua"
	}
}

// groupByEngine partitions files into one Source per engine, lua first.
func groupByEngine(dir string, files []string) []*Source {
	byEng := make(map[string][]string, 2)
	for _, f := range files {
		eng := engineForExt(f)
		byEng[eng] = append(byEng[eng], f)
	}
	var out []*Source
	for _, eng := range []string{"lua", "buzz"} {
		if fs := byEng[eng]; len(fs) > 0 {
			out = append(out, &Source{Dir: dir, Files: fs, Engine: eng})
		}
	}
	return out
}

// FindAll locates every magusfile source in dir grouped by engine, lua first.
// Returns ErrNoMagusfile when nothing is found; errors when single-file and
// magusfiles/ forms coexist.
func FindAll(dir string) ([]*Source, error) {
	mfDir := filepath.Join(dir, "magusfiles")
	if info, err := os.Stat(mfDir); err == nil && info.IsDir() {
		var entries []string
		for _, pat := range scriptExts {
			got, err := filepath.Glob(filepath.Join(mfDir, pat))
			if err != nil {
				return nil, err
			}
			entries = append(entries, got...)
		}
		if len(entries) > 0 {
			slices.Sort(entries)
			// Guard against mixing single-file and directory forms.
			for _, single := range []string{"magusfile.tl", "magusfile.bzz"} {
				if _, err2 := os.Stat(filepath.Join(dir, single)); err2 == nil {
					return nil, errors.New("interp: both " + single + " and magusfiles/ exist; remove one")
				}
			}
			return groupByEngine(dir, entries), nil
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// Single-file forms: collect every one that exists (they may coexist across
	// engines), in priority order.
	var files []string
	for _, name := range []string{"magusfile.tl", "magusfile.bzz"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	if len(files) == 0 {
		return nil, ErrNoMagusfile
	}
	return groupByEngine(dir, files), nil
}

// Find returns the primary (lua-first) magusfile source in dir, or ErrNoMagusfile.
func Find(dir string) (*Source, error) {
	srcs, err := FindAll(dir)
	if err != nil {
		return nil, err
	}
	return srcs[0], nil
}
