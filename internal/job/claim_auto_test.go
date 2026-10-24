package job

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const autoCaller = "internal/guard/render.go"

const autoCallerProof = "internal/guard/render.go references hint.Render, which the job's diff changes in internal/hint/cli_command.go"

func leafFanIn(string, []string) (int, error) { return 0, nil }

// provedCaller is the decision for autoCaller with stop, which a holder may still apply.
func provedCaller(stop string) AutoClaim {
	return AutoClaim{Request: types.JobClaimRequest{Path: autoCaller, Because: "refs:hint.Render"}, Proof: autoCallerProof, Proved: true, Appliable: true, Stop: stop}
}

type autoClaimCase struct {
	path  string
	extra map[string]string
	edit  func(row *types.Job, rows *[]types.Job, projects *[]*types.Project)
	fanIn fanInFunc
	want  AutoClaim
}

// decideEach runs each case against claimTree, plus the case's extra files and edits.
func decideEach(t *testing.T, cases map[string]autoClaimCase) {
	t.Helper()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root, row, seen, projects := claimTree(t)
			for rel, body := range tc.extra {
				p := filepath.Join(root, filepath.FromSlash(rel))
				require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
				require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
			}
			rows := []types.Job{row}
			if tc.edit != nil {
				tc.edit(&row, &rows, &projects)
				rows[0] = row
			}
			facts := readClaimFacts(root, row, seen, []string{tc.path}, projects)

			assert.Equal(t, tc.want, DecideAutoClaim(row, tc.path, facts, rows, projects, tc.fanIn))
		})
	}
}

func TestDecideAutoClaimTakesOneEdgeAndNoHub(t *testing.T) {
	t.Parallel()

	decideEach(t, map[string]autoClaimCase{
		"a caller of a declaration the diff changed is claimed": {
			path: autoCaller, fanIn: leafFanIn, want: provedCaller(""),
		},
		"a caller of that caller is two edges away": {
			path: "cmd/tool/use.go",
			extra: map[string]string{
				"internal/guard/wrap.go": "package guard\n\nimport \"example.com/m/internal/hint\"\n\nfunc Wrap() string { return hint.Render() }\n",
				"cmd/tool/use.go":        "package tool\n\nimport \"example.com/m/internal/guard\"\n\nvar _ = guard.Wrap()\n",
			},
			fanIn: leafFanIn,
			want:  AutoClaim{Stop: "cmd/tool/use.go is more than one edge from what the job changed: no refs, calls, defines or test proof holds for it"},
		},
		"a hub is proved and left to the holder": {
			path:  autoCaller,
			fanIn: func(string, []string) (int, error) { return hubFanIn, nil },
			want:  provedCaller("internal/guard/render.go is a hub: 25 files use what it declares, at or past 25"),
		},
		"an unmeasured fan-in stops the claim": {
			path: autoCaller,
			fanIn: func(string, []string) (int, error) {
				return 0, errors.New("the symbol index holds no record of internal/guard/render.go")
			},
			want: provedCaller("the symbol index holds no record of internal/guard/render.go"),
		},
	})
}

// widerClaim is the decision for path proved by because: claimed once types names
// because's kind, and until then unproved, since ProposeClaim skips a kind the store
// cannot record.
func widerClaim(path, because, proof string) AutoClaim {
	if _, err := types.ParseClaimEvidence(because); err != nil {
		return AutoClaim{Stop: path + " is more than one edge from what the job changed: no refs, calls, defines or test proof holds for it"}
	}
	return AutoClaim{Request: types.JobClaimRequest{Path: path, Because: because}, Proof: proof, Proved: true, Appliable: true}
}

func TestDecideAutoClaimTakesTheWiderEdges(t *testing.T) {
	t.Parallel()

	decideEach(t, map[string]autoClaimCase{
		"a test of a file the job holds and left alone": {
			path: "internal/lease/held_test.go", fanIn: leafFanIn,
			want: widerClaim("internal/lease/held_test.go", "test:internal/lease/held.go", "internal/lease/held_test.go tests internal/lease/held.go: it names Held"),
		},
		"a test of a file the job claimed": {
			path: "internal/free/free_test.go", fanIn: leafFanIn,
			edit: func(row *types.Job, _ *[]types.Job, _ *[]*types.Project) {
				row.WritePaths = append(row.WritePaths, "internal/free/free.go")
				row.Claims = []types.JobClaim{{Path: "internal/free/free.go"}}
			},
			want: widerClaim("internal/free/free_test.go", "test:internal/free/free.go", "internal/free/free_test.go tests internal/free/free.go: it names Free"),
		},
		"a caller of a method whose signature the diff changed": {
			path: "internal/guard/print.go", fanIn: leafFanIn,
			want: widerClaim("internal/guard/print.go", "calls:Print", "internal/guard/print.go calls Print, whose signature the job's diff changes in internal/hint/printer.go"),
		},
		"the declaration of a name a changed file uses": {
			path: "internal/vcsutil/quote.go", fanIn: leafFanIn,
			want: widerClaim("internal/vcsutil/quote.go", "defines:vcsutil.Quote", "internal/vcsutil/quote.go declares Quote, which cmd/magus/vcs.go, a file the job changed, uses"),
		},
		"a caller of a method whose body alone the diff changed": {
			path: "internal/guard/flush.go", fanIn: leafFanIn,
			want: AutoClaim{Stop: "internal/guard/flush.go is more than one edge from what the job changed: no refs, calls, defines or test proof holds for it"},
		},
		"a test of a file the job does not hold": {
			path: "internal/free/free_test.go", fanIn: leafFanIn,
			want: AutoClaim{Stop: "internal/free/free_test.go is more than one edge from what the job changed: no refs, calls, defines or test proof holds for it"},
		},
	})
}

