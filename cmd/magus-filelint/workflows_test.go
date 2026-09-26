package main

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupMagusMintsTheQueueAppTokenOnlyAsAnOutput(t *testing.T) {
	assert.Empty(t, setupMagusMintsTheQueueAppTokenOnlyAsAnOutput(repoFS(t, setupMagusAction, queueApplyFlow)))

	for _, tc := range []struct {
		name, path, old, replacement, want string
	}{
		{"a credential input", setupMagusAction, "\ninputs:\n", "\ninputs:\n  app-private-key:\n    description: x\n",
			"input app-private-key carries a credential"},
		{"a default client id", setupMagusAction, "    default: ''\n\noutputs:", "    default: abc\n\noutputs:",
			"queue-app-client-id has a default"},
		{"an unpinned mint", setupMagusAction, "actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1",
			"actions/create-github-app-token@v3", "actions/create-github-app-token@v3 is not pinned by full commit"},
		{"a token in GITHUB_ENV", setupMagusAction, "\n  steps:\n", "\n  steps:\n    - name: leak\n      shell: bash\n      run: echo \"queue=x\" >> \"$GITHUB_ENV\"\n",
			`step "leak" writes the queue app's credential to $GITHUB_ENV`},
		{"dispatch outside the environment", queueApplyFlow, "    environment: magus-queue\n", "\n",
			"job dispatch runs outside the magus-queue environment"},
		{"the job token", queueApplyFlow, "\nenv:\n", "\nenv:\n  GH_TOKEN: x\n", "names GH_TOKEN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := repoFS(t, setupMagusAction, queueApplyFlow)
			seed(t, fsys, tc.path, tc.old, tc.replacement)
			assert.Contains(t, problems(setupMagusMintsTheQueueAppTokenOnlyAsAnOutput(fsys)), tc.want)
		})
	}
}

// trustedFlow is a workflow whose one job holds a write token and restores
// through step.
func trustedFlow(step string) fstest.MapFS {
	return fstest.MapFS{".github/workflows/release.yaml": {Data: []byte(
		"on: push\npermissions:\n  contents: write\njobs:\n  build:\n    runs-on: x\n    steps:\n" + step)}}
}

func TestTrustedJobsRestoreNoActionsCache(t *testing.T) {
	saved := cacheRestoreExemptions
	t.Cleanup(func() { cacheRestoreExemptions = saved })
	cacheRestoreExemptions = map[string]string{}

	for name, step := range map[string]string{
		"restore off":         "      - uses: jdx/mise-action@v3\n        with:\n          cache: false\n",
		"off by default":      "      - uses: ./.github/actions/setup-magus\n",
		"no restoring action": "      - run: echo hi\n",
		"no steps":            "",
	} {
		t.Run("clean/"+name, func(t *testing.T) {
			assert.Empty(t, trustedJobsRestoreNoActionsCache(trustedFlow(step)))
		})
	}
	readOnly := fstest.MapFS{".github/workflows/pr.yaml": {Data: []byte(
		"on: pull_request\npermissions:\n  contents: read\njobs:\n  test:\n    steps:\n      - uses: actions/cache@v4\n")}}
	assert.Empty(t, trustedJobsRestoreNoActionsCache(readOnly), "a read-only job holding no secret may restore")
	gated := fstest.MapFS{".github/workflows/ci.yaml": {Data: []byte(
		"on: [push, pull_request]\npermissions:\n  contents: read\njobs:\n  test:\n    env:\n" +
			"      TOKEN: ${{ github.event_name != 'pull_request' && secrets.CODECOV || '' }}\n    steps:\n" +
			"      - uses: jdx/mise-action@v3\n        with:\n          cache: ${{ github.event_name == 'pull_request' }}\n")}}
	assert.Empty(t, trustedJobsRestoreNoActionsCache(gated), "a secret gated off pull requests allows a pull-request-only restore")

	got := trustedJobsRestoreNoActionsCache(trustedFlow("      - uses: jdx/mise-action@v3\n"))
	require.Len(t, got, 1)
	assert.Equal(t, ".github/workflows/release.yaml", got[0].path)
	assert.Equal(t, 8, got[0].line)
	assert.Equal(t, `release.yaml/build holds a secret, a write token or id-token (or is the queue), and restores an Actions cache: jdx/mise-action@v3 (cache: "")`, got[0].problem)

	queue := fstest.MapFS{".github/workflows/queue.yaml": {Data: []byte(
		"on: push\npermissions:\n  contents: read\njobs:\n  validate:\n    steps:\n      - uses: actions/cache/restore@v4\n")}}
	assert.Len(t, trustedJobsRestoreNoActionsCache(queue), 1, "the queue restores nothing whatever it holds")
}

func TestTrustedJobsRestoreNoActionsCacheHoldsExemptionsToTheirJob(t *testing.T) {
	saved := cacheRestoreExemptions
	t.Cleanup(func() { cacheRestoreExemptions = saved })
	cacheRestoreExemptions = map[string]string{"release.yaml/build": "a reason"}

	assert.Empty(t, trustedJobsRestoreNoActionsCache(trustedFlow("      - uses: jdx/mise-action@v3\n")))
	assert.Equal(t, []string{"release.yaml/build no longer restores a cache"},
		problems(trustedJobsRestoreNoActionsCache(trustedFlow("      - run: echo hi\n"))))

	cacheRestoreExemptions = map[string]string{"gone.yaml/build": "a reason"}
	assert.Equal(t, []string{"exemption gone.yaml/build names no trusted job"},
		problems(trustedJobsRestoreNoActionsCache(trustedFlow("      - run: echo hi\n"))))
}

func TestTrustedJobsRestoreNoActionsCachePassesTheTree(t *testing.T) {
	fsys := repoFS(t, ".github/workflows/queue-apply.yaml")
	assert.Empty(t, trustedJobsRestoreNoActionsCache(fsys), "the queue-apply exemption still matches its job")
}
