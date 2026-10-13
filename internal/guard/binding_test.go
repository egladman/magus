package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// A session bound to a job it exited is graded under the job its BAGGAGE claims, when that
// one is still declared or running: a resumed worker handed a new job was judged as the
// one it had finished, so every write to its new paths was refused. The binding is
// released, and the claim answers from then on. Without a claim, or with one naming no job
// in flight, the exited job still holds, since a rejected wait sends its holder back to it.
func TestAnExitedBindingYieldsToABaggageClaim(t *testing.T) {
	who := hookAttribution{Host: "claude-code", Session: "s1", Agent: "a1"}
	row := func(id string, state types.JobState) types.Job {
		return types.Job{ID: id, State: state, WritePaths: []string{"internal/" + id + "/**"}, Registered: 1, ReportedBase: "77aa01c"}
	}
	for _, tc := range []struct {
		name  string
		held  types.JobState
		claim string
		want  graded
		bound job.Binding
	}{
		{"an exited job and a claim on a running one", types.StateExited, "next-job",
			graded{Lease: "next-job", From: types.LeaseSourceEnv}, job.Binding{}},
		{"an exited job and a claim on a declared one", types.StateExited, "fresh-job",
			graded{Lease: "fresh-job", From: types.LeaseSourceEnv}, job.Binding{}},
		{"an exited job and no claim", types.StateExited, "",
			graded{Lease: "held-job", From: types.LeaseSourceAgent}, job.Binding{Job: "held-job"}},
		{"an exited job and a claim on itself", types.StateExited, "held-job",
			graded{Lease: "held-job", From: types.LeaseSourceAgent}, job.Binding{Job: "held-job"}},
		{"an exited job and a claim on another exited one", types.StateExited, "done-job",
			graded{Lease: "held-job", From: types.LeaseSourceContested}, job.Binding{Job: "held-job"}},
		{"an exited job and a claim on no job", types.StateExited, "absent-job",
			graded{Lease: "held-job", From: types.LeaseSourceContested}, job.Binding{Job: "held-job"}},
		{"a running job outranks the claim", types.StateRunning, "next-job",
			graded{Lease: "held-job", From: types.LeaseSourceContested}, job.Binding{Job: "held-job"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := fleetFixture(t, row("held-job", tc.held), row("next-job", types.StateRunning),
				row("fresh-job", types.StateDeclared), row("done-job", types.StateExited))
			bindCaller(t, ctx, who, "held-job")
			if tc.claim != "" {
				t.Setenv(trail.EnvBaggage, trail.BaggageLease+"="+tc.claim)
			} else {
				t.Setenv(trail.EnvBaggage, "")
			}

			v := Judge(ctx, Dependencies{}, Request{Input: bashCall(t, who.Session, who.Agent, "ls"), Host: who.Host})
			assert.Equal(t, tc.want, gradedAs(v), v.Reason)
			assert.Equal(t, tc.bound, boundJob(who, hookLocation(ctx, Dependencies{})), "the caller's record")
		})
	}
}
