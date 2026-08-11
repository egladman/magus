package std

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

//go:generate go run ../cmd/magus-utils bindings -module ini -lang buzz -out ../internal/interp/bindings/gen/ini.go

func init() { Register(INI) }

// iniGlobalSection is the key holding entries that appear before any [section]
// header. The empty string cannot collide with a real section name, because a
// literal `[]` header is not a name a config file writes.
const iniGlobalSection = ""

// INI is the "encoding/ini" host module: the key=value config format that has no
// standard and is everywhere anyway - .npmrc, .gitconfig, .editorconfig,
// setup.cfg, .flake8, most systemd units.
//
// tools/audit.buzz is the reason this exists. It carries a `coolingHours`
// constant with the comment "mirrors minimum-release-age in each project's .npmrc
// ... restated as a duration because .npmrc is not a format Buzz reads" - a value
// duplicated by hand, in a second unit, that can drift from the file it mirrors.
// That is what a missing parser costs.
//
// WHAT THIS PARSES, stated plainly because INI has no specification and every
// implementation draws the line somewhere different:
//
//   - `[section]` headers; entries before the first are under the "" key.
//   - `key = value` and `key=value`; surrounding whitespace is trimmed from both.
//   - `;` and `#` comment lines. A `#` INSIDE a value is kept, because a value is
//     frequently a URL with a fragment and treating that as a comment silently
//     truncates it.
//   - Quoted values ("..." or '...') have their quotes stripped, which is how a
//     value with meaningful leading or trailing spaces is written.
//   - A repeated key takes the LAST value, matching how git and npm resolve one.
//
// What it deliberately does NOT do: subsections (`[a "b"]` is one section named
// literally `a "b"`), multi-line continuations, or type inference. Every value is
// a string; a caller that wants a number parses it, which is the honest shape
// when the format itself has no types.
var INI = Module{
	Name: "ini",
	Path: "encoding/ini",
	Doc:  "INI/properties config parsing and rendering (.npmrc, .gitconfig, .editorconfig).",
	Methods: []Method{
		{
			Name:    "parse",
			Doc:     "Parse INI text into {section: {key: value}}. Entries before the first [section] header are under the \"\" key, which is where a flat file like .npmrc puts everything. Values are always strings; a repeated key takes the last value.",
			Args:    []Arg{{Name: "source", Type: TypeString}},
			Returns: []Ret{{Type: TypeStringMapMap}},
			Impl:    INIParse,
		},
		{
			Name:    "stringify",
			Doc:     "Render {section: {key: value}} back to INI text. The \"\" section is written first with no header, then the rest sorted by name with their keys sorted, so the output is byte-stable and diffs cleanly.",
			Args:    []Arg{{Name: "sections", Type: TypeStringMapMap}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    INIStringify,
		},
	},
}

// INIParse parses INI text into a section -> key -> value map.
func INIParse(_ context.Context, source string) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	section := iniGlobalSection
	ensure := func(name string) map[string]string {
		if s, ok := out[name]; ok {
			return s
		}
		s := map[string]string{}
		out[name] = s
		return s
	}
	// The global section exists even in a file that opens with a header, so a
	// caller can read result[""] without checking whether the key is there.
	ensure(iniGlobalSection)

	for n, raw := range strings.Split(source, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			ensure(section)
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			// A bare word is not a key=value and not a comment. Naming the line
			// number matters: the alternative is silently dropping a setting the
			// caller believes it read.
			return nil, fmt.Errorf("ini.parse: line %d is neither a section, a comment, nor key=value: %q", n+1, line)
		}
		ensure(section)[strings.TrimSpace(key)] = iniUnquote(strings.TrimSpace(value))
	}
	return out, nil
}

// iniUnquote strips one matched pair of surrounding quotes, which is how a value
// with significant leading or trailing whitespace is written.
func iniUnquote(v string) string {
	if len(v) < 2 {
		return v
	}
	if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// INIStringify renders a section -> key -> value map back to INI text.
func INIStringify(_ context.Context, sections map[string]map[string]string) (string, error) {
	m := sections
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	// Sorted, and the global section first: Go randomizes map iteration, so
	// without this the same config renders in a different order every run and a
	// generated file never stops showing drift.
	sort.Strings(names)

	var b strings.Builder
	// write never fails: it only appends to a strings.Builder, whose own Write
	// methods are documented to always return a nil error.
	write := func(name string) {
		entries := m[name]
		if len(entries) == 0 {
			return
		}
		if name != iniGlobalSection {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "[%s]\n", name)
		}
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s=%s\n", k, entries[k])
		}
	}

	if _, has := m[iniGlobalSection]; has {
		write(iniGlobalSection)
	}
	for _, name := range names {
		if name == iniGlobalSection {
			continue
		}
		write(name)
	}
	return b.String(), nil
}
