package guard

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// The filter-without-input rule: a stdin reader nothing on the line feeds.
//
// Two halves, both structural. The walk decides whether anything SUPPLIES a call's stdin: a
// pipe into it, an input redirect on it or on any compound around it, a background `&`. The
// grammar decides whether the call READS stdin: each tool's flags are modeled well enough to
// tell a flag's value from an operand, and anything the table does not model leaves the call
// unclassified. Unclassified never fires, because the guard refuses only what it can prove.

// stdinOperands says what a reader's positionals after its program are.
type stdinOperands int

const (
	// A positional names a file to read, and `-` names stdin.
	operandsAreInput stdinOperands = iota
	// tr's sets and tee's outputs: the tool reads stdin whatever it is given.
	operandsNeverInput
	// xargs: the first positional starts the command it runs, so option parsing ends there.
	operandsAreCommand
	// ripgrep: with no path it searches the working directory unless stdin is a pipe or a
	// file, which the guard cannot see, so only an explicit `-` proves a read.
	operandsOrCwd
)

type flagKind int

const (
	flagUnknown flagKind = iota
	flagBool
	// A long flag that takes a value only after `=` (--color[=WHEN]).
	flagOptional
	flagValue
	// Takes the next two words (jq --arg NAME VALUE).
	flagPair
	// Takes a value that supplies the program, so no positional is the program.
	flagProgram
	// After it, stdin is not provably read: jq -n, xargs -a, awk -f (a BEGIN-only program
	// reads nothing).
	flagSettles
)

// stdinReader is one tool's operand grammar, enough of it to say whether a call names its
// input. Each flags entry is a space-separated list of spellings; a flag in no list leaves
// the call unclassified.
type stdinReader struct {
	flags map[flagKind]string
	// Leading positionals that are the program rather than input: grep's pattern, sed's script.
	program  int
	operands stdinOperands
	// Only the first maxInputs operands are read; uniq's second one is its OUTPUT.
	maxInputs int
	// -NUM is a count (head -5, grep -3).
	numeric bool
	// A `+cmd` word is an option (less +G).
	plusCommands bool
	// A NAME=value operand is an assignment, not a file (awk).
	assignments bool
	// Whether a literal program reads input at all; nil means it always does.
	readsInput func(program string) bool
}

func (r stdinReader) kind(flag string) flagKind {
	for k, spellings := range r.flags {
		if slices.Contains(strings.Fields(spellings), flag) {
			return k
		}
	}
	return flagUnknown
}

var grepReader = stdinReader{
	program: 1, numeric: true,
	flags: map[flagKind]string{
		flagBool: "-a -b -c -E -F -G -H -h -I -i -L -l -n -o -P -q -R -r -s -T -U -v -w -x -y -Z -z " +
			"--basic-regexp --binary --byte-offset --count --dereference-recursive --extended-regexp " +
			"--files-with-matches --files-without-match --fixed-strings --ignore-case --initial-tab " +
			"--invert-match --line-buffered --line-number --line-regexp --no-filename --no-ignore-case " +
			"--no-messages --null --null-data --only-matching --perl-regexp --quiet --recursive " +
			"--silent --text --with-filename --word-regexp",
		flagOptional: "--color --colour",
		flagValue: "-A -B -C -D -d -m --after-context --before-context --binary-files --context " +
			"--devices --directories --exclude --exclude-dir --exclude-from --group-separator " +
			"--include --label --max-count",
		flagProgram: "-e -f --file --regexp",
	},
}

var awkReader = stdinReader{
	program:     1,
	assignments: true,
	readsInput:  func(program string) bool { return !strings.Contains(program, "BEGIN") },
	flags: map[flagKind]string{
		flagBool:    "-b -c -P -r -S --characters-as-bytes --posix --re-interval --sandbox --traditional",
		flagValue:   "-F -v --assign --field-separator",
		flagSettles: "-e -f -i --file --include --source",
	},
}

