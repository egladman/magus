package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceShellDenyStrengthensAPass(t *testing.T) {
	t.Parallel()
	deps := testDependencies()
	deps.ShellRules = []WorkspaceShellRule{{
		Name:     "no-curl-prod",
		Decision: "deny",
		Program:  "curl",
		Args:     []string{"https://prod.example/health"},
		Reason:   "do not hit prod from an agent shell",
	}}

	v := Evaluate(deps, "curl -s https://prod.example/health")
	assert.Equal(t, denyRuleName("workspace:no-curl-prod"), v.Rule.Name)
	assert.Contains(t, v.Deny, "do not hit prod")
	assert.Empty(t, v.Context)
}

func TestWorkspaceShellDenySeesBashDashC(t *testing.T) {
	t.Parallel()
	deps := testDependencies()
	deps.ShellRules = []WorkspaceShellRule{{
		Name:     "no-terraform",
		Decision: "deny",
		Program:  "terraform",
		Reason:   "use the workspace terraform target",
	}}

	v := Evaluate(deps, `bash -c 'terraform apply -auto-approve'`)
	assert.Equal(t, denyRuleName("workspace:no-terraform"), v.Rule.Name)
}

func TestWorkspaceShellAdviseFillsSilence(t *testing.T) {
	t.Parallel()
	deps := testDependencies()
	deps.ShellRules = []WorkspaceShellRule{{
		Name:     "advise-jq",
		Decision: "advise",
		Program:  "jq",
		Reason:   "prefer magus -o json when the input is a magus record",
	}}

	v := Evaluate(deps, "jq . file.json")
	assert.Empty(t, v.Deny)
	assert.Contains(t, v.Context, "prefer magus -o json")
}

func TestWorkspaceShellAdviseDoesNotReplaceBuiltInAdvise(t *testing.T) {
	t.Parallel()
	deps := testDependencies()
	deps.ShellRules = []WorkspaceShellRule{{
		Name:     "advise-rg",
		Decision: "advise",
		Program:  "rg",
		Reason:   "workspace-specific rg note",
	}}

	v := Evaluate(deps, "rg Foo")
	assert.Empty(t, v.Deny)
	assert.Contains(t, v.Context, "magus refs")
	assert.NotContains(t, v.Context, "workspace-specific rg note")
}

func TestWorkspaceShellDenyEscalatesBuiltInAdvise(t *testing.T) {
	t.Parallel()
	deps := testDependencies()
	deps.ShellRules = []WorkspaceShellRule{{
		Name:     "deny-rg",
		Decision: "deny",
		Program:  "rg",
		Reason:   "this workspace forbids repo-wide rg",
	}}

	v := Evaluate(deps, "rg Foo")
	assert.Equal(t, denyRuleName("workspace:deny-rg"), v.Rule.Name)
	assert.Contains(t, v.Deny, "forbids repo-wide rg")
}

func TestBuiltInDenyBeatsWorkspaceAdvise(t *testing.T) {
	t.Parallel()
	deps := testDependencies()
	deps.ShellRules = []WorkspaceShellRule{{
		Name:     "advise-go-test",
		Decision: "advise",
		Program:  "go",
		Args:     []string{"test"},
		Reason:   "should never appear: built-in deny wins",
	}}

	v := Evaluate(deps, "go test ./...")
	require.NotEmpty(t, v.Deny)
	assert.Equal(t, denyRuleRawTool, v.Rule.Name)
	assert.NotContains(t, v.Deny, "should never appear")
}

func TestWorkspaceShellFailsOpenWhenUnparsed(t *testing.T) {
	t.Parallel()
	deps := testDependencies()
	deps.ShellRules = []WorkspaceShellRule{{
		Name:     "deny-anything",
		Decision: "deny",
		Program:  "curl",
		Reason:   "should not fire on an unparseable line",
	}}

	v := Evaluate(deps, "curl https://x && (")
	assert.Empty(t, v.Deny, "unparsed lines fail open for workspace rules")
}

func TestStrengthenWithWorkspaceMerge(t *testing.T) {
	t.Parallel()
	builtInDeny := ShellVerdict{Deny: "built-in", Rule: denyRule{Name: denyRuleRawTool}}
	wsDeny := ShellVerdict{Deny: "workspace", Rule: denyRule{Name: "workspace:x"}}
	builtInAdvise := ShellVerdict{Context: "built-in advise"}
	wsAdvise := ShellVerdict{Context: "workspace advise"}

	assert.Equal(t, builtInDeny, strengthenWithWorkspace(builtInDeny, wsDeny))
	assert.Equal(t, wsDeny, strengthenWithWorkspace(builtInAdvise, wsDeny))
	assert.Equal(t, builtInAdvise, strengthenWithWorkspace(builtInAdvise, wsAdvise))
	assert.Equal(t, wsAdvise, strengthenWithWorkspace(ShellVerdict{}, wsAdvise))
}
