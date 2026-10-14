package guard

import (
	"context"
	"path"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"mvdan.cc/sh/v3/syntax"
)

// heredoc-write: `cat > f.go <<'EOF'` writes a file the way the host's write tool does,
// and reached none of the rules a write meets. The body is judged as that write, so the
// comment, proofread and lease rules hold on either channel.

const denyRuleHeredocWrite denyRuleName = "heredoc-write"

// heredocWrite is one file a line writes from a here-document.
type heredocWrite struct {
	path string
	body string
}

// heredocWrites lists the files the line writes from a here-document whose body is literal
// text: `cat > f <<EOF` and `tee [-a] f <<EOF`. A body that expands a parameter or runs a
// substitution is text no one can read before it runs, so it is skipped, as is a target
// that is not a literal word, and one in /dev.
func heredocWrites(command string, d Dialect) []heredocWrite {
	f, err := parseFile(command, d)
	if err != nil {
		return nil
	}
	var out []heredocWrite
	syntax.Walk(f, func(n syntax.Node) bool {
		s, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		call, ok := s.Cmd.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		var body *syntax.Word
		var targets []string
		for _, r := range s.Redirs {
			switch {
			case (r.Op == syntax.Hdoc || r.Op == syntax.DashHdoc) && r.Hdoc != nil:
				body = r.Hdoc
			case (r.Op == syntax.RdrOut || r.Op == syntax.AppOut || r.Op == syntax.ClbOut) && (r.N == nil || r.N.Value == "1"):
				if t, ok := plainWord(r.Word); ok {
					targets = append(targets, t)
				}
			}
		}
		text, literal := plainWord(body)
		if body == nil || !literal {
			return true
		}
		args := literalWords(call.Args)
		switch path.Base(args[0]) {
		case "cat":
			if len(args) > 1 {
				return true
			}
		case "tee":
			targets = append(targets, nonFlagArgs(args[1:])...)
		default:
			return true
		}
		for _, t := range targets {
			if !strings.HasPrefix(t, "/dev/") {
				out = append(out, heredocWrite{path: t, body: text})
			}
		}
		return true
	})
	return out
}

// judgeFileWrite is the verdict a host's write tool would get for writing content to
// target, reached without recording anything: a write a call makes some other way than
// through that tool is held to the same rules. content is "" when the call's text for the
// file is unknown, which the write rules read as a write they judge by path alone.
func judgeFileWrite(ctx context.Context, deps Dependencies, req Request, who hookAttribution, callDir, lease, target, content string) types.GuardJudgment {
	input := map[string]any{"file_path": target}
	if content != "" {
		input["content"] = content
	}
	envelope, err := json.Marshal(map[string]any{
		"session_id": who.Session,
		"agent_id":   who.Agent,
		"cwd":        callDir,
		"tool_input": input,
	})
	if err != nil {
		return types.GuardJudgment{Decision: decisionPass}
	}
	// Judged as its own call, so it must not end the outer call's mark of a judgment in
	// progress.
	ctx = context.WithValue(ctx, judgmentMarkKey{}, judgmentMark{})
	return Judge(ctx, deps, Request{
		Input: string(envelope), DryRun: true, Dir: callDir, Lease: lease,
		Host: req.Host, Form: req.Form, Session: who.Session, Agent: who.Agent, Window: req.Window,
		ReportsSkills: req.ReportsSkills,
	})
}

// plainWord is w's text when every part of it is literal: no parameter, substitution or
// arithmetic for the shell to fill in.
func plainWord(w *syntax.Word) (string, bool) {
	if w == nil {
		return "", false
	}
	var b strings.Builder
	var walk func(parts []syntax.WordPart) bool
	walk = func(parts []syntax.WordPart) bool {
		for _, part := range parts {
			switch p := part.(type) {
			case *syntax.Lit:
				b.WriteString(p.Value)
			case *syntax.SglQuoted:
				b.WriteString(p.Value)
			case *syntax.DblQuoted:
				if !walk(p.Parts) {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	literal := walk(w.Parts)
	return b.String(), literal
}

// judgeHeredocWrites judges each here-document write on the line as the write it is, by
// asking Judge about the file write a host's tool would have sent, without recording it.
// The first refusal wins; otherwise the first advice. Ungraded: the caller grades it under
// heredoc-write.
func judgeHeredocWrites(ctx context.Context, deps Dependencies, req Request, who hookAttribution, callDir, lease, command string, d Dialect) ShellVerdict {
	writes := heredocWrites(command, d)
	if len(writes) == 0 {
		return ShellVerdict{}
	}
	var advised ShellVerdict
	for _, w := range writes {
		target := w.path
		if !filepath.IsAbs(target) && callDir != "" {
			target = filepath.Join(callDir, target)
		}
		switch v := judgeFileWrite(ctx, deps, req, who, callDir, lease, target, w.body); v.Decision {
		case decisionDeny:
			first, _, _ := strings.Cut(v.Reason, "\n")
			return ShellVerdict{
				Deny: "this heredoc writes " + w.path + ", and that write is refused [" + v.Rule + "]: " + first,
				Why:  "A here-document into a file is a file write, judged by the rules the host's write tool meets.\n" + v.Reason,
				Rule: denyRule{Name: denyRuleHeredocWrite, Arg: v.Rule},
			}
		case decisionAdvise:
			if advised.Context == "" {
				advised = ShellVerdict{
					Context: "magus workspace: this heredoc writes " + w.path + ", judged as that write [" + v.Rule + "]: " + v.Context,
					Rule:    denyRule{Name: denyRuleHeredocWrite, Arg: v.Rule},
				}
			}
		}
	}
	return advised
}
