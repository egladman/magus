package vcs

import (
	"regexp"
	"strings"
)

// projectCallOpen matches the top-level call that declares a magusfile's project, under
// whatever name the magus module was imported as.
var projectCallOpen = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\\project\(\{`)

// projectKey matches a quoted map key at the start of a trimmed line, capturing the key.
var projectKey = regexp.MustCompile(`^"((?:[^"\\]|\\.)*)"[ \t]*:`)

// placeProjectKeys names the lines of a magusfile's top-level project call, which git's
// funcname matching leaves to the declaration above it: `project` for the call's own lines,
// `project.<key>` for a top-level key, and `project.<key>.<entry>` for an entry of a key
// whose value is a map. A claim can then hold one target's settings
// (`magusfile.buzz#project.targets.test`) where the call is otherwise one declaration of the
// whole file. Lines after the call that git gave the declaration above it belong to the call
// too, up to the next declaration; every other line keeps what git named.
func placeProjectKeys(lines, decls []string) {
	start := -1
	for i, l := range lines {
		if projectCallOpen.MatchString(l) {
			start = i
			break
		}
	}
	if start < 0 {
		return
	}
	above := decls[start]
	decls[start] = "project"
	// base is the depth of the lines directly inside the call's map: `"targets": {`.
	base := bracketDepth(lines[start], 0)
	if base <= 0 {
		return
	}
	depth := base
	var key, entry string
	keyIsMap := false
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " \t")
		at := depth
		switch {
		case at == base && strings.HasPrefix(trimmed, "}"):
			key, entry = "", ""
		case at == base:
			if m := projectKey.FindStringSubmatch(trimmed); m != nil {
				key, entry = m[1], ""
				keyIsMap = strings.HasPrefix(strings.TrimLeft(trimmed[len(m[0]):], " \t"), "{")
			}
		case at == base+1 && key != "" && (strings.HasPrefix(trimmed, "}") || strings.HasPrefix(trimmed, "]")):
			entry = ""
		case at == base+1 && key != "" && keyIsMap:
			if m := projectKey.FindStringSubmatch(trimmed); m != nil {
				entry = m[1]
			}
		}
		decls[i] = projectDeclaration(key, entry)
		depth = bracketDepth(lines[i], depth)
		if depth <= 0 {
			for j := i + 1; j < len(decls) && decls[j] == above; j++ {
				decls[j] = "project"
			}
			return
		}
	}
}

func projectDeclaration(key, entry string) string {
	switch {
	case key == "":
		return "project"
	case entry == "":
		return "project." + key
	}
	return "project." + key + "." + entry
}

// bracketDepth is depth after line, counting (, [ and { as opening and their pairs as
// closing, outside strings and line comments. A string does not span lines here; one that
// does is rare enough in a project table that miscounting it costs only its own lines.
func bracketDepth(line string, depth int) int {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '/' && i+1 < len(line) && line[i+1] == '/':
			return depth
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		}
	}
	return depth
}
