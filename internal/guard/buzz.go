package guard

import (
	"path"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
)

// buzzWriteSkill teaches Buzz. Nothing else does, and that is the whole argument.
//
// Buzz is not in any model's weights. A model writing it pattern-matches from Go, Python or
// JavaScript and produces something shaped plausibly wrong, which is worse than obviously
// wrong because a chunk of it parses. Measured in one session, by an agent with this
// codebase open the whole time:
//
//   - fs\glob returns [Path], not [str], and the result was indexed as strings
//   - a list needs `mut` before .append, and the declaration had `var` alone
//   - the ternary form is rejected outside --embedded, where upstream-strict parsing applies
//   - archive\extract does not exist; the module offers uncompress and readFile
//   - `import "fs"` was simply missing
//   - strings are BYTE-indexed, so .sub produced a digest that matched nothing
//
// Six, all of them facts that reading first would have supplied. None was a typo.
var buzzWriteSkill = agent.MustSkill("magus-buzz-write")

// denyBuzzWriteWithoutSkill is the verdict for the first write to a .buzz file in a session
// that has not read the Buzz skill, or "" otherwise.
//
// WRITES only. Reading Buzz is how the language gets learned, the guard already observes
// every Read, and a rule that fired there would fire on every spell and every magusfile
// until the reader routed around it. A read also cannot corrupt anything: every error above
// came from writing on an assumption, not from looking.
//
// The extension is the whole test. Buzz lives in magusfiles, spells and tools/*.buzz, and
// the one thing they share is the suffix; keying on a directory would miss a workspace that
// puts its spells somewhere else, which is exactly the shape magus-workspace-rules invites.
func denyBuzzWriteWithoutSkill(markers hint.Gate, observesSkillLoads bool, workspace, writePath string) string {
	if !isBuzzSource(writePath) {
		return ""
	}
	return denyUntilSkillLoaded(markers, observesSkillLoads, workspace, buzzWriteSkill,
		"writing Buzz",
		"No model has Buzz in its weights, so the shape it guesses from Go or TypeScript parses just often enough to ship a bug.")
}

// isBuzzSource reports whether a path is Buzz source.
//
// Case-folded because a host reports whatever the filesystem handed it, and a
// case-insensitive volume will echo back the spelling the user typed rather than the one on
// disk. The extension is checked on the base name so a directory called .buzz cannot make
// every file under it read as source.
func isBuzzSource(writePath string) bool {
	return strings.EqualFold(path.Ext(path.Base(writePath)), ".buzz")
}

// buzzAuthoredBy names what a command line is about to author in Buzz, or "" when it
// authors none.
//
// The write rule above sees a host's write tool. This sees the same act arriving as a
// COMMAND, which is the road `magus buzz -e` and a heredoc take, and which that rule is
// structurally blind to: there is no write tool in the envelope to inspect. Both spellings
// author the language nothing has taught this session yet, so both answer to the skill.
//
// `magus buzz -t <file>` and a bare `magus buzz <file>` deliberately do NOT fire. Running
// Buzz that already exists is reading it, and the write rule's reasoning applies unchanged:
// looking is how the language gets learned, and every mistake the skill prevents came from
// writing on an assumption.
func buzzAuthoredBy(command string, d Dialect) string {
	cmds, parsed := ParseCommandsDialect(command, d)
	if !parsed {
		return ""
	}
	for _, c := range cmds {
		if path.Base(c.Name) != "magus" || len(c.Args) == 0 || c.Args[0] != "buzz" {
			continue
		}
		if hasFlag(c.Args, 'e', "eval") {
			return "an inline program"
		}
	}
	for _, target := range redirectTargets(command, 0, d) {
		if isBuzzSource(target) {
			return target
		}
	}
	return ""
}

// denyBuzzAuthorWithoutSkill is buzz-unbriefed for a command line rather than a file write.
func denyBuzzAuthorWithoutSkill(markers hint.Gate, observesSkillLoads bool, workspace, command string, d Dialect) string {
	if buzzAuthoredBy(command, d) == "" {
		return ""
	}
	return denyUntilSkillLoaded(markers, observesSkillLoads, workspace, buzzWriteSkill,
		"writing Buzz",
		"No model has Buzz in its weights, so the shape it guesses from Go or TypeScript parses just often enough to ship a bug.")
}

// rankBuzzAuthor ranks the reason UNDER every other deny, matching the write path's
// ordering: the rules above answer whether this agent may run the line at all, and there
// is nothing to learn before a command that is refused anyway.
func rankBuzzAuthor(v ShellVerdict, reason string) ShellVerdict {
	if reason == "" || v.Deny != "" {
		return v
	}
	return ShellVerdict{Deny: reason, Rule: denyRule{Name: denyBuzzUnbriefed}}
}
