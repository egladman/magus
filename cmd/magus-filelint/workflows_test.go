package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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
	assert.Empty(t, trustedJobsRestoreNoActionsCache(fsys), "the queue-apply job restores no Actions cache")
}

// ci.yaml builds magus once and later jobs install that binary. The upload must
// precede any cache extraction that could overwrite it, and every consumer must
// verify the digest its publisher recorded.
func TestPublishedMagusIsBuiltFirstAndVerifiedByDigest(t *testing.T) {
	type step struct {
		ID   string            `yaml:"id"`
		Uses string            `yaml:"uses"`
		Run  string            `yaml:"run"`
		With map[string]string `yaml:"with"`
	}
	var action struct {
		Runs struct {
			Steps []step `yaml:"steps"`
		} `yaml:"runs"`
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot, setupMagusAction))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &action))
	publish, restore := -1, -1
	for i, s := range action.Runs.Steps {
		if s.ID == "publish" {
			publish = i
		}
		if strings.HasPrefix(s.Uses, "actions/cache/restore@") {
			restore = i
		}
	}
	require.NotEqual(t, -1, publish, "setup-magus publishes the binary it built")
	require.NotEqual(t, -1, restore, "setup-magus restores the run history")
	assert.Less(t, publish, restore, "the binary is uploaded before a cache entry is extracted")

	outputRe := regexp.MustCompile(`^\$\{\{ needs\.([\w-]+)\.outputs\.([\w-]+) \}\}$`)
	paths, err := filepath.Glob(filepath.Join(repoRoot, ".github", "workflows", "*.yaml"))
	require.NoError(t, err)
	published := 0
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var wf struct {
			Jobs map[string]struct {
				Needs   yaml.Node         `yaml:"needs"`
				Outputs map[string]string `yaml:"outputs"`
				Steps   []step            `yaml:"steps"`
			} `yaml:"jobs"`
		}
		require.NoError(t, yaml.Unmarshal(raw, &wf), path)
		file := filepath.Base(path)
		for name, job := range wf.Jobs {
			for i, s := range job.Steps {
				if s.Uses != "./.github/actions/setup-magus" {
					continue
				}
				if s.With["publish-artifact"] != "" {
					published++
					for _, prior := range job.Steps[:i] {
						assert.True(t, strings.HasPrefix(prior.Uses, "actions/checkout@"),
							"%s/%s: %q runs before the magus it publishes is built", file, name, prior.Uses+prior.Run)
					}
				}
				if s.With["installation-strategy"] != "artifact" {
					continue
				}
				m := outputRe.FindStringSubmatch(s.With["artifact-sha256"])
				require.NotNil(t, m, "%s/%s: artifact-sha256 is a job output of the publisher", file, name)
				var needs []string
				if job.Needs.Kind == yaml.ScalarNode {
					needs = []string{job.Needs.Value}
				} else {
					require.NoError(t, job.Needs.Decode(&needs))
				}
				assert.Contains(t, needs, m[1], "%s/%s", file, name)
				var producer string
				for _, p := range wf.Jobs[m[1]].Steps {
					if p.Uses == "./.github/actions/setup-magus" && p.With["publish-artifact"] != "" {
						producer = p.ID
					}
				}
				require.NotEmpty(t, producer, "%s/%s: %s publishes magus from a step with an id", file, name, m[1])
				assert.Equal(t, "${{ steps."+producer+".outputs.sha256 }}", wf.Jobs[m[1]].Outputs[m[2]], "%s/%s", file, name)
				assert.Equal(t, "${{ github.sha }}", s.With["artifact-commit"], "%s/%s", file, name)
			}
		}
	}
	assert.Positive(t, published, "ci.yaml builds magus once and shares it")
}
