package job

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

func TestChooseIsolation(t *testing.T) {
	t.Parallel()
	writer := types.Job{ID: "w", WritePaths: []string{"docs/a.md"}}
	reader := types.Job{ID: "r", ReadOnly: true}
	peer := types.Job{ID: "peer", State: types.StateRunning, WritePaths: []string{"docs/b.md"}}
	wide := types.Job{ID: "w", WritePaths: make([]string, largeWriteSet+1)}
	for i := range wide.WritePaths {
		wide.WritePaths[i] = fmt.Sprintf("docs/%d.md", i)
	}
	shared := types.JobOverlap{JobA: "peer", JobB: "w", PathsA: []string{"docs/a.md"}, PathsB: []string{"docs/a.md"}}
	sharedClaims := types.JobOverlap{JobA: "w", JobB: "peer", PathsA: []string{"run.go#a"}, PathsB: []string{"run.go#a"}, Claims: types.ClaimsShared}
	disjoint := types.JobOverlap{JobA: "w", JobB: "peer", PathsA: []string{"run.go#a"}, PathsB: []string{"run.go#b"}, Claims: types.ClaimsDisjoint}

	for name, tc := range map[string]struct {
		job    IsolationJob
		peers  []types.Job
		risk   float64
		weight float64
		level  int
		reason string
	}{
		"a read-only job shares the checkout": {job: IsolationJob{Row: reader}, level: IsolationShared, reason: "read-only"},
		"a writer gets its own checkout":      {job: IsolationJob{Row: writer}, level: IsolationOwnCheckout, reason: "its own branch"},

		"a binary input builds its own binary": {
			job: IsolationJob{Row: writer, BinaryInputs: []string{"internal/job/worktree.go"}}, level: IsolationOwnBuild,
			reason: "internal/job/worktree.go is a build input of ./cmd/magus",
		},
		"a binary input holds at any risk tolerance and weight": {
			job: IsolationJob{Row: writer, BinaryInputs: []string{"cmd/magus/main.go"}}, risk: 1, weight: 0.1, level: IsolationOwnBuild,
			reason: "cmd/magus/main.go is a build input",
		},
		"a binary input outranks a generator output": {
			job:   IsolationJob{Row: writer, BinaryInputs: []string{"types/job.go"}, GeneratorOutputs: []string{"internal/job/gen/job.schema.json"}},
			level: IsolationOwnBuild, reason: "types/job.go",
		},
		"a generator output gets its own checkout at any risk tolerance": {
			job: IsolationJob{Row: writer, GeneratorOutputs: []string{"console/gen/x.ts"}}, risk: 1, weight: 0.1, level: IsolationOwnCheckout,
			reason: "console/gen/x.ts is a generator's output",
		},
		"an overlap with no claim gets its own checkout": {
			job: IsolationJob{Row: writer, Overlaps: []types.JobOverlap{shared}}, peers: []types.Job{peer}, risk: 1, level: IsolationOwnCheckout,
			reason: "overlap peer's (docs/a.md) with no disjoint claim",
		},
		"an overlap on one claimed declaration gets its own checkout": {
			job: IsolationJob{Row: writer, Overlaps: []types.JobOverlap{sharedClaims}}, peers: []types.Job{peer}, risk: 1, level: IsolationOwnCheckout,
			reason: "overlap peer's (run.go#a)",
		},
		"disjoint claims settle at integration": {
			job: IsolationJob{Row: writer, Overlaps: []types.JobOverlap{disjoint}}, peers: []types.Job{peer}, risk: 0.7, level: IsolationShared,
			reason: "disjoint writer",
		},
		"a load file with a live peer leaves the shared checkout": {
			job: IsolationJob{Row: writer, LoadFiles: []string{"magusfile.buzz"}}, peers: []types.Job{peer}, risk: 0.7, level: IsolationWave,
			reason: "magusfile.buzz covers a file the workspace loads, and peer is live",
		},
		"a load file with no live peer shares": {
			job: IsolationJob{Row: writer, LoadFiles: []string{"magusfile.buzz"}}, risk: 0.7, level: IsolationShared,
		},
		"a load file does not lower a writer's own checkout": {
			job: IsolationJob{Row: writer, LoadFiles: []string{"magus.yaml"}}, peers: []types.Job{peer}, level: IsolationOwnCheckout,
		},

		"risk tolerance 0.5 lowers a writer to the wave": {job: IsolationJob{Row: writer}, risk: 0.5, level: IsolationWave, reason: "risk_tolerance 0.5"},
		"risk tolerance 0.7 shares the checkout":         {job: IsolationJob{Row: writer}, risk: 0.7, level: IsolationShared, reason: "risk_tolerance 0.7"},
		"risk tolerance below 0.5 keeps today's level":   {job: IsolationJob{Row: writer}, risk: 0.49, level: IsolationOwnCheckout},
		"a light weight lowers a writer to the wave":     {job: IsolationJob{Row: writer}, weight: 0.3, level: IsolationWave, reason: "weight 0.3"},
		"a heavier weight keeps the own checkout":        {job: IsolationJob{Row: writer}, weight: 0.31, level: IsolationOwnCheckout},
		"weight 0 is unweighted":                         {job: IsolationJob{Row: writer}, weight: 0, level: IsolationOwnCheckout},

		"a large write set raises a lowered writer": {
			job: IsolationJob{Row: wide}, risk: 0.7, level: IsolationOwnCheckout, reason: "28 write paths, past the 27",
		},
		"a write set at the p90 stays lowered": {
			job: IsolationJob{Row: types.Job{ID: "w", WritePaths: wide.WritePaths[:largeWriteSet]}}, risk: 0.7, level: IsolationShared,
		},
		"an unmerged dependency raises a lowered writer": {
			job: IsolationJob{Row: writer, UnmergedDependency: "base-job"}, weight: 0.2, level: IsolationOwnCheckout,
			reason: "depends_on base-job is not merged",
		},
		"an unmerged dependency raises a reader": {
			job: IsolationJob{Row: reader, UnmergedDependency: "base-job"}, level: IsolationOwnCheckout, reason: "its own base",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			level, reason := ChooseIsolation(tc.job, tc.peers, tc.risk, tc.weight)
			assert.Equal(t, tc.level, level, reason)
			assert.Contains(t, reason, tc.reason)
			assert.NotEmpty(t, reason, "every level says why")
		})
	}
}
