package job

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

type isolationCase struct {
	job    isolationJob
	peers  []types.Job
	risk   float64
	weight float64
	level  int
	reason string
}

var (
	isolationWriter = types.Job{ID: "w", WritePaths: []string{"docs/a.md"}}
	isolationReader = types.Job{ID: "r", ReadOnly: true}
	isolationPeer   = types.Job{ID: "peer", State: types.StateRunning, WritePaths: []string{"docs/b.md"}}
)

func assertIsolation(t *testing.T, cases map[string]isolationCase) {
	t.Helper()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			level, reason := chooseIsolation(tc.job, tc.peers, tc.risk, tc.weight)
			assert.Equal(t, tc.level, level, reason)
			assert.Contains(t, reason, tc.reason)
			assert.NotEmpty(t, reason, "every level says why")
		})
	}
}

func TestChooseIsolationHardSignalsIgnoreTheDials(t *testing.T) {
	t.Parallel()
	writer, peers := isolationWriter, []types.Job{isolationPeer}
	shared := types.JobOverlap{JobA: "peer", JobB: "w", PathsA: []string{"docs/a.md"}, PathsB: []string{"docs/a.md"}}
	sharedClaims := types.JobOverlap{JobA: "w", JobB: "peer", PathsA: []string{"run.go#a"}, PathsB: []string{"run.go#a"}, Claims: types.ClaimsShared}
	disjoint := types.JobOverlap{JobA: "w", JobB: "peer", PathsA: []string{"run.go#a"}, PathsB: []string{"run.go#b"}, Claims: types.ClaimsDisjoint}
	assertIsolation(t, map[string]isolationCase{
		"a binary input builds its own binary": {
			job: isolationJob{Row: writer, BinaryInputs: []string{"internal/job/worktree.go"}}, level: isolationOwnBuild,
			reason: "internal/job/worktree.go is a build input of ./cmd/magus",
		},
		"a binary input holds at any risk tolerance and weight": {
			job: isolationJob{Row: writer, BinaryInputs: []string{"cmd/magus/main.go"}}, risk: 1, weight: 0.1, level: isolationOwnBuild,
		},
		"a binary input outranks a generator output": {
			job:   isolationJob{Row: writer, BinaryInputs: []string{"types/job.go"}, GeneratorOutputs: []string{"internal/job/gen/job.schema.json"}},
			level: isolationOwnBuild, reason: "types/job.go",
		},
		"a generator output gets its own checkout at any risk tolerance": {
			job: isolationJob{Row: writer, GeneratorOutputs: []string{"console/gen/x.ts"}}, risk: 1, weight: 0.1, level: isolationOwnCheckout,
			reason: "console/gen/x.ts is a generator's output",
		},
		"an overlap with no claim gets its own checkout": {
			job: isolationJob{Row: writer, Overlaps: []types.JobOverlap{shared}}, peers: peers, risk: 1, level: isolationOwnCheckout,
			reason: "overlap peer's (docs/a.md) with no disjoint claim",
		},
		"an overlap on one claimed declaration gets its own checkout": {
			job: isolationJob{Row: writer, Overlaps: []types.JobOverlap{sharedClaims}}, peers: peers, risk: 1, level: isolationOwnCheckout,
			reason: "overlap peer's (run.go#a)",
		},
		"disjoint claims settle at integration": {
			job: isolationJob{Row: writer, Overlaps: []types.JobOverlap{disjoint}}, peers: peers, risk: 0.7, level: isolationShared,
		},
		"a load file with a live peer leaves the shared checkout": {
			job: isolationJob{Row: writer, LoadFiles: []string{"magusfile.buzz"}}, peers: peers, risk: 0.7, level: isolationWave,
			reason: "magusfile.buzz covers a file the workspace loads, and peer is live",
		},
		"a load file with no live peer shares": {
			job: isolationJob{Row: writer, LoadFiles: []string{"magusfile.buzz"}}, risk: 0.7, level: isolationShared,
		},
		"a load file does not lower a writer's own checkout": {
			job: isolationJob{Row: writer, LoadFiles: []string{"magus.yaml"}}, peers: peers, level: isolationOwnCheckout,
		},
	})
}

func TestChooseIsolationSoftSignalsMoveAWriter(t *testing.T) {
	t.Parallel()
	writer, reader := isolationWriter, isolationReader
	wide := types.Job{ID: "w", WritePaths: make([]string, largeWriteSet+1)}
	for i := range wide.WritePaths {
		wide.WritePaths[i] = fmt.Sprintf("docs/%d.md", i)
	}
	assertIsolation(t, map[string]isolationCase{
		"a read-only job shares the checkout":            {job: isolationJob{Row: reader}, level: isolationShared, reason: "read-only"},
		"a writer gets its own checkout":                 {job: isolationJob{Row: writer}, level: isolationOwnCheckout, reason: "its own branch"},
		"risk tolerance 0.5 lowers a writer to the wave": {job: isolationJob{Row: writer}, risk: 0.5, level: isolationWave, reason: "risk_tolerance 0.5"},
		"risk tolerance 0.7 shares the checkout":         {job: isolationJob{Row: writer}, risk: 0.7, level: isolationShared, reason: "risk_tolerance 0.7"},
		"risk tolerance below 0.5 keeps today's level":   {job: isolationJob{Row: writer}, risk: 0.49, level: isolationOwnCheckout},
		"a light weight lowers a writer to the wave":     {job: isolationJob{Row: writer}, weight: 0.3, level: isolationWave, reason: "weight 0.3"},
		"a heavier weight keeps the own checkout":        {job: isolationJob{Row: writer}, weight: 0.31, level: isolationOwnCheckout},
		"weight 0 is unweighted":                         {job: isolationJob{Row: writer}, weight: 0, level: isolationOwnCheckout},
		"a large write set raises a lowered writer": {
			job: isolationJob{Row: wide}, risk: 0.7, level: isolationOwnCheckout, reason: "28 write paths, past the 27",
		},
		"a write set at the p90 stays lowered": {
			job: isolationJob{Row: types.Job{ID: "w", WritePaths: wide.WritePaths[:largeWriteSet]}}, risk: 0.7, level: isolationShared,
		},
		"an unmerged dependency raises a lowered writer": {
			job: isolationJob{Row: writer, UnmergedDependency: "base-job"}, weight: 0.2, level: isolationOwnCheckout,
			reason: "depends_on base-job is not merged",
		},
		"an unmerged dependency raises a reader": {
			job: isolationJob{Row: reader, UnmergedDependency: "base-job"}, level: isolationOwnCheckout, reason: "its own base",
		},
	})
}
