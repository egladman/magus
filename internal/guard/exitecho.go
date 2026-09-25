package guard

import (
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// exitEcho is which status a line's last statement prints.
type exitEcho int

const (
	exitEchoNone exitEcho = iota
	// exitEchoStatus prints `$?`, directly or through a variable that captured it.
	exitEchoStatus
	// exitEchoPipeStatus prints PIPESTATUS, the per-stage statuses a pipe discards.
	exitEchoPipeStatus
)

// The exit-0 fact leads the second line: it is what turns the echo from noise into a
// wrong answer, and nothing on screen says so.
const (
	denyExitStatusEcho = "Drop the trailing `echo $?`: the harness already reports a nonzero exit, and success needs no confirmation.\n" +
		"The echo exits 0, so the line reads as passing whatever ran before it. If a failure must not be masked, join with `&&` or make separate calls."
	denyPipeStatusEcho = "Drop the `echo ${PIPESTATUS[...]}` and let the line's own exit carry the failure: run the command without the pipe, or put `set -o pipefail` first.\n" +
		"The echo exits 0, so the line reads as passing whatever the pipeline did."
)

func denyExitStatusEchoFor(echo exitEcho) string {
	if echo == exitEchoPipeStatus {
		return denyPipeStatusEcho
	}
	return denyExitStatusEcho
}

// exitStatusEchoFires reports a line that ends by printing an exit status and nothing
// else. The host reports a nonzero exit on its own, and the echo exits 0, so the line
// reads as passing whatever ran before it.
//
// Three shapes end that way: `cmd; echo "rc=$?"`, the same through a capture
// (`cmd; rc=$?; echo $rc`), and `cmd || echo "failed $?"`, where the echo runs exactly
// when cmd failed and replaces its status with 0.
//
// Only the LAST statement, and only printed to the console (stderr included): anywhere
// earlier, redirected to a file, captured by `printf -v`, or followed by `exit $rc`, the
// status can feed later logic, and the guard cannot prove it does not.
func exitStatusEchoFires(command string, d Dialect) exitEcho {
	f, err := parseFile(command, d)
	if err != nil || len(f.Stmts) == 0 {
		return exitEchoNone
	}
	last := f.Stmts[len(f.Stmts)-1]
	if last.Background || last.Coprocess || last.Disown || last.Negated {
		return exitEchoNone
	}
	if or, ok := last.Cmd.(*syntax.BinaryCmd); ok {
		if or.Op != syntax.OrStmt {
			return exitEchoNone
		}
		return echoesStatus(or.Y, nil)
	}
	captured := map[string]exitEcho{}
	i := len(f.Stmts) - 2
	for ; i >= 0; i-- {
		name, echo, ok := statusCapture(f.Stmts[i])
		if !ok {
			break
		}
		if _, seen := captured[name]; !seen {
			captured[name] = echo
		}
	}
	if i < 0 {
		return exitEchoNone
	}
	// After `&`, $? is the status of starting a job, not of the job.
	if prev := f.Stmts[i]; prev.Background || prev.Coprocess || prev.Disown {
		return exitEchoNone
	}
	return echoesStatus(last, captured)
}

// statusCapture reports a statement that only assigns a status to a variable
// (`rc=$?`, `ps=${PIPESTATUS[0]}`).
func statusCapture(s *syntax.Stmt) (string, exitEcho, bool) {
	call, ok := s.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) > 0 || len(call.Assigns) != 1 || len(s.Redirs) > 0 || s.Background {
		return "", exitEchoNone, false
	}
	a := call.Assigns[0]
	if a.Name == nil || a.Value == nil || a.Append || a.Index != nil || len(a.Value.Parts) != 1 {
		return "", exitEchoNone, false
	}
	p, ok := a.Value.Parts[0].(*syntax.ParamExp)
	if !ok {
		return "", exitEchoNone, false
	}
	echo := statusParam(p, nil)
	return a.Name.Value, echo, echo != exitEchoNone
}

// echoesStatus reports an echo or printf to the console whose arguments expand to literal
// text and statuses alone.
func echoesStatus(s *syntax.Stmt, captured map[string]exitEcho) exitEcho {
	if s.Background || s.Coprocess || s.Disown || s.Negated {
		return exitEchoNone
	}
	if slices.ContainsFunc(s.Redirs, func(r *syntax.Redirect) bool { return !toStderr(r) }) {
		return exitEchoNone
	}
	call, ok := s.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) > 0 || len(call.Args) < 2 {
		return exitEchoNone
	}
	switch filepath.Base(literalWord(call.Args[0].Parts)) {
	case "echo":
	case "printf":
		if slices.ContainsFunc(call.Args[1:], func(w *syntax.Word) bool { return strings.HasPrefix(w.Lit(), "-v") }) {
			return exitEchoNone
		}
	default:
		return exitEchoNone
	}
	found := exitEchoNone
	for _, w := range call.Args[1:] {
		echo, only := onlyStatus(w.Parts, false, captured)
		if !only {
			return exitEchoNone
		}
		found = max(found, echo)
	}
	return found
}

// toStderr reports `>&2` and `1>&2`: still the console, so still what the reader sees.
func toStderr(r *syntax.Redirect) bool {
	if r.Op != syntax.DplOut || r.Word == nil || r.Word.Lit() != "2" {
		return false
	}
	return r.N == nil || r.N.Value == "1"
}

// onlyStatus reports whether parts expand to literal text and statuses alone, and the
// strongest status among them. An unquoted glob or tilde is refused because it expands
// to something other than the label it looks like.
func onlyStatus(parts []syntax.WordPart, quoted bool, captured map[string]exitEcho) (exitEcho, bool) {
	found := exitEchoNone
	for _, p := range parts {
		switch p := p.(type) {
		case *syntax.Lit:
			if !quoted && strings.ContainsAny(p.Value, "*?[~") {
				return exitEchoNone, false
			}
		case *syntax.SglQuoted:
		case *syntax.DblQuoted:
			echo, ok := onlyStatus(p.Parts, true, captured)
			if !ok {
				return exitEchoNone, false
			}
			found = max(found, echo)
		case *syntax.ParamExp:
			echo := statusParam(p, captured)
			if echo == exitEchoNone {
				return exitEchoNone, false
			}
			found = max(found, echo)
		default:
			return exitEchoNone, false
		}
	}
	return found, true
}

// statusParam reports a parameter expansion that yields a status unchanged: `$?`, any
// index of PIPESTATUS, or a variable that captured one.
func statusParam(p *syntax.ParamExp, captured map[string]exitEcho) exitEcho {
	if p.Param == nil || p.Flags != nil || p.Excl || p.Length || p.Width || p.IsSet ||
		p.NestedParam != nil || len(p.Modifiers) > 0 || p.Slice != nil || p.Repl != nil || p.Names != 0 || p.Exp != nil {
		return exitEchoNone
	}
	switch name := p.Param.Value; {
	case name == "PIPESTATUS":
		return exitEchoPipeStatus
	case p.Index != nil:
		return exitEchoNone
	case name == "?":
		return exitEchoStatus
	default:
		return captured[name]
	}
}
