package sandbox

import (
	"os"
	"testing"

	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// TestTmpRuleNotExecutable verifies the default temp-dir rule grants read+write
// but NOT execute. On a multiuser host the temp dir is world-shared, so granting
// execve there would let a confined spell run payloads planted by other users.
func TestTmpRuleNotExecutable(t *testing.T) {
	if os.TempDir() == "" {
		t.Skip("no temp dir on this platform")
	}
	p := BuildPolicy(t.TempDir(), nil, nil, nil, nil)
	tmp := filesystem.ResolveRulePath(os.TempDir())

	var found bool
	for _, r := range p.FS.Rules {
		if r.Path != tmp {
			continue
		}
		found = true
		if r.Exec {
			t.Errorf("temp-dir rule %q has Exec=true; world-shared temp must not be executable", tmp)
		}
		if !r.Read || !r.Write {
			t.Errorf("temp-dir rule %q should be read+write, got read=%v write=%v", tmp, r.Read, r.Write)
		}
	}
	if !found {
		t.Fatalf("no temp-dir rule (%q) found in default policy", tmp)
	}
}

// TestRulePathsNormalized verifies BuildPolicy resolves rule paths so a rule
// path that is a symlink is compared symmetrically with resolved access paths.
// A workspace reached through a symlink must still match its own rule.
func TestRulePathsNormalized(t *testing.T) {
	realDir := t.TempDir()
	link := t.TempDir() + "/link"
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// Build the policy against the symlink; the rule path must be resolved to
	// the real directory so a read of a file under it is allowed.
	p := BuildPolicy(link, nil, nil, nil, nil)
	if err := p.CheckRead(realDir + "/file.txt"); err != nil {
		t.Errorf("CheckRead under resolved workspace should pass, got %v", err)
	}
}
