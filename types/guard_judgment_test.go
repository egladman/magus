package types

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	json "github.com/egladman/magus/internal/json"
)

// The hook glue of every host parses this envelope, so its keys and their order are the
// contract: the move into types must not change a byte of it.
func TestGuardJudgmentWireShape(t *testing.T) {
	full := GuardJudgment{
		SchemaVersion: 3,
		Decision:      "deny",
		Reason:        "no",
		Context:       "fyi",
		Rule:          "some-rule",
		Lease:         "job-a",
		LeaseFrom:     LeaseSourceFlag,
		Next: []NextStep{{
			ID:    "deny-remedy-some-rule",
			Run:   "magus run test .",
			Argv:  []string{"magus", "run", "test", "."},
			Why:   "because",
			Reads: true,
		}},
	}
	got, err := json.Marshal(full)
	require.NoError(t, err)
	assert.Equal(t, `{"schema_version":3,"decision":"deny","reason":"no","context":"fyi","rule":"some-rule",`+
		`"lease":"job-a","lease_from":"flag","next":[{"id":"deny-remedy-some-rule","run":"magus run test .",`+
		`"argv":["magus","run","test","."],"why":"because","reads":true}]}`, string(got))

	pass, err := json.Marshal(GuardJudgment{SchemaVersion: 3, Decision: "pass"})
	require.NoError(t, err)
	assert.Equal(t, `{"schema_version":3,"decision":"pass"}`, string(pass), "a pass names only its decision")
}

// A NextStep reaches the console and the MCP tools as YAML as well as JSON, with the same
// optional fields left out.
func TestNextStepYAMLOmitsEmptyFields(t *testing.T) {
	got, err := yaml.Marshal(NextStep{ID: "a", Run: "magus ls"})
	require.NoError(t, err)
	assert.Equal(t, "id: a\nrun: magus ls\n", string(got))
}

// A refused run reports the deny itself: its reason as the message, and the whole judgment
// for a caller that reads the rule or the remedy.
func TestGuardDeniedErrorCarriesTheJudgment(t *testing.T) {
	deny := GuardJudgment{Decision: "deny", Reason: "run it through the target", Rule: "raw-tool"}
	var err error = &GuardDeniedError{Judgment: deny}

	assert.Equal(t, deny.Reason, err.Error())
	var denied *GuardDeniedError
	require.ErrorAs(t, fmt.Errorf("run: %w", err), &denied)
	assert.Equal(t, deny, denied.Judgment)
}
