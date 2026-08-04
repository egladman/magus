package bindings

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordRunner is a service.Runner that records starts without forking a process.
type recordRunner struct{ started int }

func (r *recordRunner) Start(context.Context, spells.Service) (service.Handle, error) {
	r.started++
	return struct{}{}, nil
}
func (r *recordRunner) Stop(service.Handle) {}

func serviceOp() spells.Op {
	// bin "true" exits 0, so the non-supervised fall-through fork is harmless.
	return spells.Op{
		Kind:    spells.OpKindService,
		Command: spells.Command{Bin: "true"},
		Service: &spells.Service{Command: spells.Command{Bin: "true"}},
	}
}

// TestRunCommandResolvesCwdAgainstContext proves a command op with no explicit cwd
// runs in the context working directory (the project dir the magusfile runner sets via
// std.WithCwd), not the process cwd. This is what lets a spell op invoked from a
// subproject target - go["go-run"] from docs/ - resolve its relative paths correctly.
func TestRunCommandResolvesCwdAgainstContext(t *testing.T) {
	dir := t.TempDir()
	ctx := std.WithCwd(context.Background(), dir)

	op := spells.Op{Command: spells.Command{Bin: "sh", Args: []string{"-c", "echo hi > marker.txt"}}}
	_, err := runCommand(ctx, op, commandOpts{})
	require.NoError(t, err)

	assert.FileExists(t, filepath.Join(dir, "marker.txt"),
		"an op with no explicit cwd must run in the context cwd, not the process cwd")
}

// stubPackageManagers prepends a temp bin dir to PATH holding a fake for each
// package manager; each records "<name> <argv>" into invoked.txt when run. The
// returned read func reports which stub ran and with what argv.
func stubPackageManagers(t *testing.T) func() string {
	t.Helper()
	binDir := t.TempDir()
	record := filepath.Join(binDir, "invoked.txt")
	for _, name := range []string{"npm", "pnpm", "yarn", "bun"} {
		script := "#!/bin/sh\necho \"" + name + " $@\" > " + record + "\n"
		require.NoError(t, os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755))
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		data, err := os.ReadFile(record)
		require.NoError(t, err, "no package-manager stub was invoked")
		return strings.TrimSpace(string(data))
	}
}

// TestRunCommandSubstitutesPackageManager proves the fork-time swap: an op that
// recorded pnpm forks the manager the project's lockfile names when its spell
// opted in via pmBin, including bun's exec->x verb rewrite.
func TestRunCommandSubstitutesPackageManager(t *testing.T) {
	invoked := stubPackageManagers(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bun.lock"), nil, 0o644))
	ctx := std.WithCwd(context.Background(), dir)

	op := spells.Op{Command: spells.Command{Bin: "pnpm", Args: []string{"exec", "tsc", "--noEmit"}}}
	_, err := runCommand(ctx, op, commandOpts{pmBin: "pnpm"})
	require.NoError(t, err)
	assert.Equal(t, "bun x tsc --noEmit", invoked(),
		"a bun-lockfile project must fork bun with the exec verb rewritten to x")
}

// TestRunCommandKeepsRecordedBinWithoutOptIn proves a spell that declared no
// package-manager bin is never substituted, whatever the project dir contains.
func TestRunCommandKeepsRecordedBinWithoutOptIn(t *testing.T) {
	invoked := stubPackageManagers(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bun.lock"), nil, 0o644))
	ctx := std.WithCwd(context.Background(), dir)

	op := spells.Op{Command: spells.Command{Bin: "pnpm", Args: []string{"audit"}}}
	_, err := runCommand(ctx, op, commandOpts{})
	require.NoError(t, err)
	assert.Equal(t, "pnpm audit", invoked(),
		"no opt-in (empty pmBin) must fork the recorded bin untouched")
}

// TestRunCommandSubstitutionDefaultsToRecordedBin proves the fallback: a
// project with no manifest field and no lockfile forks the recorded default.
func TestRunCommandSubstitutionDefaultsToRecordedBin(t *testing.T) {
	invoked := stubPackageManagers(t)
	ctx := std.WithCwd(context.Background(), t.TempDir())

	op := spells.Op{Command: spells.Command{Bin: "pnpm", Args: []string{"audit"}}}
	_, err := runCommand(ctx, op, commandOpts{pmBin: "pnpm"})
	require.NoError(t, err)
	assert.Equal(t, "pnpm audit", invoked())
}

// TestRunCommandAppendsInstallHintWhenBinAbsent proves the missing-tool DX: a
// fork that fails because the bin is not on PATH carries the spell's declared
// install hint, while a bin with no hint keeps the bare exited error.
func TestRunCommandAppendsInstallHintWhenBinAbsent(t *testing.T) {
	ctx := std.WithCwd(context.Background(), t.TempDir())
	op := spells.Op{Command: spells.Command{Bin: "definitely-not-a-real-tool-xyz", Args: []string{"--version"}}}

	t.Run("declared hint is appended", func(t *testing.T) {
		hints := map[string]string{"definitely-not-a-real-tool-xyz": "mise use -g definitely-not-a-real-tool-xyz"}
		_, err := runCommand(ctx, op, commandOpts{installHints: hints})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not found on PATH")
		assert.Contains(t, err.Error(), "install: mise use -g definitely-not-a-real-tool-xyz")
	})
	t.Run("no hint keeps the bare error", func(t *testing.T) {
		_, err := runCommand(ctx, op, commandOpts{})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "install:")
	})
}

// TestRunCommandSupervisesServiceDependency proves runCommand routes a service op to
// the supervisor (not a foreground fork) when supervision is active - the case of a
// service reached via magus.needs.
func TestRunCommandSupervisesServiceDependency(t *testing.T) {
	rr := &recordRunner{}
	sess := service.NewSession(service.New(rr, 0), nil, nil)
	ctx := service.WithSupervision(service.WithSession(context.Background(), sess))

	_, err := runCommand(ctx, serviceOp(), commandOpts{})
	require.NoError(t, err)
	assert.Equal(t, 1, rr.started, "service dependency should be supervised, not forked")
}

// TestRunCommandForegroundsDirectService proves a service op with no active
// supervision falls through to a real fork (the directly-run case), and is not
// handed to the supervisor.
func TestRunCommandForegroundsDirectService(t *testing.T) {
	rr := &recordRunner{}
	sess := service.NewSession(service.New(rr, 0), nil, nil)
	// Session present but supervision NOT active: this is a directly-run service.
	ctx := service.WithSession(context.Background(), sess)

	_, err := runCommand(ctx, serviceOp(), commandOpts{})
	require.NoError(t, err) // `true` exits 0
	assert.Equal(t, 0, rr.started, "directly-run service must foreground, not be supervised")
}
