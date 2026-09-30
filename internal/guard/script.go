package guard

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// A script is judged by its content, not by the line that launches it.
//
// Measured 2026-09-24: 122 scripts written to scratch directories ran 334 times, and the
// guard judged `python3 p3.py`, which is nothing, rather than p3.py, which was an
// `open(p).read().replace(a, b)` rewrite it denies 189 times inline. Others were retry
// loops, a `cd "$1"; exec "$@"` shim written right after a cd deny, and magus wrappers
// capturing output to files. The content is on disk, so judging it is provable.

// scriptJudgedRules are the inline rules a script's content answers to: the ones about
// how an agent drives magus and edits files. The rest stay inline-only, because a
// workspace's own committed scripts legitimately run raw tools and git.
var scriptJudgedRules = []denyRuleName{
	denyRuleBusyWait, denyRuleScriptedRewrite, denyRuleThrowawayCopy,
	denyRuleOutputPipe, denyRuleOutputRedirect, denyRuleUnknownEnv,
}

// maxScriptBytes bounds what is read. A program larger than this is not a scratch script.
const maxScriptBytes = 1 << 20

// scriptLang is how a script's content is judged.
type scriptLang int

const (
	scriptNone scriptLang = iota
	scriptShell
	// scriptInterpreter is python, perl, ruby or node: the scripted-rewrite rule is the
	// one inline rule that reads their programs.
	scriptInterpreter
	// scriptBuzz is a program `magus buzz` runs, judged by the files its fs writes replace.
	scriptBuzz
)

// denyScriptContent refuses a line that runs a script whose content would be refused
// typed inline. callDir resolves a relative script path; with none, only an absolute one
// is read.
func denyScriptContent(deps Dependencies, callDir, command string, d Dialect) ShellVerdict {
	for _, run := range scriptRuns(command, d) {
		file := run.path
		if !filepath.IsAbs(file) {
			if callDir == "" {
				continue
			}
			file = filepath.Join(callDir, file)
		}
		body, ok := readScript(file)
		if !ok {
			continue
		}
		lang := run.lang
		if lang == scriptNone {
			lang = langOf(file, body)
		}
		if v := judgeScript(deps, lang, body); v.Deny != "" {
			v.Deny += "\nJudged from the content of " + run.path + ", which this line runs: a script gets the verdict its lines would get typed inline."
			return v
		}
	}
	return ShellVerdict{}
}

// rankScriptContent fills a silence and outranks an advisory, but never replaces a deny
// the line earned on its own.
func rankScriptContent(v, script ShellVerdict) ShellVerdict {
	if script.Deny == "" || v.Deny != "" {
		return v
	}
	return script
}

// denyScriptWrite refuses a write that leaves a script refused typed inline, when the
// write is what introduced the refusal: editing a script that already carried one, for
// any other reason, is not blocked on a line it did not touch.
func denyScriptWrite(deps Dependencies, file string, w writeFields) ShellVerdict {
	file = strings.TrimSpace(file)
	// Every write passes through here, so a file whose extension names another language
	// is not read at all; only an extensionless one needs its shebang. A .buzz file is as
	// often a magusfile, spell or module that magus loads as a script someone runs, so it
	// is judged when `magus buzz` runs it.
	lang := langOf(file, w.Content)
	if file == "" || lang == scriptBuzz || (lang == scriptNone && filepath.Ext(file) != "") {
		return ShellVerdict{}
	}
	before, _ := readScript(file)
	after := w.Content
	if after == "" {
		if w.OldText == "" || !strings.Contains(before, w.OldText) {
			return ShellVerdict{}
		}
		after = strings.Replace(before, w.OldText, w.NewText, 1)
	}
	lang = langOf(file, after)
	v := judgeScript(deps, lang, after)
	if v.Deny == "" || judgeScript(deps, lang, before).Rule.Name == v.Rule.Name {
		return ShellVerdict{}
	}
	v.Deny += "\nJudged from what this write leaves in " + file + ": a script gets the verdict its lines would get typed inline."
	return v
}

// judgeScript is the inline verdict for a script's content, kept only when it is one of
// the rules a script answers to.
func judgeScript(deps Dependencies, lang scriptLang, body string) ShellVerdict {
	switch lang {
	case scriptShell:
		v := evaluateRules(deps, body, DialectBash)
		if v.Deny != "" && slices.Contains(scriptJudgedRules, v.Rule.Name) {
			return v
		}
	case scriptInterpreter:
		if scriptRewrites(body) && !allOutside(deps.scope, scriptPaths(body)) {
			return ShellVerdict{Deny: denyScriptedRewrite, Rule: denyRule{Name: denyRuleScriptedRewrite}}
		}
	case scriptBuzz:
		// The verdict an inline interpreter gets for replacing a file the tree carries: fs
		// in a Buzz script writes exactly what open(p, 'w') does in a python one.
		if rel := buzzRewrittenFile(deps.scope.root, body); rel != "" {
			return ShellVerdict{
				Deny: interpreterRewriteDenial(rel) + referenceScriptNote(deps.scope.root, denyRuleInterpreterRewrite),
				Rule: denyRule{Name: denyRuleInterpreterRewrite},
			}
		}
	}
	return ShellVerdict{}
}

