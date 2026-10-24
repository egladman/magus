package guard

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// autoClaimTree is a checkout whose job changed hint.Render after its checkpoint, with one
// file beside it for each condition the auto-claim checks. Each file but the write path
// declares the name its fan-in is counted by.
var autoClaimTree = map[string]string{
	"internal/hint/render.go":   "package hint\n\nfunc Render() string {\n\treturn \"x\"\n}\n",
	"internal/guard/render.go":  "package guard\n\nimport \"example.com/m/internal/hint\"\n\nfunc Show() string { return hint.Render() }\n",
	"cmd/tool/use.go":           "package tool\n\nimport \"example.com/m/internal/guard\"\n\nfunc Use() string { return guard.Show() }\n",
	"internal/hub/hub.go":       "package hub\n\nimport \"example.com/m/internal/hint\"\n\nfunc Hub() string { return hint.Render() }\n",
	"internal/hint/gen/out.go":  "package gen\n\nimport \"example.com/m/internal/hint\"\n\nfunc Out() string { return hint.Render() }\n",
	"internal/held/held.go":     "package held\n\nimport \"example.com/m/internal/hint\"\n\nfunc Held() string { return hint.Render() }\n",
	"internal/capped/capped.go": "package capped\n\nimport \"example.com/m/internal/hint\"\n\nfunc Capped() string { return hint.Render() }\n",
}

