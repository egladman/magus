package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/termkit"
)

// legacyGlueDir is where every hook line ran its glue from before install copied the
// glue into the workspace.
const legacyGlueDir = "docs/guides/integrations/agents/"

// legacyGlueScript is the embedded glue to run in place of script, when script is a
// legacy hook line's path to a shipped glue script and this workspace holds no file
// there. A checkout of magus holds one, so it runs as written. Any other path, or a
// name the binary embeds no script for, is not legacy and fails as it would.
//
// compat(until: no workspace's hook lines name docs/guides/integrations/agents): a session
// keeps the hook lines it started with, and outside a checkout of magus the old path does
// not exist, so a session started before `magus agent harness install` moved its lines
// would have every call refused, the reinstall included.
func legacyGlueScript(script string) (string, bool, error) {
	name, ok := strings.CutPrefix(filepath.ToSlash(filepath.Clean(script)), legacyGlueDir)
	if !ok || !agent.IsGlueScript(name) {
		return "", false, nil
	}
	if _, err := os.Stat(script); !errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	digest, err := agent.GlueDigest()
	if err != nil {
		return "", false, err
	}
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("magus-legacy-glue-%d-%s", os.Getuid(), digest))
	if err := agent.WriteGlue(dir); err != nil {
		return "", false, fmt.Errorf("write the embedded glue for the legacy hook line %s: %w", script, err)
	}
	return filepath.Join(dir, name), true, nil
}

// noteLegacyGlue says on stderr, once per hook session, that script's legacy line ran the
// embedded glue, so the fallback is never silent. It reads the event to learn the session
// and hands the same bytes on as the glue's stdin.
//
// compat: see legacyGlueScript.
func noteLegacyGlue(ctx context.Context, script string) error {
	event, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	go func() {
		_, _ = w.Write(event) // the glue reading less than all of it is its own business
		_ = w.Close()         // nothing more is written; a close error leaves the glue at EOF all the same
	}()
	os.Stdin = r
	marker := filepath.Join(os.TempDir(), "magus-guard-notices-"+fileSafe(os.Getenv("USER")), fileSafe(hookSession(event))+".legacy-glue")
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err == nil {
		_ = os.WriteFile(marker, nil, 0o644) // unwritten, the note repeats on the next call, which is louder, not quieter
	}
	// Unchecked: a fallback, which -s, passed by every hook line, never hides.
	slog.WarnContext(ctx, fmt.Sprintf("this hook line names %s, which this workspace does not hold, so it ran the glue this magus embeds; "+
		"run `magus agent harness install` from your own terminal", script), attr.Unchecked())
	return nil
}

// hookSession is the session a host's hook event names, "anon" for none.
func hookSession(event []byte) string {
	var fields struct {
		Session      string `json:"session_id"`
		Conversation string `json:"conversation_id"`
	}
	_ = codec.Unmarshal(event, &fields) // an event that is not JSON names no session
	switch {
	case fields.Session != "":
		return fields.Session
	case fields.Conversation != "":
		return fields.Conversation
	}
	return "anon"
}

func fileSafe(s string) string {
	if s == "" {
		return "anon"
	}
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '.' {
			return '_'
		}
		return r
	}, s)
}

// hookGlueShaped reports `magus buzz -s` with stdin not a terminal: the shape every
// shipped hook wiring runs.
func hookGlueShaped(sub string, subArgs []string) bool {
	if sub != "buzz" || termkit.StdinIsTerminal() {
		return false
	}
	own, _ := splitForwarded(subArgs)
	return slices.Contains(own, "-s")
}

// splitForwarded splits buzz's own args from the script's, after `--`.
func splitForwarded(subArgs []string) (own, forwarded []string) {
	at := slices.Index(subArgs, "--")
	if at < 0 {
		return subArgs, nil
	}
	return subArgs[:at], subArgs[at+1:]
}

// answerFailedGlue turns the exit of a hook glue that failed with cause into the
// guard's answer to the event it was handed, since a host runs the call of a hook that
// exits anything but 2, and the reason to print. A script that was handed no hook event
// keeps code and has no reason.
func answerFailedGlue(stdin *guard.HookStdin, rootFlag string, subArgs []string, cause error, code int) (int, string) {
	_, forwarded := splitForwarded(subArgs)
	root, _ := workspace.FindRoot(rootFlag) // a root not found names the release as the repair
	answer, reason, isHook := guard.FailedGlue(root, stdin.Event(), slices.Contains(forwarded, "--agent-name"), cause)
	if !isHook {
		return code, ""
	}
	return answer.ExitCode(), reason
}
