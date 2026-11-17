//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
)

// LauncherFiles is how many ExtraFiles a confining Cmd from Command carries ahead of
// any a caller appends: the ruleset.
const LauncherFiles = 1

// requiredLevel is the landlock ABI Required accepts.
const requiredLevel = RequiredABI

// defaultProbe is the running kernel's landlock ABI.
func defaultProbe() (int, error) { return ABI() }

// confine starts path as a launcher that applies p's compiled landlock ruleset to
// itself and execs path with argv name, args. The ruleset rides in ExtraFiles,
// closed in the launcher before the exec, so nothing of the policy reaches the
// command. This process's copy closes when the Cmd is collected; close
// cmd.ExtraFiles[0] after Start to release it sooner.
func confine(ctx context.Context, p *Policy, path, name string, args []string) (*exec.Cmd, error) {
	abi, err := ABI()
	if err != nil {
		return nil, err
	}
	self, rest := procSelfRules(p.rules.Rules, os.Getpid())
	rulesetFD, err := buildRuleset(rest, abi, handledScopes(abi))
	if err != nil {
		return nil, err
	}

	// /proc/self/exe, not os.Executable: it names the running image even after a
	// rebuild replaces the file on disk, so the launcher always speaks this protocol.
	cmd := exec.CommandContext(ctx, "/proc/self/exe")
	cmd.ExtraFiles = []*os.File{os.NewFile(uintptr(rulesetFD), "landlock-ruleset")}
	// ExtraFiles[0] is descriptor 3 in the child.
	cmd.Args = append([]string{launcherName, "3", encodeRules(self), path, name}, args...)
	return cmd, nil
}

// launch is the launcher half of Command. args is the ruleset descriptor, the rules on
// the launcher's own procfs entries (see procSelfRules), the resolved path, then the
// command's argv.
func launch(args []string) error {
	if len(args) < 4 {
		return errors.New("sandbox: launcher: want a ruleset descriptor, its /proc/self rules, a path and an argv")
	}
	rulesetFD, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("sandbox: launcher: ruleset descriptor: %w", err)
	}
	self, err := decodeRules(args[1])
	if err != nil {
		return err
	}
	path, argv := args[2], args[3:]

	// Only this thread is confined, and that is enough: it is the one that execs, and
	// execve leaves the new image with this thread alone. Confining every thread
	// instead would fail under cgo and, on kernels without landlock erratum 2,
	// deadlock on the signal scope.
	runtime.LockOSThread()
	if len(self) > 0 {
		// The exec keeps this pid, so this process's /proc entries are the command's.
		abi, err := ABI()
		if err != nil {
			return err
		}
		if err := addPathRules(rulesetFD, self, handledAccessFS(abi)); err != nil {
			return err
		}
	}
	if err := restrictSelf(rulesetFD); err != nil {
		return err
	}
	// An exec does no os/exec descriptor setup. Close the ruleset descriptor
	// explicitly so the command never inherits descriptor 3.
	if err := unix.Close(rulesetFD); err != nil {
		return fmt.Errorf("sandbox: launcher: close ruleset descriptor: %w", err)
	}
	// path is the command the parent chose and resolved, now under the ruleset.
	if err := unix.Exec(path, argv, os.Environ()); err != nil {
		return fmt.Errorf("sandbox: launcher: exec %s: %w", path, err)
	}
	return nil
}

// procSelfRules splits rules into those on process pid's own procfs entries, rewritten
// under /proc/self, and the rest. Rule paths are resolved when the policy is built, so
// /proc/self and /dev/fd name the pid of the process that built it. A ruleset built
// there would grant a child that process's own entries and none of the child's, which
// do not exist yet; the launcher grants those for itself.
func procSelfRules(rules []filesystem.Rule, pid int) (self, rest []filesystem.Rule) {
	own := "/proc/" + strconv.Itoa(pid)
	for _, r := range rules {
		if tail, ok := strings.CutPrefix(r.Path, own); ok && (tail == "" || tail[0] == '/') {
			r.Path = "/proc/self" + tail
			self = append(self, r)
			continue
		}
		rest = append(rest, r)
	}
	return self, rest
}

// encodeRules renders rules for the launcher's argv as "rwx:/path" words joined by
// commas, a dash for an access not granted.
//
// A path's backslashes and commas are backslash-escaped.
// Otherwise a path holding ",rwx:/" would inject a rule.
func encodeRules(rules []filesystem.Rule) string {
	escaper := strings.NewReplacer(`\`, `\\`, `,`, `\,`)
	words := make([]string, 0, len(rules))
	for _, r := range rules {
		flag := func(on bool, c string) string {
			if on {
				return c
			}
			return "-"
		}
		words = append(words, fmt.Sprintf("%s%s%s:%s", flag(r.Read, "r"), flag(r.Write, "w"), flag(r.Exec, "x"), escaper.Replace(r.Path)))
	}
	return strings.Join(words, ",")
}

// splitRuleWords splits s at its unescaped commas and unescapes each word.
func splitRuleWords(s string) ([]string, error) {
	var words []string
	var w strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			if i+1 == len(s) || (s[i+1] != '\\' && s[i+1] != ',') {
				return nil, fmt.Errorf("sandbox: launcher: malformed escape at byte %d of %q", i, s)
			}
			i++
			w.WriteByte(s[i])
		case ',':
			words = append(words, w.String())
			w.Reset()
		default:
			w.WriteByte(c)
		}
	}
	return append(words, w.String()), nil
}

// decodeRules is encodeRules undone.
func decodeRules(s string) ([]filesystem.Rule, error) {
	if s == "" {
		return nil, nil
	}
	words, err := splitRuleWords(s)
	if err != nil {
		return nil, err
	}
	var rules []filesystem.Rule
	for _, w := range words {
		flags, path, ok := strings.Cut(w, ":")
		if !ok || len(flags) != 3 || !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("sandbox: launcher: malformed rule %q", w)
		}
		rules = append(rules, filesystem.Rule{Path: path, Read: flags[0] == 'r', Write: flags[1] == 'w', Exec: flags[2] == 'x'})
	}
	return rules, nil
}
