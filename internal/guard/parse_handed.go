package guard

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
)

// handedScript is a shell script a statement hands over in a way shellPayload
// does not read: on a shell's stdin, or inside another language's program.
// read is false when the text is computed as the line runs.
type handedScript struct {
	stmt *syntax.Stmt
	to   string
	via  string
	text string
	read bool
}

// handedScripts are the scripts f's statements hand over, in walk order.
func handedScripts(f *syntax.File, d Dialect) []handedScript {
	piped := map[*syntax.Stmt]*syntax.Stmt{}
	var out []handedScript
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.BinaryCmd:
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				piped[n.Y] = lastInPipe(n.X)
			}
		case *syntax.Stmt:
			call, ok := n.Cmd.(*syntax.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			words := literalWords(call.Args)
			// xargs spends its stdin on arguments, so what it runs reads none.
			fromStdin := path.Base(words[0]) != "xargs"
			stdin := func() (scriptText, bool) {
				if !fromStdin {
					return scriptText{}, false
				}
				return stdinText(n, piped[n])
			}
			for _, inv := range peelWrappers(words, d) {
				for _, h := range programScripts(inv, stdin) {
					h.stmt = n
					out = append(out, h)
				}
			}
		}
		return true
	})
	return out
}

// unreadHandoffs names each script the line hands over in a form the guard
// cannot read, nested scripts included.
func unreadHandoffs(command string, d Dialect) []string {
	var out []string
	eachScript(command, d, func(sf *scriptFile) bool {
		for _, h := range handedScripts(sf.file, d) {
			if !h.read {
				out = append(out, fmt.Sprintf("%s through %s", h.to, h.via))
			}
		}
		return true
	})
	return out
}

// handedDialect is the dialect a handed script runs under: a shell's own, and
// /bin/sh for the shell another language starts.
func handedDialect(h handedScript, d Dialect) Dialect {
	if shells[h.to] {
		return wrapperShellDialect(h.to, d)
	}
	return wrapperShellDialect("sh", d)
}

func lastInPipe(st *syntax.Stmt) *syntax.Stmt {
	for {
		b, ok := st.Cmd.(*syntax.BinaryCmd)
		if !ok || (b.Op != syntax.Pipe && b.Op != syntax.PipeAll) {
			return st
		}
		st = b.Y
	}
}

// scriptText is a program's text and whether the guard can read it.
type scriptText struct {
	text string
	read bool
	via  string
}

// stdinText is the program st reads on stdin, false when none is handed
// over. A file redirected in is a script file, which is not one.
func stdinText(st, piped *syntax.Stmt) (scriptText, bool) {
	for _, r := range st.Redirs {
		switch r.Op {
		case syntax.WordHdoc:
			return scriptText{literalWord(r.Word.Parts) + "\n", readsAsWritten(r.Word.Parts), "a here-string"}, true
		case syntax.Hdoc, syntax.DashHdoc:
			return heredocScript(r), true
		case syntax.RdrIn:
			for _, p := range r.Word.Parts {
				if _, ok := p.(*syntax.ProcSubst); ok {
					return scriptText{via: "a process substitution"}, true
				}
			}
		}
	}
	if piped == nil {
		return scriptText{}, false
	}
	return pipedText(piped), true
}

// heredocScript reads a heredoc whose body has no expansion, which a quoted
// delimiter guarantees.
func heredocScript(r *syntax.Redirect) scriptText {
	out := scriptText{read: true, via: "a heredoc"}
	if r.Hdoc == nil {
		return out
	}
	var b strings.Builder
	for _, p := range r.Hdoc.Parts {
		lit, ok := p.(*syntax.Lit)
		if !ok {
			return scriptText{via: "a heredoc that expands"}
		}
		b.WriteString(lit.Value)
	}
	out.text = b.String()
	return out
}