// stdinReaders are the tools the rule judges, keyed by base name.
var stdinReaders = map[string]stdinReader{
	"grep":  grepReader,
	"egrep": grepReader,
	"fgrep": grepReader,
	"awk":   awkReader,
	"gawk":  awkReader,
	"rg": {
		program: 1, operands: operandsOrCwd,
		flags: map[flagKind]string{
			flagBool: "-0 -a -c -F -H -I -i -L -l -N -n -o -P -p -q -S -s -U -u -v -w -x -z " +
				"--case-sensitive --column --count --count-matches --files-with-matches " +
				"--files-without-match --fixed-strings --follow --heading --hidden --ignore-case " +
				"--invert-match --json --line-number --line-regexp --multiline --no-filename " +
				"--no-heading --no-ignore --no-line-number --null --only-matching --pcre2 --pretty " +
				"--quiet --search-zip --smart-case --stats --text --trim --vimgrep --with-filename " +
				"--word-regexp",
			flagValue: "-A -B -C -d -E -g -j -M -m -r -T -t --after-context --before-context --color " +
				"--colors --context --context-separator --encoding --glob --iglob --max-columns " +
				"--max-count --max-depth --max-filesize --replace --sort --sortr --threads --type " +
				"--type-add --type-not",
			flagProgram: "-e -f --file --regexp",
			flagSettles: "--files --type-list",
		},
	},
	"sed": {
		program: 1,
		flags: map[flagKind]string{
			flagBool: "-E -n -r -s -u -z --debug --follow-symlinks --null-data --posix --quiet " +
				"--regexp-extended --sandbox --separate --silent --unbuffered",
			flagValue:   "-l --line-length",
			flagProgram: "-e -f --expression --file",
			flagSettles: "-i --in-place",
		},
	},
	"jq": {
		program: 1,
		flags: map[flagKind]string{
			flagBool: "-a -C -c -e -j -M -R -r -S -s --ascii-output --color-output --compact-output " +
				"--exit-status --join-output --monochrome-output --raw-input --raw-output --raw-output0 " +
				"--seq --slurp --sort-keys --stream --stream-errors --tab --unbuffered",
			flagValue:   "-L --indent",
			flagPair:    "--arg --argjson --rawfile --slurpfile",
			flagProgram: "-f --from-file",
			// --args and --jsonargs turn the positionals after the filter into $ARGS.
			flagSettles: "-n --null-input --args --jsonargs",
		},
	},
	"head": {
		numeric: true,
		flags: map[flagKind]string{
			flagBool:  "-q -v -z --quiet --silent --verbose --zero-terminated",
			flagValue: "-c -n --bytes --lines",
		},
	},
	"tail": {
		numeric: true,
		flags: map[flagKind]string{
			flagBool:     "-F -f -q -r -v -z --quiet --retry --silent --verbose --zero-terminated",
			flagOptional: "--follow",
			flagValue:    "-b -c -n -s --bytes --lines --max-unchanged-stats --pid --sleep-interval",
		},
	},
	"sort": {
		flags: map[flagKind]string{
			flagBool: "-b -C -c -d -f -g -h -i -M -m -n -R -r -s -u -V -z --debug --dictionary-order " +
				"--general-numeric-sort --human-numeric-sort --ignore-case --ignore-leading-blanks " +
				"--ignore-nonprinting --merge --month-sort --numeric-sort --random-sort --reverse " +
				"--stable --unique --version-sort --zero-terminated",
			flagOptional: "--check",
			flagValue: "-k -o -S -T -t --batch-size --buffer-size --compress-program --field-separator " +
				"--key --output --parallel --random-source --sort --temporary-directory",
			flagSettles: "--files0-from",
		},
	},
	"uniq": {
		maxInputs: 1,
		flags: map[flagKind]string{
			flagBool:     "-c -D -d -i -u -z --count --ignore-case --repeated --unique --zero-terminated",
			flagOptional: "--all-repeated --group",
			flagValue:    "-f -s -w --check-chars --skip-chars --skip-fields",
		},
	},
	"wc": {
		flags: map[flagKind]string{
			flagBool:    "-c -L -l -m -w --bytes --chars --lines --max-line-length --words",
			flagValue:   "--total",
			flagSettles: "--files0-from",
		},
	},
	"cut": {
		flags: map[flagKind]string{
			flagBool: "-n -s -z --complement --only-delimited --zero-terminated",
			flagValue: "-b -c -d -f --bytes --characters --delimiter --fields " +
				"--output-delimiter",
		},
	},
	"tr": {
		program: 1, operands: operandsNeverInput,
		flags: map[flagKind]string{
			flagBool: "-C -c -d -s -t --complement --delete --squeeze-repeats --truncate-set1",
		},
	},
	"cat": {
		flags: map[flagKind]string{
			flagBool: "-A -b -E -e -l -n -s -T -t -u -v --number --number-nonblank --show-all " +
				"--show-ends --show-nonprinting --show-tabs --squeeze-blank",
		},
	},
	"tee": {
		operands: operandsNeverInput,
		flags: map[flagKind]string{
			flagBool:     "-a -i -p --append --ignore-interrupts",
			flagOptional: "--output-error",
		},
	},
	"xargs": {
		operands: operandsAreCommand,
		flags: map[flagKind]string{
			flagBool: "-0 -o -p -r -t -x --exit --interactive --no-run-if-empty --null --open-tty " +
				"--verbose",
			flagValue: "-d -E -I -J -L -n -P -R -S -s --delimiter --max-args --max-chars --max-procs " +
				"--process-slot-var",
			flagSettles: "-a --arg-file",
		},
	},
	"less": {
		plusCommands: true,
		flags: map[flagKind]string{
			flagBool: "-E -e -F -G -g -I -i -K -M -m -N -n -Q -q -R -r -S -s -X --chop-long-lines " +
				"--ignore-case --IGNORE-CASE --LINE-NUMBERS --no-init --quit-if-one-screen " +
				"--RAW-CONTROL-CHARS",
			flagValue:   "-b -h -j -P -p -x -y -z --pattern",
			flagSettles: "-t --tag",
		},
	},
	"more": {
		plusCommands: true, numeric: true,
		flags: map[flagKind]string{
			flagBool:  "-c -d -f -l -p -s -u",
			flagValue: "-n --lines",
		},
	},
}

