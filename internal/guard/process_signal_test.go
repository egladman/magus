package guard

import (
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Neither a PID nor a process name proves the caller owns it.
func TestGuardDeniesProcessSignals(t *testing.T) {
	for _, command := range []string{
		`pkill -f 'magus job fork' ; ./magus status`,
		`pkill -f './magus mcp' 2>/dev/null`,
		`kill 27873 32452 34431 2>/dev/null`,
		`kill -TERM 30940`,
		`kill -9 -- -1234`,
		`/bin/kill -s TERM 30940`,
		`killall magus`,
		`bash -c 'pkill -f magus'`,
		`env X=1 /usr/bin/pkill -f magus`,
		`pkill -- --help`,
		`killall -- --version`,
		`kill -- -l`,
	} {
		t.Run(command, func(t *testing.T) {
			v := Evaluate(strict(testDependencies()), command)
			require.Equal(t, denyRuleName("process-signal"), v.Rule.Name)
			require.NotEmpty(t, v.Deny)
			assert.Contains(t, v.Deny, "cancel")
			require.Len(t, v.Next, 1)
			assert.Equal(t, hint.Status.String(), v.Next[0].Run)
			assert.Empty(t, Evaluate(strict(testDependencies()), v.Next[0].Run).Deny)
		})
	}
	deps := advising(testDependencies(), "process-signal")
	assert.Empty(t, Evaluate(deps, "kill 30940").Deny)
	assert.NotEmpty(t, advised(Evaluate(deps, "kill 30940")))
}

func TestGuardAllowsSignalDocumentationAndMentions(t *testing.T) {
	for _, command := range []string{
		`echo "pkill -f magus"`,
		`printf '%s\n' 'kill 30940'`,
		`kill -l`,
		`kill -l TERM`,
		`kill -L`,
		`kill --list`,
		`kill --help`,
		`pkill --help`,
		`killall --version`,
	} {
		assert.NotEqual(t, denyRuleName("process-signal"), Evaluate(strict(testDependencies()), command).Rule.Name, command)
	}
}

func TestGuardDeniesSingleProcessProbe(t *testing.T) {
	for _, command := range []string{
		`kill -0 4242`,
		`kill -s 0 4242`,
		`kill --signal=0 4242`,
		`sleep 1m; kill -0 4242`,
		`kill -0 4242 && ./magus run go-test .`,
	} {
		v := Evaluate(strict(testDependencies()), command)
		assert.Equal(t, denyRuleProcessPoll, v.Rule.Name, command)
		assert.NotEmpty(t, v.Deny, command)
	}
}
