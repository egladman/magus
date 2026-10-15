package guard

import (
	"path"
	"strconv"

	"github.com/egladman/magus/internal/hint"
)

// read-paging: a mapped file read window by window. read-navigation refuses the whole read
// and serves the file's map; a reader who pages through the file instead spends the whole
// read's lines over several calls, and learns the map from none of them.

const denyRuleReadPaging denyRuleName = "read-paging"

// readPagingRefused is the window of one file a caller is refused. Two windows are a read
// and a look around it; a third is a walk through the file.
const readPagingRefused = 3

// readPagingKind names the tally of one caller's windows of one file.
func readPagingKind(agent, rel string) hint.MarkerKind {
	return hint.MarkerKind("page-" + shortHash(agent) + "-" + shortHash(rel))
}

// readNavigationKind marks that one caller was refused a whole read of rel and served its
// map, so the same caller asking for the same file again is let through.
func readNavigationKind(agent, rel string) hint.MarkerKind {
	return hint.MarkerKind("whole-read-" + shortHash(agent) + "-" + shortHash(rel))
}

// gradeReadPaging counts each new window the line reads of a mapped file, and refuses the
// one that reaches readPagingRefused for its caller. A window inside one declaration or
// heading is a targeted read and never counts, and neither is a repeat of a window already
// read, a whole read (read-navigation's), or a window of a file short enough to read
// whole. Ungraded.
func gradeReadPaging(deps Dependencies, facts hint.Gate, agent, dir, command string, d Dialect) ShellVerdict {
	if deps.scope.root == "" || facts.CacheDir() == "" {
		return ShellVerdict{}
	}
	root, ok := resolvedRoot(deps.scope.root)
	if !ok {
		return ShellVerdict{}
	}
	nav, _ := deps.setting(string(denyRuleReadNavigation))
	for _, rc := range readCalls(command, d) {
		abs, rel, ok := workspacePath(root, dir, rc.path)
		if !ok || wholeReadFiles[path.Base(rel)] {
			continue
		}
		m, ok := buildFileMap(deps, abs, rel)
		if !ok || rc.whole(m.lines) || nav.Lines > 0 && m.lines <= nav.Lines {
			continue
		}
		first, last := rc.span(m.lines)
		if _, inside := m.covering(first, last); inside {
			continue
		}
		tally := readPagingKind(agent, rel)
		window := tally + hint.MarkerKind("-"+strconv.Itoa(first)+"-"+strconv.Itoa(last))
		if facts.AlreadyFired(window) {
			continue
		}
		n := facts.Count(tally)
		if n < readPagingRefused {
			facts.MarkFired(window)
			continue
		}
		// A refused window was never read, so it is not recorded as one: asked again, by
		// any tool, it is refused again.
		v := denyReadPaging(m, n)
		if deps.grade(v).Deny == "" {
			facts.MarkFired(window)
		}
		return v
	}
	return ShellVerdict{}
}

func denyReadPaging(m fileMap, window int) ShellVerdict {
	read := "`sed -n <first>,<last>p " + shellWord(m.rel) + "`"
	if m.indexed {
		read = "`" + hint.Refs.With("<name>", "--definition", "--source") + "`"
	}
	return ShellVerdict{
		Deny: "magus workspace: window " + strconv.Itoa(window) + " of " + m.rel + " pages through its " + strconv.Itoa(m.lines) + " lines; read one " + m.noun + ": " + read + ".",
		Why:  denyReadNavigation(m).Why,
		Rule: denyRule{Name: denyRuleReadPaging, Arg: m.rel},
	}
}
