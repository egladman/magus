//go:build !wasm

package std

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// The dir named here does not exist, so a refusal that came after the process started
// would fail differently: the remedy is the whole error, and nothing ran.
func TestOsExecRefusesAVCSBinary(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		args []string
		dir  string
		want string
	}{
		{
			name: "by name",
			cmd:  "git", args: []string{"commit", "-m", "x"},
			want: `proc\exec git: git runs only through vcs\cmd, which holds it to the lease rules: call vcs\cmd(["commit", "-m", "x"]) instead`,
		},
		{
			name: "by absolute path, with a dir",
			cmd:  "/usr/bin/git", args: []string{"status", "--short"}, dir: "no/such/dir",
			want: `proc\exec /usr/bin/git: git runs only through vcs\cmd, which holds it to the lease rules: call vcs\cmd(["status", "--short"], {"dir": "no/such/dir"}) instead`,
		},
		{
			name: "another backend",
			cmd:  "jj", args: []string{"describe", "-m", "x"},
			want: `proc\exec jj: jj runs only through vcs\cmd, which holds it to the lease rules: call vcs\cmd(["describe", "-m", "x"]) instead`,
		},
		{
			name: "a compound shell line",
			cmd:  "/bin/sh", args: []string{"-c", "cd sub && git commit -m 'a b'"},
			want: `proc\exec /bin/sh: git runs only through vcs\cmd, which holds it to the lease rules: call vcs\cmd(["commit", "-m", "a b"]) instead`,
		},
		{
			name: "behind a wrapper",
			cmd:  "env", args: []string{"-u", "HOME", "GIT_DIR=x", "sl", "commit"},
			want: `proc\exec env: sl runs only through vcs\cmd, which holds it to the lease rules: call vcs\cmd(["commit"]) instead`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := OsExec(t.Context(), tc.cmd, tc.args, tc.dir, nil)
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestOsExecRunsAShellLineThatOnlyMentionsGit(t *testing.T) {
	res, err := OsExec(t.Context(), "/bin/sh", []string{"-c", "echo git"}, "", map[string]any{"quiet": true})
	require.NoError(t, err)
	assert.Equal(t, "git", res.Stdout)
}

func TestArgvCommands(t *testing.T) {
	cases := []struct {
		argv []string
		want []hint.Invocation
	}{
		{[]string{"/usr/bin/git", "status"}, []hint.Invocation{{Name: "git", Args: []string{"status"}}}},
		{[]string{"timeout", "-s", "KILL", "5", "git", "push"}, []hint.Invocation{{Name: "git", Args: []string{"push"}}}},
		{[]string{"env", "-S", "git stash"}, []hint.Invocation{{Name: "git", Args: []string{"stash"}}}},
		{
			[]string{"bash", "--norc", "-ec", "cd sub; git commit -m \"a b\" | cat"},
			[]hint.Invocation{
				{Name: "cd", Args: []string{"sub"}},
				{Name: "git", Args: []string{"commit", "-m", "a b"}},
				{Name: "cat", Args: []string{}},
			},
		},
		{
			[]string{"find", ".", "-exec", "git", "add", "{}", ";"},
			[]hint.Invocation{{Name: "find", Args: []string{".", "-exec", "git", "add", "{}", ";"}}, {Name: "git", Args: []string{"add", "{}"}}},
		},
		{[]string{"sh", "-c", "echo $(git rev-parse HEAD)"}, []hint.Invocation{
			{Name: "echo", Args: []string{""}},
			{Name: "git", Args: []string{"rev-parse", "HEAD"}},
		}},
		{[]string{"sh", "-c", "git commit -m 'unterminated"}, []hint.Invocation{
			{Name: "git", Args: []string{"commit", "-m", "unterminated"}},
			{Name: "commit", Args: []string{"-m", "unterminated"}},
			{Name: "-m", Args: []string{"unterminated"}},
			{Name: "unterminated", Args: []string{}},
		}},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, argvCommands(tc.argv), tc.argv)
	}
}

func TestVcsCmdStillRunsWhereProcExecIsRefused(t *testing.T) {
	ctx := WithCurrentWorkingDirectory(t.Context(), testkit.NewRepository(t, nil).Root())

	res, err := VcsCommand(ctx, []string{"rev-parse", "--is-inside-work-tree"}, map[string]any{"quiet": true})
	require.NoError(t, err)
	assert.Equal(t, "true", res.Stdout)
}

// Not parallel: the job store resolves the per-repository state directory, and the
// environment is what keeps these rows out of the developer's own store.
func TestVcsCmdIsStillLeaseGraded(t *testing.T) {
	testkit.Isolate(t)
	repo := testkit.NewRepository(t, nil)
	ws := types.WithWorkspace(t.Context(), &ledgerWorkspace{cacheDir: t.TempDir(), root: repo.Root()})
	_, err := MagusPutJob(ws, "worker", types.JobPut{Criteria: new("ship it"), State: new(types.StateRunning), ReadOnly: new(true)})
	require.NoError(t, err)
	refusal := errors.New("lease-vcs: refused")
	withVCSLeaseGate(t, func(context.Context, types.Job, string, []string, string, []string) error { return refusal })

	_, err = VcsCommand(types.WithJobLease(WithCurrentWorkingDirectory(ws, repo.Root()), "worker"), []string{"commit", "--allow-empty", "-m", "x"}, nil)
	assert.ErrorIs(t, err, refusal)
}
