package guard

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"mvdan.cc/sh/v3/syntax"
)

// copy-write: a file written to scratch and copied in with cp, mv or install reaches the
// workspace without any rule reading its text. The source's bytes are judged as the
// write of its destination, as a heredoc's body is.

const denyRuleCopyWrite denyRuleName = "copy-write"

// copyWriteCap bounds the bytes read from one source. A larger text file is refused
// rather than judged by path alone, since the cap is what keeps the judgment bounded.
const copyWriteCap = 1 << 20

// copyWrite is one file a line copies or moves into place: the source and the file its
// bytes land in.
type copyWrite struct {
	name   string
	source string
	target string
}

// copyWrites are the line's cp, mv and install calls whose source is a regular file
// outside workspace and whose destination is inside it, one per source. A source already
// inside the workspace met the write rules on its way in.
func copyWrites(command string, d Dialect, start, home, workspace string) []copyWrite {
	if workspace == "" {
		return nil
	}
	var out []copyWrite
	dirTable := newScriptDirs(start, home)
	eachScript(command, d, func(sf *scriptFile) bool {
		dirs := dirTable.of(sf)
		syntax.Walk(sf.file, func(n syntax.Node) bool {
			stmt, ok := n.(*syntax.Stmt)
			if !ok {
				return true
			}
			for _, c := range shellCommands(sf.text, stmt, d, placeableWord) {
				out = append(out, callCopyWrites(path.Base(c.Name), c.Args, dirs[stmt], home, workspace)...)
			}
			return true
		})
		return true
	})
	return out
}

// callCopyWrites is copyWrites for one call of name with args, run in dir.
func callCopyWrites(name string, args []string, dir, home, workspace string) []copyWrite {
	if name != "cp" && name != "mv" && name != "install" {
		return nil
	}
	ops := operands(args, "mogStT")
	intoDir := ""
	for i, a := range args {
		if a == "-t" && i+1 < len(args) {
			intoDir = args[i+1]
		} else if v, ok := strings.CutPrefix(a, "--target-directory="); ok {
			intoDir = v
		}
	}
	sources := ops
	if intoDir == "" {
		if len(ops) < 2 {
			return nil
		}
		sources, intoDir = ops[:len(ops)-1], ops[len(ops)-1]
	}
	dest, placed := placedWord{intoDir, dir}.place(home)
	if !placed {
		return nil
	}
	if _, inside := workspaceRelative(workspace, dest); !inside {
		return nil
	}
	var out []copyWrite
	for _, word := range sources {
		source, placed := placedWord{word, dir}.place(home)
		if !placed {
			continue
		}
		if _, inside := workspaceRelative(workspace, source); inside {
			continue
		}
		info, err := os.Stat(source)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		target := dest
		if d, err := os.Stat(dest); (err == nil && d.IsDir()) || len(sources) > 1 {
			target = filepath.Join(dest, filepath.Base(source))
		}
		out = append(out, copyWrite{name: word, source: source, target: target})
	}
	return out
}

// copySource is the text a copy writes, "" for a binary file, which the write rules judge
// by path alone. A source too large or unreadable is refused: reason says why.
func copySource(source string) (text, reason string) {
	f, err := os.Open(source)
	if err != nil {
		return "", fmt.Sprintf("%s cannot be read, so its bytes cannot be judged: %v", source, err)
	}
	defer func() { _ = f.Close() }() // read only; a close error loses nothing
	data, err := io.ReadAll(io.LimitReader(f, copyWriteCap+1))
	if err != nil {
		return "", fmt.Sprintf("%s cannot be read, so its bytes cannot be judged: %v", source, err)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", ""
	}
	if len(data) > copyWriteCap {
		return "", fmt.Sprintf("%s is over %d bytes, more than the guard reads to judge a copy", source, copyWriteCap)
	}
	return string(data), ""
}

// judgeCopyWrites judges each copy into the workspace as the write it is, by asking
// Judge about the file write a host's tool would have sent, without recording it. The
// first refusal wins; otherwise the first advice. Ungraded: the caller grades it under
// copy-write.
func judgeCopyWrites(ctx context.Context, deps Dependencies, req Request, who hookAttribution, callDir, lease, command string, d Dialect) ShellVerdict {
	writes := copyWrites(command, d, callDir, userHome(), hookLocation(ctx, deps).workspace)
	var advised ShellVerdict
	for _, w := range writes {
		content, reason := copySource(w.source)
		if reason != "" {
			return ShellVerdict{
				Deny: fmt.Sprintf("this line copies %s into %s, and the copy is refused: %s", w.name, w.target, reason),
				Why:  "A file copied in from outside the workspace is a file write, judged by the rules the host's write tool meets, which read its text.",
				Rule: denyRule{Name: denyRuleCopyWrite},
			}
		}
		switch v := judgeFileWrite(ctx, deps, req, who, callDir, lease, w.target, content); v.Decision {
		case verdict.Deny:
			first, _, _ := strings.Cut(v.Reason, "\n")
			return ShellVerdict{
				Deny: fmt.Sprintf("this line copies %s into %s, and that write is refused [%s]: %s", w.name, w.target, v.Rule, first),
				Why:  "A file copied in from outside the workspace is a file write, judged by the rules the host's write tool meets.\n" + v.Reason,
				Rule: denyRule{Name: denyRuleCopyWrite, Arg: v.Rule},
			}
		case verdict.Advise:
			if advised.Context == "" {
				advised = ShellVerdict{
					Context: fmt.Sprintf("magus workspace: this line copies %s into %s, judged as that write [%s]: %s", w.name, w.target, v.Rule, v.Context),
					Rule:    denyRule{Name: denyRuleCopyWrite, Arg: v.Rule},
				}
			}
		}
	}
	return advised
}