// autoClaimFixture checkpoints autoClaimTree for row, changes hint.Render, and seeds row
// and others. Hub is used by 26 files and every other name by one.
func autoClaimFixture(t *testing.T, row types.Job, others ...types.Job) (context.Context, string, Dependencies) {
	t.Helper()
	repo := testkit.NewRepo(t, autoClaimTree)
	row.CheckoutRoot, row.Checkpoint = repo.Root(), repo.Head()
	repo.Write("internal/hint/render.go", "package hint\n\nfunc Render() string {\n\treturn \"y\"\n}\n")
	ctx, _ := fleetFixture(t, append([]types.Job{row}, others...)...)

	ws := typestest.NewMockWorkspaceRepository(t)
	ws.EXPECT().All().Return([]*types.Project{{
		Path:          ".",
		Sources:       types.MustParseGlobs("**/*.go"),
		TargetInputs:  map[string][]types.InputRef{"render-generate": {{Glob: "internal/hint/*.go"}}},
		TargetOutputs: map[string][]types.OutputRef{"render-generate": {{Glob: "internal/hint/gen/*.go"}}},
	}}).Maybe()
	ws.EXPECT().Get(mock.Anything).Return(nil).Maybe()
	ws.EXPECT().ClassifyFiles(mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	ws.EXPECT().TargetGraph(mock.Anything).Return(types.TargetGraphOutput{}, nil).Maybe()
	definers := map[string]string{}
	for rel, body := range autoClaimTree {
		if _, decl, ok := strings.Cut(body, "\nfunc "); ok {
			name, _, _ := strings.Cut(decl, "(")
			definers[name] = rel
		}
	}
	deps := Dependencies{
		Inspect:   func(context.Context, string) (types.WorkspaceRepository, error) { return ws, nil },
		SymbolIDs: func(name string) []string { return []string{"symbol " + name} },
		SymbolSites: func(name string) ([]types.KnowledgeRefSite, bool) {
			sites := []types.KnowledgeRefSite{{File: definers[name]}, {File: "cmd/user/user.go"}}
			if name == "Hub" {
				for i := range 25 {
					sites = append(sites, types.KnowledgeRefSite{File: filepath.Join("cmd", "hubuser", string(rune('a'+i))+".go")})
				}
			}
			return sites, false
		},
	}
	return ctx, repo.Root(), deps
}

func autoClaimRow() types.Job {
	return types.Job{ID: "render-change", Criteria: "change hint.Render", State: types.StateRunning,
		WritePaths: []string{"internal/hint/render.go"}, Registered: 1}
}

func TestGradeLeasedWriteClaimsAFileOneEdgeOut(t *testing.T) {
	row := autoClaimRow()
	ctx, root, deps := autoClaimFixture(t, row)

	got := gradeLeasedWrite(ctx, deps, row.ID, filepath.Join(root, "internal/guard/render.go"))
	assert.Equal(t, writeGrade{Decision: "advise", Why: got.Why,
		Context: "magus workspace: claimed internal/guard/render.go for lease render-change by refs:hint.Render: internal/guard/render.go references hint.Render, which the job's diff changes in internal/hint/render.go.",
	}, got)

	stored, ok := leaseRow(ctx, hookLocation(ctx, deps), row.ID)
	require.True(t, ok)
	assert.Equal(t, []string{"internal/hint/render.go", "internal/guard/render.go"}, stored.WritePaths)
	require.Len(t, stored.Claims, 1)
	assert.Equal(t, types.JobClaim{Path: "internal/guard/render.go", Evidence: types.ClaimEvidence{Kind: types.ClaimRefs, Subject: "hint.Render"},
		Proof: "internal/guard/render.go references hint.Render, which the job's diff changes in internal/hint/render.go",
		At:    stored.Claims[0].At, By: stored.Claims[0].By}, stored.Claims[0])
	assert.NotZero(t, stored.Claims[0].At)

	assert.Equal(t, writeGrade{}, gradeLeasedWrite(ctx, deps, row.ID, filepath.Join(root, "internal/guard/render.go")),
		"the claimed path is the job's own")
}

func TestGradeLeasedWriteDeniesWhatItWillNotClaim(t *testing.T) {
	holder := types.Job{ID: "held-change", Criteria: "own held", State: types.StateRunning,
		WritePaths: []string{"internal/held/held.go"}, Registered: 1}
	for name, tc := range map[string]struct {
		path, stop string
		maxClaims  int
		// before is a write the job claims first.
		before string
		// served says the refusal serves the apply line, which the store would record.
		served bool
	}{
		"two edges out": {
			path: "cmd/tool/use.go",
			stop: "cmd/tool/use.go is more than one edge from what the job changed",
		},
		"a hub": {
			path: "internal/hub/hub.go", served: true,
			stop: "internal/hub/hub.go is a hub: 26 files use what it declares, at or past 25",
		},
		"a generated output": {
			path: "internal/hint/gen/out.go", served: true,
			stop: "internal/hint/gen/out.go is a generated output, which its target regenerates",
		},
		"held by another lease": {
			path: "internal/held/held.go",
			stop: "internal/held/held.go is owned by lease held-change",
		},
		"the cap reached": {
			path:      "internal/capped/capped.go",
			maxClaims: 1, before: "internal/guard/render.go",
			stop: "render-change holds 1 proved claim(s), its max_claims (max_claims on job render-change)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			row := autoClaimRow()
			row.MaxClaims = tc.maxClaims
			ctx, root, deps := autoClaimFixture(t, row, holder)
			if tc.before != "" {
				require.Equal(t, "advise", gradeLeasedWrite(ctx, deps, row.ID, filepath.Join(root, tc.before)).Decision)
			}

			got := gradeLeasedWrite(ctx, deps, row.ID, filepath.Join(root, tc.path))
			want := writeGrade{Decision: "deny", Reason: got.Reason, Next: got.Next}
			if tc.served {
				want.Rule = string(denyRuleLeaseClaim)
			}
			assert.Equal(t, want, got)
			say, _, _ := strings.Cut(got.Reason, "\n")
			assert.Contains(t, say, tc.stop, "the verdict line names what stopped the claim")
			if tc.served {
				require.Len(t, got.Next, 1)
				assert.Contains(t, got.Next[0].Run, `"claim":[{"path":"`+tc.path+`"`)
			} else {
				assert.Empty(t, got.Next, "a line the store would refuse is never served")
			}

			stored, ok := leaseRow(ctx, hookLocation(ctx, deps), row.ID)
			require.True(t, ok)
			assert.False(t, slices.Contains(stored.WritePaths, tc.path), "nothing was claimed")
		})
	}
}

// The line a hub's refusal serves is pre-authorized only if the worker's own guard runs it.
func TestServedHubClaimLinePassesTheGuardForTheWorker(t *testing.T) {
	row := autoClaimRow()
	ctx, root, deps := autoClaimFixture(t, row)

	got := gradeLeasedWrite(ctx, deps, row.ID, filepath.Join(root, "internal/hub/hub.go"))
	require.Len(t, got.Next, 1)
	v := Judge(ctx, deps, Request{Input: got.Next[0].Run, Lease: row.ID, Dir: root})
	assert.NotEqual(t, "deny", v.Decision, "%s: %s", got.Next[0].Run, v.Reason)
}
