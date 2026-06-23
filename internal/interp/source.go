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

// scriptExts are the magusfile glob patterns, and enginePriority the engine
// preference order. Buzz is currently the only engine; these slices are the seam
// a second engine would extend (add its glob here, a branch to engineForExt, and
// register it under engine.Register).
var scriptExts = []string{"*.buzz"}
var enginePriority = []string{"buzz"}

// engineForExt maps a file extension to an engine name. Only .buzz is globbed
// today, so every file routed here is Buzz; the switch is the extension point for
// a future engine rather than dead generality.
func engineForExt(path string) string { //nolint:unparam // intentional seam for a future engine (see doc)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".buzz":
		return "buzz"
	default:
		return "buzz"
	}
}

// groupByEngine partitions files into one Source per engine, in priority order.
func groupByEngine(dir string, files []string) []*Source {
	byEng := make(map[string][]string, len(enginePriority))
	for _, f := range files {
		eng := engineForExt(f)
		byEng[eng] = append(byEng[eng], f)
	}
	var out []*Source
	for _, eng := range enginePriority {
		if fs := byEng[eng]; len(fs) > 0 {
			out = append(out, &Source{Dir: dir, Files: fs, Engine: eng})
		}
	}
	return out
}

// FindAll locates every magusfile source in dir grouped by engine, in priority
// order. Returns ErrNoMagusfile when nothing is found; errors when single-file
// and magusfiles/ forms coexist.
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
			if _, err2 := os.Stat(filepath.Join(dir, "magusfile.buzz")); err2 == nil {
				return nil, errors.New("interp: both magusfile.buzz and magusfiles/ exist; remove one")
			}
			return groupByEngine(dir, entries), nil
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	// Single-file form.
	path := filepath.Join(dir, "magusfile.buzz")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNoMagusfile
		}
		return nil, err
	}
	return groupByEngine(dir, []string{path}), nil
}

// Find returns the primary magusfile source in dir, or ErrNoMagusfile.
func Find(dir string) (*Source, error) {
	srcs, err := FindAll(dir)
	if err != nil {
		return nil, err
	}
	if len(srcs) == 0 {
		return nil, ErrNoMagusfile
	}
	return srcs[0], nil
}