func readScript(file string) (string, bool) {
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxScriptBytes {
		return "", false
	}
	body, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}
	return string(body), true
}

// langOf reads a script's language from its shebang, else its extension.
func langOf(file, body string) scriptLang {
	if first, _, _ := strings.Cut(body, "\n"); strings.HasPrefix(first, "#!") {
		fields := strings.Fields(strings.TrimPrefix(first, "#!"))
		if len(fields) > 0 {
			prog := path.Base(fields[0])
			if prog == "env" && len(fields) > 1 {
				prog = fields[len(fields)-1]
			}
			if l := langOfProgram(prog); l != scriptNone {
				return l
			}
		}
	}
	switch strings.ToLower(filepath.Ext(file)) {
	case ".sh", ".bash", ".zsh":
		return scriptShell
	case ".py", ".pl", ".rb", ".js", ".mjs", ".cjs":
		return scriptInterpreter
	case ".buzz":
		return scriptBuzz
	}
	return scriptNone
}

func langOfProgram(name string) scriptLang {
	switch {
	case shells[name]:
		return scriptShell
	case scriptedRewriteInterpreters[name]:
		return scriptInterpreter
	}
	return scriptNone
}

// scriptRun is one script a line runs, as the line names it.
type scriptRun struct {
	path string
	lang scriptLang // scriptNone defers to the file's shebang and extension
}

// scriptRuns names every script file the line would run: an interpreter or shell given a
// file operand, or a path executed directly. A `-c` payload is a script too, so it is
// parsed and its own runs collected.
func scriptRuns(command string, d Dialect) []scriptRun {
	return scriptRunsAt(command, d, 0)
}

func scriptRunsAt(command string, d Dialect, depth int) []scriptRun {
	if depth > writeScanDepth {
		return nil
	}
	f, err := parseFile(command, d)
	if err != nil {
		return nil
	}
	vars := map[string]string{}
	for _, m := range assignmentRe.FindAllStringSubmatch(command, -1) {
		vars[m[1]] = m[3]
	}
	var out []scriptRun
	syntax.Walk(f, func(n syntax.Node) bool {
		call, ok := n.(*syntax.CallExpr)
		if !ok {
			return true
		}
		words := make([]string, len(call.Args))
		for i, w := range call.Args {
			words[i] = scopeWord(command, w, vars)
		}
		if script, ok := shellPayload(literalWords(call.Args)); ok {
			out = append(out, scriptRunsAt(script, d, depth+1)...)
			return true
		}
		if run, ok := scriptRunOf(words); ok {
			out = append(out, run)
		}
		return true
	})
	return out
}

func scriptRunOf(words []string) (scriptRun, bool) {
	for len(words) > 0 {
		first := words[0]
		if strings.HasPrefix(first, unresolved) {
			return scriptRun{}, false
		}
		name := path.Base(first)
		switch {
		case shells[name] || scriptedRewriteInterpreters[name]:
			file, ok := scriptOperand(words[1:], shells[name])
			if !ok {
				return scriptRun{}, false
			}
			return scriptRun{path: file, lang: langOfProgram(name)}, true
		case wrappers[name] && name != "eval":
			words = skipWrapperArgs(name, words[1:])
		case name == "magus":
			file, ok := buzzScriptFile(words[1:])
			if !ok {
				return scriptRun{}, false
			}
			return scriptRun{path: file, lang: scriptBuzz}, true
		case strings.Contains(first, "/"):
			return scriptRun{path: first}, true
		default:
			return scriptRun{}, false
		}
	}
	return scriptRun{}, false
}

// buzzScriptFile is the file a `magus buzz` line runs. An -e snippet or stdin carries its
// program on the line, where denyInterpreterRewrite reads it; --check runs nothing.
func buzzScriptFile(args []string) (string, bool) {
	if len(args) == 0 || args[0] != "buzz" {
		return "", false
	}
	for i := 1; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-e" || a == "-" || a == "--" || a == "--check" || a == "lsp" || strings.HasPrefix(a, unresolved):
			return "", false
		case a == "-C":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a, true
		}
	}
	return "", false
}

// scriptOperand is the file an interpreter runs: its first operand, unless a flag says
// the program arrives some other way (inline, a module, stdin). A shell's -e is errexit
// and its -c payload was already taken, so for a shell only -o's option name is skipped.
func scriptOperand(args []string, shell bool) (string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-" || strings.HasPrefix(a, unresolved):
			return "", false
		case shell && (a == "-o" || a == "+o"):
			i++
		case !shell && (a == "-c" || a == "-e" || a == "-E" || a == "-m" || a == "--eval" || a == "-p"):
			return "", false
		case strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+"):
		default:
			return a, true
		}
	}
	return "", false
}
