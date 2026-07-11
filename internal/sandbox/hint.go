package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/interactive"
)

// allowLabel derives a dot-free config-key segment from a denied path or binary,
// so it can name a `sandbox.allow.<label>` entry. Falls back to "entry".
func allowLabel(target string) string {
	var b strings.Builder
	for _, r := range filepath.Base(target) {
		if r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "entry"
	}
	return b.String()
}

// denyHint renders the `magus config set` command(s) that allow a sandbox-denied
// operation on target (a path, or a resolved binary for exec). mode is "ro"
// (read/exec) or "rw" (write); rw needs the extra mode command.
func denyHint(mode, target string) string {
	label := allowLabel(target)
	cmd := fmt.Sprintf("sandbox blocked access to %s; allow it with:\n"+
		"        magus config set key=sandbox.allow.%s.path,value=%s", target, label, target)
	if mode == "rw" {
		cmd += fmt.Sprintf("\n        magus config set key=sandbox.allow.%s.mode,value=rw", label)
	}
	return cmd
}

// EmitDenyHint prints the "allow it with" remedy via the interactive hint channel
// (a no-op when hints are disabled). Call it at a sandbox denial site, before
// returning the diagnostic error, while the path/command is still typed and in
// scope — it doesn't survive being raised across a script VM, so a central
// handler could not reconstruct the target.
func EmitDenyHint(mode, target string) {
	interactive.Emit(os.Stderr, denyHint(mode, target))
}