// pipedText is what src writes into a pipe, readable only for an echo, a
// printf or a cat of a heredoc spelled from literals.
func pipedText(src *syntax.Stmt) scriptText {
	unread := scriptText{via: "a pipe"}
	call, ok := src.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return unread
	}
	for _, w := range call.Args {
		if !readsAsWritten(w.Parts) {
			return unread
		}
	}
	words := literalWords(call.Args)
	args := words[1:]
	switch path.Base(words[0]) {
	case "echo":
		escapes := false
		for len(args) > 0 && (args[0] == "-n" || args[0] == "-e" || args[0] == "-E" || args[0] == "-ne" || args[0] == "-en") {
			escapes = escapes || strings.Contains(args[0], "e")
			args = args[1:]
		}
		text := strings.Join(args, " ")
		if escapes && strings.Contains(text, `\`) {
			return unread
		}
		return scriptText{text + "\n", true, "a pipe"}
	case "printf":
		if len(args) != 1 {
			return unread
		}
		if text, ok := printfLiteral(args[0]); ok {
			return scriptText{text, true, "a pipe"}
		}
	case "cat":
		if len(args) == 0 {
			if t, ok := stdinText(src, nil); ok {
				t.via = "a pipe"
				return t
			}
		}
	}
	return unread
}

// printfLiteral is a printf format with no conversion, its \n, \t and \\
// escapes applied.
func printfLiteral(format string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '\\' && c != '%' {
			b.WriteByte(c)
			continue
		}
		if i+1 == len(format) {
			return "", false
		}
		i++
		switch {
		case c == '%' && format[i] == '%':
			b.WriteByte('%')
		case c == '\\' && format[i] == 'n':
			b.WriteByte('\n')
		case c == '\\' && format[i] == 't':
			b.WriteByte('\t')
		case c == '\\' && format[i] == '\\':
			b.WriteByte('\\')
		default:
			return "", false
		}
	}
	return b.String(), true
}

// programScripts are the shell scripts inv runs other than by -c. A shell
// reads one on stdin. Each language below has its own way to start one.
func programScripts(inv hint.Invocation, stdin func() (scriptText, bool)) []handedScript {
	call := programCall{name: path.Base(inv.Name), args: inv.Args, stdin: stdin}
	name := call.name
	switch {
	case shells[name]:
		return call.shellScripts()
	case name == "tclsh" || name == "wish":
		if ops := operands(call.args, ""); len(ops) > 0 && ops[0] != "-" {
			return nil
		}
		return call.fromStdin(tclScripts)
	case name == "awk" || name == "gawk" || name == "mawk" || name == "nawk":
		return call.inlinePrograms("source", "Fvf", awkScripts)
	case name == "sed" || name == "gsed":
		return call.inlinePrograms("expression", "efl", sedScripts)
	case name == "php" || strings.HasPrefix(name, "php") && isDigits(strings.TrimPrefix(name, "php")):
		return call.phpProgramScripts()
	case name == "osascript":
		return call.appleProgramScripts()
	case name == "vim" || name == "vi" || name == "nvim" || name == "ex" || name == "view":
		return call.exCommandScripts()
	}
	return nil
}

// programCall is one interpreter's argv and the stdin its statement hands it.
type programCall struct {
	name  string
	args  []string
	stdin func() (scriptText, bool)
}

// fromStdin runs lang over the program on the call's stdin, or names it
// unread.
func (c programCall) fromStdin(lang func(string, string) []handedScript) []handedScript {
	t, ok := c.stdin()
	if !ok {
		return nil
	}
	if !t.read {
		return []handedScript{{to: c.name, via: t.via}}
	}
	return lang(c.name, t.text)
}

// shellScripts is the script a shell reads on stdin: none when -c or a
// script file gives it one.
func (c programCall) shellScripts() []handedScript {
	if _, ok := shellDashC(c.args); ok {
		return nil
	}
	if ops := operands(c.args, "oO"); len(ops) > 0 && ops[0] != "-" && !hasFlag(c.args, 's', "") {
		return nil
	}
	return c.fromStdin(func(to, text string) []handedScript {
		return []handedScript{{to: to, via: "stdin", text: text, read: true}}
	})
}

// inlinePrograms runs lang over each -e or --long program, else the first
// operand, as awk and sed take theirs. valued names the short flags that
// take a value. A program read with -f is not inline.
func (c programCall) inlinePrograms(long, valued string, lang func(string, string) []handedScript) []handedScript {
	if hasFlag(c.args, 'f', "file") {
		return nil
	}
	programs := flagValues(c.args, "e", long)
	if len(programs) == 0 {
		if ops := operands(c.args, valued); len(ops) > 0 {
			programs = ops[:1]
		}
	}
	return eachProgram(c.name, programs, lang)
}

func (c programCall) phpProgramScripts() []handedScript {
	if code := flagValues(c.args, "rBRE", ""); len(code) > 0 {
		return eachProgram(c.name, code, phpScripts)
	}
	if ops := operands(c.args, "cdzf"); len(ops) > 0 && ops[0] != "-" {
		return nil
	}
	return c.fromStdin(phpScripts)
}

func (c programCall) appleProgramScripts() []handedScript {
	if lines := flagValues(c.args, "e", ""); len(lines) > 0 {
		return appleScripts(c.name, strings.Join(lines, "\n"))
	}
	if ops := operands(c.args, "elis"); len(ops) > 0 && ops[0] != "-" {
		return nil
	}
	return c.fromStdin(appleScripts)
}

// exCommandScripts are the scripts Vim's -c and + commands run, and an Ex
// script on stdin.
func (c programCall) exCommandScripts() []handedScript {
	commands := flagValues(c.args, "c", "cmd")
	for _, a := range c.args {
		if len(a) > 1 && a[0] == '+' {
			commands = append(commands, a[1:])
		}
	}
	out := eachProgram(c.name, commands, exScripts)
	if c.name == "ex" || hasFlag(c.args, 'e', "") {
		out = append(out, c.fromStdin(func(to, text string) []handedScript {
			return eachProgram(to, strings.Split(text, "\n"), exScripts)
		})...)
	}
	return out
}

func isDigits(s string) bool {
	_, err := strconv.Atoi(s)
	return s != "" && err == nil
}

func eachProgram(name string, programs []string, lang func(string, string) []handedScript) []handedScript {
	out := make([]handedScript, 0, len(programs))
	for _, p := range programs {
		out = append(out, lang(name, p)...)
	}
	return out
}

// flagValues are the values of the short flags in shorts and of long. A
// value is attached or the next word, as getopt reads a cluster.
func flagValues(args []string, shorts, long string) []string {
	var out []string
	valued := fmt.Sprintf("--%s=", long)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return out
		case long != "" && strings.HasPrefix(a, valued):
			out = append(out, strings.TrimPrefix(a, valued))
		case long != "" && a == "--"+long:
			if i+1 < len(args) {
				out = append(out, args[i+1])
				i++
			}
		case len(a) > 1 && a[0] == '-' && a[1] != '-':
			for j := 1; j < len(a); j++ {
				if !strings.ContainsRune(shorts, rune(a[j])) {
					continue
				}
				if j < len(a)-1 {
					out = append(out, a[j+1:])
				} else if i+1 < len(args) {
					out = append(out, args[i+1])
					i++
				}
				break
			}
		}
	}
	return out
}

// literal is a script found spelled out; computed is one that is not.
func literal(to, via, text string) handedScript {
	return handedScript{to: to, via: via, text: text, read: true}
}

func computed(to, via string) handedScript { return handedScript{to: to, via: via} }

// countedScripts pairs every call totalRe finds with literalRe's literal
// argument; a call the literal form does not cover is computed.
func countedScripts(to, via, src string, totalRe, literalRe *regexp.Regexp, value func([]string) string) []handedScript {
	literals := literalRe.FindAllStringSubmatch(src, -1)
	out := make([]handedScript, 0, len(literals))
	for _, m := range literals {
		out = append(out, literal(to, via, value(m)))
	}
	for range len(totalRe.FindAllStringIndex(src, -1)) - len(literals) {
		out = append(out, computed(to, via))
	}
	return out
}

func firstGroup(m []string) string {
	for _, g := range m[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

// cStringUnescape undoes the backslash escapes awk, php and AppleScript share.
func cStringUnescape(s string) string {
	return strings.NewReplacer(`\"`, `"`, `\'`, `'`, `\n`, "\n", `\t`, "\t", `\\`, `\`).Replace(s)
}

var (
	awkStringRe        = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	awkRegexRe         = regexp.MustCompile(`/(?:[^/\\\n]|\\.)+/`)
	awkSystemRe        = regexp.MustCompile(`\bsystem\s*\(`)
	awkLiteralSystemRe = regexp.MustCompile(`\bsystem\s*\(\s*\x01(\d+)\x01\s*\)`)
	awkLiteralPipeRe   = regexp.MustCompile(`\|&?\s*\x01(\d+)\x01|\x01(\d+)\x01\s*\|&?\s*getline`)
	awkPipeRe          = regexp.MustCompile(`\|&?`)
)

// awkScripts are the commands an awk program starts through system() or a
// pipe. Its strings are numbered out first, so a `|` or a `system(` inside
// one is text, and a regex's `|` is alternation.
func awkScripts(to, program string) []handedScript {
	var strs []string
	stripped := awkStringRe.ReplaceAllStringFunc(program, func(s string) string {
		strs = append(strs, cStringUnescape(s[1:len(s)-1]))
		return fmt.Sprintf("\x01%d\x01", len(strs)-1)
	})
	stripped = strings.ReplaceAll(awkRegexRe.ReplaceAllString(stripped, "//"), "||", "")
	value := func(m []string) string {
		i, _ := strconv.Atoi(firstGroup(m))
		return strs[i]
	}
	return append(countedScripts(to, "system()", stripped, awkSystemRe, awkLiteralSystemRe, value),
		countedScripts(to, "a pipe", stripped, awkPipeRe, awkLiteralPipeRe, value)...)
}

// sedScripts are the commands GNU sed's e runs. An `e command` is literal. A
// bare e or an s///e runs the pattern space, which is computed.
func sedScripts(to, script string) []handedScript {
	var out []handedScript
	for i := 0; i < len(script); {
		c := script[i]
		switch {
		case c == '/':
			i = skipDelimited(script, i+1, '/')
		case c == '\\' && i+1 < len(script):
			i = skipDelimited(script, i+2, script[i+1])
		case c == '#' || c == ':' || c == 'r' || c == 'R' || c == 'w' || c == 'W':
			i = sedLineEnd(script, i)
		case c == 'a' || c == 'i' || c == 'c':
			i = sedTextEnd(script, i)
		case c == 'b' || c == 't' || c == 'T':
			j := strings.IndexAny(script[i:], ";\n")
			if j < 0 {
				return out
			}
			i += j
		case c == 'e':
			var h handedScript
			h, i = sedExec(to, script, i)
			out = append(out, h)
		case c == 's' && i+1 < len(script):
			var hs []handedScript
			hs, i = sedSubstitute(to, script, i)
			out = append(out, hs...)
		case c == 'y' && i+1 < len(script):
			delim := script[i+1]
			i = skipDelimited(script, skipDelimited(script, i+2, delim), delim)
		default:
			i++
		}
	}
	return out
}

// sedLineEnd is the index of the newline that ends the line holding i.
func sedLineEnd(script string, i int) int {
	if j := strings.IndexByte(script[i:], '\n'); j >= 0 {
		return i + j
	}
	return len(script)
}

// sedTextEnd is the end of the text an a, i or c at i adds. A line ending in
// a backslash goes on to the next.
func sedTextEnd(script string, i int) int {
	for i = sedLineEnd(script, i); i > 0 && i < len(script) && script[i-1] == '\\'; {
		i = sedLineEnd(script, i+1)
	}
	return i
}

// sedExec reads the e command at i and returns the index past it.
func sedExec(to, script string, i int) (handedScript, int) {
	end := sedLineEnd(script, i)
	command := strings.TrimSpace(script[i+1 : end])
	if command == "" || command[0] == ';' || command[0] == '}' {
		return computed(to, "e"), end
	}
	return literal(to, "e", command), end
}

// sedSubstitute reads the s command at i, an e flag among its flags, and
// returns the index past it. A w flag takes the rest of the line.
func sedSubstitute(to, script string, i int) ([]handedScript, int) {
	var out []handedScript
	delim := script[i+1]
	i = skipDelimited(script, skipDelimited(script, i+2, delim), delim)
	for ; i < len(script) && !strings.ContainsRune(";\n}", rune(script[i])); i++ {
		if script[i] == 'e' {
			out = append(out, computed(to, "s///e"))
		}
		if script[i] == 'w' {
			return out, sedLineEnd(script, i)
		}
	}
	return out, i
}

// skipDelimited is the index past the delim that closes the field from i.
func skipDelimited(s string, i int, delim byte) int {
	for ; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case delim:
			return i + 1
		}
	}
	return len(s)
}

var (
	phpCallRe        = regexp.MustCompile(`(?i)(?:^|[^\w>:$\\])(?:system|exec|shell_exec|passthru|popen|proc_open|pcntl_exec)\s*\(`)
	phpLiteralCallRe = regexp.MustCompile(`(?i)(?:^|[^\w>:$\\])(?:system|exec|shell_exec|passthru|popen|proc_open|pcntl_exec)\s*\(\s*(?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\$]|\\.)*)")\s*[,)]`)
	phpBacktickRe    = regexp.MustCompile("`([^`]*)`")
)

// phpScripts are the commands a php program starts: a shell function's
// first argument, and a backtick string.
func phpScripts(to, program string) []handedScript {
	out := countedScripts(to, "a shell function", program, phpCallRe, phpLiteralCallRe, func(m []string) string {
		return cStringUnescape(firstGroup(m))
	})
	for _, m := range phpBacktickRe.FindAllStringSubmatch(program, -1) {
		if strings.Contains(m[1], "$") {
			out = append(out, computed(to, "a backtick string"))
			continue
		}
		out = append(out, literal(to, "a backtick string", m[1]))
	}
	return out
}

var (
	doShellRe        = regexp.MustCompile(`(?i)\bdo\s+shell\s+script\b`)
	doShellLiteralRe = regexp.MustCompile(`(?i)\bdo\s+shell\s+script\s+"((?:[^"\\]|\\.)*)"(?:\s*$|\s*[^&\s])`)
	jsShellRe        = regexp.MustCompile(`\bdoShellScript\s*\(`)
	jsShellLiteralRe = regexp.MustCompile(`\bdoShellScript\s*\(\s*(?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)")\s*[,)]`)
)

// appleScripts are the commands an AppleScript or JXA program runs with do
// shell script. A string joined with & is computed.
func appleScripts(to, program string) []handedScript {
	var out []handedScript
	for _, line := range strings.Split(program, "\n") {
		out = append(out, countedScripts(to, "do shell script", line, doShellRe, doShellLiteralRe, func(m []string) string {
			return cStringUnescape(firstGroup(m))
		})...)
	}
	return append(out, countedScripts(to, "doShellScript", program, jsShellRe, jsShellLiteralRe, func(m []string) string {
		return cStringUnescape(firstGroup(m))
	})...)
}

var (
	tclExecRe = regexp.MustCompile(`(?m)(?:^|[;\[{])\s*exec\s+([^\n;\]]*)`)
	tclOpenRe = regexp.MustCompile(`\bopen\s+(?:"\|([^"]*)"|\{\|([^}]*)\})`)
	tclBraces = strings.NewReplacer("{", "'", "}", "'")
)

// tclScripts are the commands a Tcl program runs with exec or opens as a
// pipe. A word with a substitution is computed.
func tclScripts(to, program string) []handedScript {
	var out []handedScript
	add := func(via, words string) {
		if strings.ContainsAny(words, "$[") {
			out = append(out, computed(to, via))
			return
		}
		out = append(out, literal(to, via, tclBraces.Replace(words)))
	}
	for _, m := range tclExecRe.FindAllStringSubmatch(program, -1) {
		words := strings.TrimSpace(m[1])
		for _, opt := range []string{"-ignorestderr", "-keepnewline", "--"} {
			words = strings.TrimSpace(strings.TrimPrefix(words, opt))
		}
		add("exec", words)
	}
	for _, m := range tclOpenRe.FindAllStringSubmatch(program, -1) {
		add("open", firstGroup(m))
	}
	return out
}

var (
	exRangeRe        = regexp.MustCompile(`^[\s:]*(?:sil(?:ent)?!?\s*)?[0-9,.$%'<>+\-]*`)
	exReadWriteRe    = regexp.MustCompile(`^(?:r(?:e(?:ad?)?)?\s*|w(?:r(?:i(?:te?)?)?)?\s+)!`)
	exSystemRe       = regexp.MustCompile(`\bsystem(?:list)?\s*\(`)
	exLiteralSystem  = regexp.MustCompile(`\bsystem(?:list)?\s*\(\s*(?:'([^']*)'|"((?:[^"\\]|\\.)*)")\s*[,)]`)
	exExpandsInShell = regexp.MustCompile(`(?:^|[^\\])[%#]`)
)

// exScripts are the commands an Ex line runs by `!`, `read !`, `write !` or
// system(). A `!` takes the rest of the line. Vim expands an unescaped % or
// # in it.
func exScripts(to, line string) []handedScript {
	out := countedScripts(to, "system()", line, exSystemRe, exLiteralSystem, func(m []string) string {
		return cStringUnescape(firstGroup(m))
	})
	segments := strings.Split(line, "|")
	for i, seg := range segments {
		c := exRangeRe.ReplaceAllString(seg, "")
		if m := exReadWriteRe.FindString(c); m != "" {
			c = c[len(m)-1:]
		}
		if !strings.HasPrefix(c, "!") {
			continue
		}
		script := strings.Join(append([]string{c[1:]}, segments[i+1:]...), "|")
		if exExpandsInShell.MatchString(script) {
			return append(out, computed(to, "!"))
		}
		return append(out, literal(to, "!", script))
	}
	return out
}
