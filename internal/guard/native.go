package guard

import (
	"path"
	"regexp"
	"strconv"
	"strings"
)

// A host's own search tools (a content search taking a regex, a file search taking a glob)
// are judged as the shell line they stand for, so the symbol-search and translation rules
// hold whichever way the search was asked. The input is read by shape, never by the host's
// tool name: a `pattern` with no command and no file path is a search, and the pattern
// itself says which kind.

// fileGlobRe is a pattern only a file glob spells: a wildcard leading a segment (`*.go`,
// `**/x`, `src/*.ts`). As a regex the leading `*` repeats nothing, so a content search
// would be rejected by its own tool.
var fileGlobRe = regexp.MustCompile(`(?:^|/)\*`)

// nativeSearchLine renders a host search tool's input as the rg or find line it stands
// for, or "" when the input is no search this guard reads.
func nativeSearchLine(input map[string]any) string {
	pattern := envelopeString(input, "pattern")
	if pattern == "" {
		return ""
	}
	if fileGlobRe.MatchString(pattern) && !strings.ContainsAny(pattern, `\|()^$+`) {
		return nativeGlobLine(pattern, envelopeString(input, "path"))
	}
	return nativeGrepLine(pattern, input)
}

// nativeGrepLine is rg with the flags the input names. Files-with-matches is the default
// output of the content search hosts ship, so it is -l unless content or count is asked.
func nativeGrepLine(pattern string, input map[string]any) string {
	argv := []string{"rg"}
	switch envelopeString(input, "output_mode") {
	case "content":
		argv = append(argv, "-n")
	case "count":
		argv = append(argv, "-c")
	default:
		argv = append(argv, "-l")
	}
	if input["-i"] == true {
		argv = append(argv, "-i")
	}
	for _, flag := range []string{"-A", "-B", "-C"} {
		if n, ok := input[flag].(float64); ok && n > 0 {
			argv = append(argv, flag, strconv.Itoa(int(n)))
		}
	}
	if g := envelopeString(input, "glob"); g != "" {
		argv = append(argv, "-g", g)
	}
	if t := envelopeString(input, "type"); t != "" {
		argv = append(argv, "-t", t)
	}
	argv = append(argv, "-e", pattern)
	if p := envelopeString(input, "path"); p != "" {
		argv = append(argv, p)
	}
	return shellLine(argv)
}

// nativeGlobLine is the find a glob stands for when its shape has one: `**/<name>` under
// any directory, `<dir>/**/<name>`, and `<dir>/<name>` one level down. Any other glob is
// rendered as a find -path, which no rule translates, so it runs.
func nativeGlobLine(glob, root string) string {
	if root == "" {
		root = "."
	}
	dir, name := path.Split(glob)
	dir = strings.TrimSuffix(dir, "/")
	if strings.ContainsAny(name, "/") || name == "" || name == "**" {
		return shellLine([]string{"find", root, "-path", glob})
	}
	switch {
	case dir == "**":
		return shellLine([]string{"find", root, "-type", "f", "-name", name})
	case strings.HasSuffix(dir, "/**") && !strings.ContainsAny(strings.TrimSuffix(dir, "/**"), "*?["):
		return shellLine([]string{"find", path.Join(root, strings.TrimSuffix(dir, "/**")), "-type", "f", "-name", name})
	case dir == "":
		return shellLine([]string{"find", root, "-maxdepth", "1", "-type", "f", "-name", name})
	case !strings.ContainsAny(dir, "*?["):
		return shellLine([]string{"find", path.Join(root, dir), "-maxdepth", "1", "-type", "f", "-name", name})
	}
	return shellLine([]string{"find", root, "-path", glob})
}

// shellLine joins argv into a line the shell parser reads back as the same words.
func shellLine(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && !strings.ContainsAny(a, " \t\n'\"\\$`|&;()<>*?[]{}!#~") {
			out[i] = a
			continue
		}
		out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(out, " ")
}
