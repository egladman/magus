package guard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// denyRuleDeviationAuthor refuses an agent adding a line to a gate's ledger.
const denyRuleDeviationAuthor denyRuleName = "deviation-author"

// deviationLedgerSuffix names a gate's ledger of permitted findings.
const deviationLedgerSuffix = ".deviations.jsonl"

func isDeviationLedger(path string) bool {
	return strings.HasSuffix(strings.TrimSpace(path), deviationLedgerSuffix)
}

func deviationDenial(path string) string {
	return denial{
		Say: fmt.Sprintf("magus workspace: only a person adds a line to %s.", strings.TrimSpace(path)),
		Why: ruleDocDeviationAuthor.Why,
	}.full()
}

// denyDeviationWrite is the file-write door: the reason a write adds a ledger
// line, or "". It keys on no lease, so a root session is refused too.
func denyDeviationWrite(path string, f writeFields) string {
	if !isDeviationLedger(path) || !addsDeviationLine(path, f) {
		return ""
	}
	return deviationDenial(path)
}

// addsDeviationLine reports a line the write leaves more copies of than it
// found. A write it cannot read counts as adding.
func addsDeviationLine(path string, f writeFields) bool {
	added := append(make([]string, 0, 2+len(f.Edits)), f.Content, f.NewText)
	removed := append(make([]string, 0, 1+len(f.Edits)), f.OldText)
	for _, e := range f.Edits {
		added, removed = append(added, e.NewText), append(removed, e.OldText)
	}
	newText, oldText := strings.Join(added, "\n"), strings.Join(removed, "\n")
	if strings.TrimSpace(newText) == "" && strings.TrimSpace(oldText) == "" {
		return true
	}
	if f.Content != "" {
		old, err := os.ReadFile(strings.TrimSpace(path))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return true
		}
		oldText = string(old)
	}
	seen := map[string]int{}
	for line := range strings.Lines(oldText) {
		seen[strings.TrimSpace(line)]++
	}
	for line := range strings.Lines(newText) {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		if seen[text] == 0 {
			return true
		}
		seen[text]--
	}
	return false
}

// denyDeviationCommand is the shell door. A line does not show what it writes,
// so any write to a ledger counts as adding, except a remove. A merge or a
// revert names no target: it replays lines a commit holds.
func denyDeviationCommand(callDir, line string, d Dialect) string {
	if target := deviationLineTarget(line, d, callDir); target != "" {
		return deviationDenial(target)
	}
	for _, program := range inlineBuzzPrograms(line, d) {
		if target := buzzDeviationWrite(program); target != "" {
			return deviationDenial(target)
		}
	}
	for _, run := range scriptRuns(line, d) {
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
			lang = detectScriptLang(file, body)
		}
		target := ""
		switch lang {
		case scriptBuzz:
			target = buzzDeviationWrite(body)
		case scriptShell:
			target = deviationLineTarget(body, DialectBash, callDir)
		}
		if target != "" {
			return deviationDenial(target)
		}
	}
	return ""
}

// deviationLineTarget is the first ledger the line writes or patches. A patch
// the guard cannot read names no ledger: refusing it would charge every `git
// diff | git apply` a session runs.
func deviationLineTarget(line string, d Dialect, dir string) string {
	if target := deviationWriteTarget(line, d); target != "" {
		return target
	}
	writes, _ := patchWrites(line, 0, d, dir, userHome())
	for _, w := range writes {
		if isDeviationLedger(w.word) {
			return w.word
		}
	}
	return ""
}

// deviationWriteTarget is the first ledger the line writes and does not remove.
func deviationWriteTarget(line string, d Dialect) string {
	removed := map[string]int{}
	cmds, _ := ParseCommandsDialect(line, d)
	for _, c := range cmds {
		name := path.Base(c.Name)
		isRemove := name == "rm" || name == "rmdir"
		if !isRemove {
			_, isVCS := vcsPathOperands(name, c.Args)
			isRemove = isVCS && (slices.Contains(c.Args, "rm") || slices.Contains(c.Args, "remove"))
		}
		if isRemove {
			for _, op := range operands(c.Args, "") {
				removed[op]++
			}
		}
	}
	for _, target := range writeTargetCandidates(line, d) {
		if !isDeviationLedger(target) {
			continue
		}
		if removed[target] > 0 {
			removed[target]--
			continue
		}
		return target
	}
	return ""
}

// buzzDeviationCallRe finds the fs members that can put text at a path. The
// group is the argument the destination is: a copy names its source first.
var buzzDeviationCallRe = regexp.MustCompile(`\bfs\\(?:(writeFileAtomic|writeFile|writeLines|appendFile)|(copyFile|copyDir|rename|symlink))\s*\(`)

// buzzDeviationWrite is the first ledger a Buzz program writes at a destination
// its text spells.
func buzzDeviationWrite(program string) string {
	for _, m := range buzzDeviationCallRe.FindAllStringSubmatchIndex(program, -1) {
		args, ok := callArguments(program, m[1]-1)
		if !ok {
			continue
		}
		parts := splitArguments(args)
		at := 0
		if m[4] >= 0 {
			at = 1
		}
		if at >= len(parts) {
			continue
		}
		dest := parts[at]
		if label := buzzLabelRe.FindString(dest); label != "" {
			dest = dest[len(label):]
		}
		for _, literal := range destinationLiterals(program, dest) {
			if isDeviationLedger(literal) {
				return literal
			}
		}
	}
	return ""
}

// rankDeviationWrite outranks another deny, which would send the reader to a
// spelling this rule refuses too.
func rankDeviationWrite(v ShellVerdict, reason string) ShellVerdict {
	if reason == "" {
		return v
	}
	return ShellVerdict{Deny: reason, Rule: denyRule{Name: denyRuleDeviationAuthor}}
}