// stdinWrappers run their payload with the stdin they were given. nohup is absent because it
// swaps a terminal stdin for an unreadable one, and sudo and setsid are absent because what
// they hand the payload is theirs to decide.
var stdinWrappers = map[string]bool{
	"command": true, "env": true, "exec": true, "nice": true, "stdbuf": true, "time": true, "timeout": true,
}

// unfedReader names the first stdin reader on the line that nothing feeds, the shape that
// hangs on a harness whose stdin never closes.
func unfedReader(command string, d Dialect) (string, bool) {
	f, err := parseFile(command, d)
	if err != nil || execRedirectsStdin(f) {
		return "", false
	}
	var stack []syntax.Node
	tool := ""
	syntax.Walk(f, func(n syntax.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		if call, ok := n.(*syntax.CallExpr); ok && tool == "" && !stdinSupplied(stack) {
			tool = starvedReader(call)
		}
		return true
	})
	return tool, tool != ""
}

// stdinSupplied reports whether anything around the innermost node of stack gives it a
// stdin other than the shell's own. A substitution inherits what its command was given,
// so the walk goes straight through one: `echo x | cmd $(cat)` feeds the cat.
//
// A redirect on the command itself counts, though bash expands `cmd $(cat) < f` before it
// opens f. Missing that hang is the direction that fails open.
func stdinSupplied(stack []syntax.Node) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		switch n := stack[i].(type) {
		case *syntax.Stmt:
			// A non-interactive shell runs a background job with /dev/null as its stdin.
			if n.Background || n.Coprocess || slices.ContainsFunc(n.Redirs, redirectsStdin) {
				return true
			}
		case *syntax.BinaryCmd:
			if (n.Op == syntax.Pipe || n.Op == syntax.PipeAll) && stack[i+1] == syntax.Node(n.Y) {
				return true
			}
		case *syntax.ProcSubst:
			if n.Op == syntax.CmdOut {
				return true
			}
		case *syntax.FuncDecl, *syntax.CoprocClause:
			// A function body is fed wherever it is called, which this line may not show.
			return true
		}
	}
	return false
}

func redirectsStdin(r *syntax.Redirect) bool {
	if r.N != nil {
		return r.N.Value == "0"
	}
	switch r.Op {
	case syntax.RdrIn, syntax.RdrInOut, syntax.DplIn, syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return true
	}
	return false
}

// execRedirectsStdin reports an `exec < f`, which feeds every command after it on the line.
func execRedirectsStdin(f *syntax.File) bool {
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		s, ok := n.(*syntax.Stmt)
		if !ok || found {
			return !found
		}
		call, ok := s.Cmd.(*syntax.CallExpr)
		if ok && len(call.Args) > 0 && call.Args[0].Lit() == "exec" && slices.ContainsFunc(s.Redirs, redirectsStdin) {
			found = true
		}
		return !found
	})
	return found
}

// starvedReader names the tool when call provably reads stdin, or returns "".
func starvedReader(call *syntax.CallExpr) string {
	words := peelStdinWrappers(call.Args)
	if len(words) == 0 {
		return ""
	}
	name, ok := staticWord(words[0])
	if !ok {
		return ""
	}
	name = path.Base(name)
	if r, ok := stdinReaders[name]; ok && r.readsStdin(words[1:]) {
		return name
	}
	return ""
}

func peelStdinWrappers(words []*syntax.Word) []*syntax.Word {
	for len(words) > 0 {
		name, ok := staticWord(words[0])
		if !ok || !stdinWrappers[path.Base(name)] {
			return words
		}
		args := literalWords(words[1:])
		// env -S hands its payload to no shell this parse can see.
		if _, split := envSplitString(args); split && path.Base(name) == "env" {
			return nil
		}
		rest := skipWrapperArgs(path.Base(name), args)
		words = words[len(words)-len(rest):]
	}
	return nil
}

// operand is one positional word; text is meaningful only when static.
type operand struct {
	text   string
	static bool
}

var awkAssignmentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// readsStdin reports whether a call with these arguments, given no stdin by its
// surroundings, reads stdin: every word classified and no operand naming its input.
func (r stdinReader) readsStdin(words []*syntax.Word) bool {
	var operands []operand
	programFromFlag, optionsDone := false, false
	for i := 0; i < len(words); i++ {
		w := words[i]
		text, static := staticWord(w)
		if !static {
			if mayFieldSplit(w) {
				return false
			}
			// A quoted expansion is one word, but its value may begin with `-`.
			if !optionsDone && !leadsWithPlainText(w) {
				return false
			}
			operands = append(operands, operand{})
			optionsDone = optionsDone || r.operands == operandsAreCommand
			continue
		}
		switch {
		case optionsDone || !isOptionWord(text, r.plusCommands):
			operands = append(operands, operand{text: text, static: true})
			optionsDone = optionsDone || r.operands == operandsAreCommand
			continue
		case text == "--":
			optionsDone = true
			continue
		case text[0] == '+':
			continue
		}
		consumed, program, ok := r.option(text, words[i+1:])
		if !ok {
			return false
		}
		i += consumed
		programFromFlag = programFromFlag || program
	}

	program := r.program
	if programFromFlag {
		program = 0
	}
	if len(operands) < program {
		return false // a usage error, not a read
	}
	if r.readsInput != nil && (program == 0 || !operands[0].static || !r.readsInput(operands[0].text)) {
		return false
	}
	if r.operands == operandsNeverInput || r.operands == operandsAreCommand {
		return true
	}
	inputs := operands[program:]
	if r.assignments {
		inputs = slices.DeleteFunc(slices.Clone(inputs), func(o operand) bool {
			return o.static && awkAssignmentRe.MatchString(o.text)
		})
	}
	if r.maxInputs > 0 && len(inputs) > r.maxInputs {
		inputs = inputs[:r.maxInputs]
	}
	if len(inputs) == 0 {
		return r.operands == operandsAreInput
	}
	return slices.ContainsFunc(inputs, func(o operand) bool { return o.static && o.text == "-" })
}

// option classifies one option word and reports how many following words it consumes as
// values, and whether it supplied the program. ok is false for a flag the grammar does not
// model and for one that settles stdin.
func (r stdinReader) option(word string, rest []*syntax.Word) (consumed int, program, ok bool) {
	takes := func(n int, program bool) (int, bool, bool) {
		if len(rest) < n || slices.ContainsFunc(rest[:n], mayFieldSplit) {
			return 0, false, false
		}
		return n, program, true
	}
	if strings.HasPrefix(word, "--") {
		name, _, inline := strings.Cut(word, "=")
		switch k := r.kind(name); {
		case k == flagOptional, k == flagBool && !inline:
			return 0, false, true
		case (k == flagValue || k == flagProgram) && inline:
			return 0, k == flagProgram, true
		case k == flagValue || k == flagProgram:
			return takes(1, k == flagProgram)
		case k == flagPair && !inline:
			return takes(2, false)
		}
		return 0, false, false
	}
	for j := 1; j < len(word); j++ {
		switch k := r.kind("-" + word[j:j+1]); {
		case k == flagBool:
			continue
		case k == flagValue || k == flagProgram:
			// The rest of the cluster is the value (`-A3`), or the next word is.
			if j+1 < len(word) {
				return 0, k == flagProgram, true
			}
			return takes(1, k == flagProgram)
		case k == flagUnknown && r.numeric && word[j] >= '0' && word[j] <= '9':
			continue
		}
		return 0, false, false
	}
	return 0, false, true
}

func isOptionWord(text string, plusCommands bool) bool {
	return len(text) > 1 && (text[0] == '-' || plusCommands && text[0] == '+')
}

// staticWord is a word's text when no expansion is involved in it.
func staticWord(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

// mayFieldSplit reports an unquoted expansion, which can become several words or none, so
// no position after it is known.
func mayFieldSplit(w *syntax.Word) bool {
	return slices.ContainsFunc(w.Parts, func(p syntax.WordPart) bool {
		switch p.(type) {
		case *syntax.Lit, *syntax.SglQuoted, *syntax.DblQuoted:
			return false
		}
		return true
	})
}

// leadsWithPlainText reports a word whose first character is literal and is not a dash,
// so whatever it expands to is an operand rather than an option.
func leadsWithPlainText(w *syntax.Word) bool {
	if len(w.Parts) == 0 {
		return false
	}
	first := w.Parts[0]
	if dq, ok := first.(*syntax.DblQuoted); ok {
		if len(dq.Parts) == 0 {
			return false
		}
		first = dq.Parts[0]
	}
	var lead string
	switch p := first.(type) {
	case *syntax.Lit:
		lead = p.Value
	case *syntax.SglQuoted:
		lead = p.Value
	}
	return lead != "" && lead[0] != '-' && lead[0] != '+'
}