// A wider edge stops where the first cut's do: a hub, a generated output, another job's
// path and the cap.
func TestDecideAutoClaimStopsTheWiderEdges(t *testing.T) {
	t.Parallel()

	const test = "internal/lease/held_test.go"
	stopped := func(stop string, appliable bool) AutoClaim {
		return AutoClaim{Request: types.JobClaimRequest{Path: test, Because: "test:internal/lease/held.go"},
			Proof: "internal/lease/held_test.go tests internal/lease/held.go: it names Held", Proved: true, Appliable: appliable, Stop: stop}
	}
	decideEach(t, map[string]autoClaimCase{
		"a hub": {
			path: test, fanIn: func(string, []string) (int, error) { return hubFanIn, nil },
			want: stopped("internal/lease/held_test.go is a hub: 25 files use what it declares, at or past 25", true),
		},
		"a generated output": {
			path: test, fanIn: leafFanIn,
			edit: func(_ *types.Job, _ *[]types.Job, projects *[]*types.Project) {
				(*projects)[0].TargetOutputs["lease-generate"] = []types.OutputRef{{Glob: test}}
			},
			want: stopped("internal/lease/held_test.go is a generated output, which its target regenerates", true),
		},
		"a path another live job holds": {
			path: test, fanIn: leafFanIn,
			edit: func(_ *types.Job, rows *[]types.Job, _ *[]*types.Project) {
				*rows = append(*rows, types.Job{ID: "leaser", State: types.StateRunning, WritePaths: []string{test}})
			},
			want: stopped("["+string(types.WritePathFileShared)+"] internal/lease/held_test.go is held by live job leaser, and a proof never takes a path another job holds", false),
		},
		"a job at its cap": {
			path: test, fanIn: leafFanIn,
			edit: func(row *types.Job, _ *[]types.Job, _ *[]*types.Project) {
				row.MaxClaims = 1
				row.Claims = []types.JobClaim{{Path: "cmd/magus/vcs_test.go"}}
			},
			want: stopped("cli-ls-files holds 1 proved claim(s), its max_claims (max_claims on job cli-ls-files)", false),
		},
	})
}

func TestDecideAutoClaimLeavesGeneratedAndConfigFilesToTheHolder(t *testing.T) {
	t.Parallel()

	decideEach(t, map[string]autoClaimCase{
		"a generated output is proved and left to the holder": {
			path: "cmd/magus/gen/flags.go", fanIn: leafFanIn,
			want: AutoClaim{Request: types.JobClaimRequest{Path: "cmd/magus/gen/flags.go", Because: "output:.:config-generate"},
				Proof: "cmd/magus/gen/flags.go is an output of .:config-generate, which reads internal/commands/*.go, a source the job changed", Proved: true, Appliable: true,
				Stop: "cmd/magus/gen/flags.go is a generated output, which its target regenerates"},
		},
		"a caller a target outputs is generated": {
			path: autoCaller, fanIn: leafFanIn,
			edit: func(_ *types.Job, _ *[]types.Job, projects *[]*types.Project) {
				(*projects)[0].TargetOutputs["render-generate"] = []types.OutputRef{{Glob: autoCaller}}
			},
			want: provedCaller("internal/guard/render.go is a generated output, which its target regenerates"),
		},
		"no workspace leaves a generated output possible": {
			path: autoCaller, fanIn: leafFanIn,
			edit: func(_ *types.Job, _ *[]types.Job, projects *[]*types.Project) { *projects = nil },
			want: provedCaller("no workspace answered for its targets' outputs, so internal/guard/render.go may be generated"),
		},
		"a magusfile is proved and left to the holder": {
			path: "cmd/magus/testdata/magusfile.buzz", extra: map[string]string{"cmd/magus/testdata/magusfile.buzz": "exec magus vcs ls-files\n"}, fanIn: leafFanIn,
			want: AutoClaim{Request: types.JobClaimRequest{Path: "cmd/magus/testdata/magusfile.buzz", Because: "test:cmd/magus/vcs.go"},
				Proof: "cmd/magus/testdata/magusfile.buzz tests cmd/magus/vcs.go: it names \"ls-files\"", Proved: true, Appliable: true,
				Stop: "cmd/magus/testdata/magusfile.buzz is a magusfile or workspace config, which every job's plan reads"},
		},
	})
}

// A path another job holds, or one past the cap, is refused by `magus job apply` too, so
// the holder is not sent there.
func TestDecideAutoClaimStopsWhatTheStoreWouldRefuse(t *testing.T) {
	t.Parallel()

	refused := provedCaller("")
	refused.Appliable = false
	held, capped := refused, refused
	held.Stop = "[" + string(types.WritePathFileShared) + "] internal/guard/render.go is held by live job renderer, and a proof never takes a path another job holds"
	capped.Stop = "cli-ls-files holds 1 proved claim(s), its max_claims (max_claims on job cli-ls-files)"
	decideEach(t, map[string]autoClaimCase{
		"a path another live job holds": {
			path: autoCaller, fanIn: leafFanIn, want: held,
			edit: func(_ *types.Job, rows *[]types.Job, _ *[]*types.Project) {
				*rows = append(*rows, types.Job{ID: "renderer", State: types.StateRunning, WritePaths: []string{autoCaller}})
			},
		},
		"a job at its cap": {
			path: autoCaller, fanIn: leafFanIn, want: capped,
			edit: func(row *types.Job, _ *[]types.Job, _ *[]*types.Project) {
				row.MaxClaims = 1
				row.Claims = []types.JobClaim{{Path: "cmd/magus/vcs_test.go"}}
			},
		},
	})
}
