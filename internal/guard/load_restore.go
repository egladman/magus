package guard

import (
	"cmp"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

// namedImport picks the import path out of a BZZ2001 message.
var namedImport = regexp.MustCompile(`import "([^"]+)"`)

// namedImports are the import paths loadFailure says could not be resolved, in the order
// the message names them. A chain of imports names each file on the way to the missing one.
func namedImports(loadFailure error) []string {
	if loadFailure == nil {
		return nil
	}
	text := loadFailure.Error()
	if !strings.Contains(text, string(buzz.UnresolvedImport)) {
		return nil
	}
	var out []string
	for _, m := range namedImport.FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	return out
}

// namesImport reports whether rel, a slash path inside the workspace, is the file an
// import path stands for. The message carries the path as the importing file wrote it, not
// the importer, so a file matches when its path ends with the import's, at a directory
// boundary.
func namesImport(imports []string, rel string) bool {
	return slices.ContainsFunc(imports, func(imp string) bool {
		clean := path.Clean(imp)
		for strings.HasPrefix(clean, "../") {
			clean = strings.TrimPrefix(clean, "../")
		}
		file := strings.TrimPrefix(clean, "./") + ".buzz"
		return rel == file || strings.HasSuffix(rel, "/"+file)
	})
}

// workspaceRel is p as a slash path inside root, resolved against cwd when relative.
// A glob never names one file, and a path outside root names no workspace file.
func workspaceRel(p, cwd, root string) (string, bool) {
	if p == "" || strings.ContainsAny(p, "*?[") {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	rel, err := filepath.Rel(root, filepath.Clean(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// restoreTargets are the paths a git line puts files back at: the destination of `git mv`,
// the paths of `git restore`, and the paths after `--` in `git checkout`. nil for any other
// line, and for a flag outside the few that only choose where the content comes from.
func restoreTargets(words []string) []string {
	if len(words) < 3 || path.Base(words[0]) != "git" {
		return nil
	}
	args := words[2:]
	switch words[1] {
	case "mv":
		if len(args) != 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
			return nil
		}
		return args[1:]
	case "restore":
		var paths []string
		for i := 0; i < len(args); i++ {
			switch a := args[i]; {
			case a == "--":
				return append(paths, args[i+1:]...)
			case a == "--staged", a == "-S", a == "--worktree", a == "-W", strings.HasPrefix(a, "--source="):
			case a == "-s":
				i++
			case strings.HasPrefix(a, "-"):
				return nil
			default:
				paths = append(paths, a)
			}
		}
		return paths
	case "checkout":
		i := slices.Index(args, "--")
		if i < 0 || i > 1 || (i == 1 && strings.HasPrefix(args[0], "-")) {
			return nil
		}
		return args[i+1:]
	}
	return nil
}

// loadRestore says why call is allowed while the guard policy does not load, "" when it is
// not: the call puts back a file loadFailure names as an import that cannot be resolved,
// which is the one change that lets the tree load again without a rebuild. Every path the
// call writes must be named, so a restore cannot carry another write with it.
// git restore and checkout may also put back any file the error cites.
func loadRestore(call judgedCall, loadFailure error, at location) string {
	if loadFailure == nil || at.workspace == "" {
		return ""
	}
	imports := namedImports(loadFailure)
	cwd := cmp.Or(at.dir, at.workspace)
	var targets []string
	gitRestore := false
	switch call.seam {
	case seamWrite:
		targets = []string{call.path}
	case seamCommand:
		sole, ok := soleCall(call.command, call.dialect)
		if !ok || len(sole.Assigns) > 0 || dynamicArgs(sole) {
			return ""
		}
		words := literalWords(sole.Args)
		targets = restoreTargets(words)
		gitRestore = len(targets) > 0 && (words[1] == "restore" || words[1] == "checkout")
	}
	if len(targets) == 0 {
		return ""
	}
	rels := make([]string, len(targets))
	allImports := true
	for i, target := range targets {
		rel, ok := workspaceRel(target, cwd, at.workspace)
		if !ok {
			return ""
		}
		switch {
		case namesImport(imports, rel):
		case gitRestore && namesFile(loadFailure.Error(), at.workspace, rel):
			allImports = false
		default:
			return ""
		}
		rels[i] = rel
	}
	why := "which the load error names"
	if allImports {
		why += " as an import it cannot resolve"
	}
	return fmt.Sprintf("%s puts back %s, %s. It runs although the guard policy does not load, because restoring that file is how the tree loads again.", call.what, strings.Join(rels, ", "), why)
}

// namesFile reports whether text cites rel as a location,
// `rel:` or `<root>/rel:`. A path that only ends in rel does
// not count, so a restore reaches no file the error never named.
func namesFile(text, root, rel string) bool {
	if strings.Contains(text, filepath.Join(root, filepath.FromSlash(rel))+":") {
		return true
	}
	for at := 0; ; {
		i := strings.Index(text[at:], rel+":")
		if i < 0 {
			return false
		}
		i += at
		if i == 0 || strings.ContainsRune(" \t\n\"'`(", rune(text[i-1])) {
			return true
		}
		at = i + 1
	}
}
