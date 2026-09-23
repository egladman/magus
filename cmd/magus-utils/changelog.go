package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// fragment is one unreleased changelog entry, kept in its own file under
// changes/unreleased/ so concurrent changes never edit a shared file. The file is
// the entry exactly as it reads under [Unreleased]: one Keep a Changelog section
// heading and one entry in lintUnreleased's shape. A breaking change says so in
// its headline, as every released entry does:
//
//	### Removed
//
//	- **Breaking: the `exclusive` option.** A magusfile that sets it fails with
//	  MGS1038; delete the key.
type fragment struct {
	path    string
	section string
	// entry is the "- **headline**" line and its two-space continuations.
	entry string
}

// parseFragment reads one fragment, reporting every departure from the format
// against path.
func parseFragment(path string, data []byte) (fragment, error) {
	text := strings.TrimRight(strings.ReplaceAll(string(data), "\r\n", "\n"), " \n")
	if problems := lintUnreleased(text); len(problems) > 0 {
		return fragment{}, fmt.Errorf("%s: %s", path, strings.Join(problems, "; "))
	}
	f := fragment{path: path}
	var entries int
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "### "):
			if f.section != "" {
				return fragment{}, fmt.Errorf("%s: holds more than one section heading; write one fragment per entry", path)
			}
			f.section = strings.TrimSpace(line[4:])
		case strings.HasPrefix(line, "- "):
			entries++
			lines = append(lines, line)
		case line != "":
			lines = append(lines, line)
		}
	}
	if f.section == "" {
		return fragment{}, fmt.Errorf("%s: opens with no `### <group>` heading, one of %s", path, strings.Join(changelogSections, ", "))
	}
	if entries != 1 {
		return fragment{}, fmt.Errorf("%s: holds %d entries; write one fragment per entry", path, entries)
	}
	f.entry = strings.Join(lines, "\n")
	return f, nil
}

// readFragments parses every fragment in dir in file-name order, the order entries
// render in within a section. A missing dir holds no fragments. Dotfiles are
// skipped; any other file that is not a .md fragment is an error. Every failure is
// reported, not only the first.
func readFragments(dir string) ([]fragment, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var frags []fragment
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		if e.IsDir() || filepath.Ext(name) != ".md" {
			errs = append(errs, fmt.Errorf("%s: not a fragment; %s holds only <name>.md files", path, dir))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		f, err := parseFragment(path, data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		frags = append(frags, f)
	}
	return frags, errors.Join(errs...)
}

// renderUnreleased assembles fragments into an [Unreleased] body: sections in
// changelogSections order, entries in fragment order. It is "" for no fragments.
func renderUnreleased(frags []fragment) string {
	var b strings.Builder
	for _, section := range changelogSections {
		opened := false
		for _, f := range frags {
			if f.section != section {
				continue
			}
			if !opened {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString("### " + section + "\n\n")
				opened = true
			}
			b.WriteString(f.entry + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// runLintFragments checks the named fragment files and reports every malformed
// one; pr-changelog runs it over the fragments a pull request adds.
//
// Usage: magus-utils lint-fragments changes/unreleased/<name>.md...
func runLintFragments(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: magus-utils lint-fragments <fragment.md> [<fragment.md>...]")
	}
	var errs []error
	for _, path := range args {
		if filepath.Ext(path) != ".md" {
			errs = append(errs, fmt.Errorf("%s: not a fragment; fragments are <name>.md files", path))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := parseFragment(path, data); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	fmt.Printf("%d fragment(s) follow the changelog format\n", len(args))
	return nil
}
