package vcs_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

func TestResolveAutodetect(t *testing.T) {
	cases := []struct {
		claim string
		want  string
	}{
		{".git", "git"},
		{".hg", "hg"},
		{".jj", "jj"},
	}
	for _, c := range cases {
		t.Run(c.claim, func(t *testing.T) {
			t.Setenv("MAGUS_VCS_NAME", "")
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, c.claim), 0o755); err != nil {
				t.Fatal(err)
			}
			res, err := vcs.Resolve(context.Background(), root, "origin/main", types.VCSOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if res.Name != c.want {
				t.Errorf("Name = %q, want %q", res.Name, c.want)
			}
			if res.Source != types.VCSSourceAuto {
				t.Errorf("Source = %q, want %q", res.Source, types.VCSSourceAuto)
			}
			if res.VCS == nil {
				t.Error("VCS is nil, want non-nil")
			}
		})
	}
}

func TestResolveExplicitOverridesAutodetect(t *testing.T) {
	t.Setenv("MAGUS_VCS_NAME", "jj")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := vcs.Resolve(context.Background(), root, "origin/main", types.VCSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "jj" {
		t.Errorf("Name = %q, want jj", res.Name)
	}
	if res.Source != types.VCSSourceExplicit {
		t.Errorf("Source = %q, want explicit", res.Source)
	}
}

func TestResolveExplicitUnknown(t *testing.T) {
	t.Setenv("MAGUS_VCS_NAME", "fossil")
	_, err := vcs.Resolve(context.Background(), t.TempDir(), "origin/main", types.VCSOptions{})
	if err == nil {
		t.Fatal("expected error for unknown VCS name, got nil")
	}
	if !errors.Is(err, types.ErrVCSUnknown) {
		t.Errorf("error = %v, want ErrUnknownVCS", err)
	}
}

func TestResolveDefaultWhenNoMarker(t *testing.T) {
	t.Setenv("MAGUS_VCS_NAME", "")
	res, err := vcs.Resolve(context.Background(), t.TempDir(), "origin/main", types.VCSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "git" {
		t.Errorf("Name = %q, want git", res.Name)
	}
	if res.Source != types.VCSSourceDefault {
		t.Errorf("Source = %q, want default", res.Source)
	}
}

func TestResolveDisabled(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "false")
	res, err := vcs.Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != types.VCSSourceDisabled {
		t.Errorf("Source = %q, want disabled", res.Source)
	}
	if res.VCS != nil {
		t.Errorf("VCS = %v, want nil", res.VCS)
	}
}

func TestResolvePerVCSBaseRef(t *testing.T) {
	t.Setenv("MAGUS_VCS_ENABLED", "")
	t.Setenv("MAGUS_VCS_BASE_REF", "")
	t.Setenv("MAGUS_VCS_NAME", "jj")
	t.Setenv("MAGUS_VCS_JJ_BASE_REF", "main@origin")
	res, err := vcs.Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Base != "main@origin" {
		t.Errorf("Base = %q, want main@origin", res.Base)
	}
}

func TestResolveBuiltinBaseRefs(t *testing.T) {
	cases := []struct {
		name, want string
	}{
		{"git", "origin/main"},
		{"hg", "tip"},
		{"jj", "trunk()"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("MAGUS_VCS_ENABLED", "")
			t.Setenv("MAGUS_VCS_BASE_REF", "")
			t.Setenv("MAGUS_VCS_NAME", c.name)
			t.Setenv(perVCSEnv(c.name, "BASE_REF"), "")
			res, err := vcs.Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if res.Base != c.want {
				t.Errorf("Base = %q, want %q", res.Base, c.want)
			}
		})
	}
}

func TestVCSClaims(t *testing.T) {
	cases := []struct {
		name   string
		claims []string
	}{
		{"git", []string{".git"}},
		{"hg", []string{".hg"}},
		{"jj", []string{".jj"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("MAGUS_VCS_NAME", c.name)
			res, err := vcs.Resolve(context.Background(), t.TempDir(), "", types.VCSOptions{})
			if err != nil {
				t.Fatalf("Resolve(%q): %v", c.name, err)
			}
			got := res.VCS.Claims()
			if len(got) != len(c.claims) {
				t.Fatalf("Claims() = %v, want %v", got, c.claims)
			}
			for i := range got {
				if got[i] != c.claims[i] {
					t.Errorf("Claims()[%d] = %q, want %q", i, got[i], c.claims[i])
				}
			}
		})
	}
}

func TestDiffCommandsGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	dir := t.TempDir()
	mustRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", args, out)
		}
	}
	mustRun("git", "init")
	mustRun("git", "config", "user.email", "test@example.com")
	mustRun("git", "config", "user.name", "Test")
	mustRun("git", "config", "commit.gpgsign", "false")
	mustRun("git", "commit", "--allow-empty", "-m", "init")

	// Capture the SHA we just created.
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	wantSHA := strings.TrimSpace(string(out))

	t.Setenv("MAGUS_VCS_NAME", "git")
	res, err := vcs.Resolve(context.Background(), dir, "", types.VCSOptions{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	hints, err := res.VCS.DiffCommands(t.Context(), dir, "origin/main")
	if err != nil {
		t.Fatalf("DiffCommands: %v", err)
	}

	wantCLI := "git diff origin/main..." + wantSHA
	wantGUI := "git difftool origin/main..." + wantSHA
	if hints.CLI != wantCLI {
		t.Errorf("CLI = %q, want %q", hints.CLI, wantCLI)
	}
	if hints.GUI != wantGUI {
		t.Errorf("GUI = %q, want %q", hints.GUI, wantGUI)
	}
}

func TestFindCommitAndHistoryGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}

	dir := t.TempDir()
	mustRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", args, out)
		}
	}
	mustRun("git", "init")
	mustRun("git", "config", "user.email", "alice@example.com")
	mustRun("git", "config", "user.name", "Alice")
	mustRun("git", "config", "commit.gpgsign", "false")
	mustRun("git", "commit", "--allow-empty", "-m", "first")
	mustRun("git", "commit", "--allow-empty", "-m", "second line\n\nbody text")

	res, err := vcs.Resolve(context.Background(), dir, "", types.VCSOptions{Name: "git"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	c, err := res.VCS.FindCommit(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("FindCommit: %v", err)
	}
	if c.Subject != "second line" {
		t.Errorf("Subject = %q, want %q", c.Subject, "second line")
	}
	if c.Body != "body text" {
		t.Errorf("Body = %q, want %q", c.Body, "body text")
	}
	if c.Author.Name != "Alice" || c.Author.Email != "alice@example.com" {
		t.Errorf("Author = %+v, want Alice <alice@example.com>", c.Author)
	}
	if c.Date.IsZero() {
		t.Error("Date is zero; expected a parsed RFC3339 record date")
	}
	if c.ID == "" || c.Short == "" || !strings.HasPrefix(c.ID, c.Short) {
		t.Errorf("ID/Short inconsistent: %q / %q", c.ID, c.Short)
	}

	hist, err := res.VCS.History(context.Background(), dir, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("History len = %d, want 2", len(hist))
	}
	if hist[0].Subject != "second line" || hist[1].Subject != "first" {
		t.Errorf("History order wrong: %q, %q (want newest first)", hist[0].Subject, hist[1].Subject)
	}
}

func TestInstallableAndInstaller(t *testing.T) {
	names := vcs.InstallableVCSes()
	// git and hg implement MergeDriverInstaller; jj does not.
	want := map[string]bool{"git": true, "hg": true}
	if len(names) != len(want) {
		t.Fatalf("Installable() = %v, want keys %v", names, want)
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("Installable() returned unexpected %q", n)
		}
		if _, ok := vcs.Installer(n); !ok {
			t.Errorf("Installer(%q): got !ok, want an installer", n)
		}
	}

	// jj is a known VCS but exposes no merge-driver installer.
	if _, ok := vcs.Installer("jj"); ok {
		t.Error("Installer(\"jj\"): got ok, want false (no installer)")
	}
	// Unknown VCS name yields no installer.
	if _, ok := vcs.Installer("svn"); ok {
		t.Error("Installer(\"svn\"): got ok, want false (unknown VCS)")
	}
}

func perVCSEnv(name, suffix string) string {
	return "MAGUS_VCS_" + toUpper(name) + "_" + suffix
}

func toUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}
