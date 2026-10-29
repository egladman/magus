package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type hookBindingsSuite struct {
	suite.Suite
}

func TestHookBindingsSuite(t *testing.T) {
	suite.Run(t, new(hookBindingsSuite))
}

const attachUnchecked = "not verified: https://host.example/docs/hooks 2026-10-29: inline SVG"

// bindsEveryPoint binds each point but attach through one hook, and
// explains attach.
func bindsEveryPoint() HarnessDescriptor {
	all := types.HookPoints()
	return HarnessDescriptor{
		ID: "host",
		Hooks: []types.HookCommand{
			{Event: "Pre", Script: "magus-command", Points: all[:len(all)-1]},
			{Event: "Pre", Script: "magus-observe"},
		},
		UnsupportedHooks: []types.HookBinding{
			{Point: types.HookPointAttach, Status: types.Unsupported, Reason: attachUnchecked},
		},
	}
}

func (s *hookBindingsSuite) TestABoundPointNamesTheScriptsThatBindIt() {
	d := bindsEveryPoint()
	d.Hooks = append(d.Hooks, types.HookCommand{Event: "Stop", Script: "magus-checkpoint",
		Points: []types.HookPoint{types.HookPointStop}})
	table, err := HookBindings(d)
	s.Require().NoError(err)
	s.Len(table, len(types.HookPoints()))
	s.Equal(types.HookBinding{Harness: "host", Point: types.HookPointShell, Status: types.Bound,
		Adapter: "magus-command"}, table[0])
	s.Equal(types.HookBinding{Harness: "host", Point: types.HookPointStop, Status: types.Bound,
		Adapter: "magus-command, magus-checkpoint"}, table[2])
	s.Equal(types.HookBinding{Harness: "host", Point: types.HookPointAttach, Status: types.Unsupported,
		Reason: attachUnchecked}, table[len(table)-1])
}

func (s *hookBindingsSuite) TestAPointWithNoRowIsAGap() {
	d := bindsEveryPoint()
	d.UnsupportedHooks = nil
	table, err := HookBindings(d)
	s.Require().Error(err)
	s.Equal("host: no hook binds attach and mgs_listUnsupportedHooks gives no reason; "+
		"list attach in a hook's points, or add an Unsupported row citing the host's docs", err.Error())
	s.Equal(types.HookBinding{Harness: "host", Point: types.HookPointAttach}, table[len(table)-1],
		"the table still comes back, the gap's status empty")
}

func (s *hookBindingsSuite) TestAnUnsupportedRowAHookBindsIsStale() {
	d := bindsEveryPoint()
	d.UnsupportedHooks = append(d.UnsupportedHooks, types.HookBinding{Point: types.HookPointShell,
		Status: types.Unsupported, Reason: attachUnchecked})
	table, err := HookBindings(d)
	s.Require().Error(err)
	s.Equal("host: mgs_listUnsupportedHooks says shell is unsupported, but magus-command binds it; drop the row",
		err.Error())
	s.Equal(types.Bound, table[0].Status, "the hook wins over the stale row")
}

func (s *hookBindingsSuite) TestValidRowsLoad() {
	s.NoError(validateHookBindings(bindsEveryPoint()))
}

func (s *hookBindingsSuite) TestAReasonCitingADocURLLoads() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Reason = "no hook output carries an image (https://host.example/docs/hooks)"
	s.NoError(validateHookBindings(d))
}

func (s *hookBindingsSuite) TestAReasonCitingAVendoredFileIsRefused() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Reason = "the host has no such event (testdata/hosts/codex/hooks.schema.json)"
	s.ErrorContains(validateHookBindings(d), "which cites no https URL")
}

func (s *hookBindingsSuite) TestAReasonCitingNothingIsRefused() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Reason = "the host cannot"
	s.EqualError(validateHookBindings(d), `unsupported_hooks[0]: attach gives the reason "the host cannot", `+
		`which cites no https URL; cite the host's docs page, or say `+
		`"not verified: <url checked> <YYYY-MM-DD> <what is unsettled>"`)
}

func (s *hookBindingsSuite) TestANotVerifiedReasonMustNameTheURLChecked() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Reason = "not verified: 2026-10-29 inline SVG"
	s.ErrorContains(validateHookBindings(d), "which cites no https URL")
}

func (s *hookBindingsSuite) TestANotVerifiedReasonMustNameTheDateRead() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Reason = "not verified: https://host.example/docs/hooks inline SVG"
	s.EqualError(validateHookBindings(d), `unsupported_hooks[0]: attach gives the reason `+
		`"not verified: https://host.example/docs/hooks inline SVG", `+
		`which names no date; say when the URL was read, as YYYY-MM-DD`)
}

func (s *hookBindingsSuite) TestABoundRowInTheUnsupportedListIsRefused() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Status = types.Bound
	s.EqualError(validateHookBindings(d), `unsupported_hooks[0]: attach is bound: this list holds `+
		`Unsupported rows only, and a Bound point is one a hook lists`)
}

func (s *hookBindingsSuite) TestARowWithNoStatusIsRefused() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Status = ""
	s.ErrorContains(validateHookBindings(d), `attach is unset: this list holds Unsupported rows only`)
}

func (s *hookBindingsSuite) TestASecondRowForOnePointIsRefused() {
	d := bindsEveryPoint()
	d.UnsupportedHooks = append(d.UnsupportedHooks, d.UnsupportedHooks[0])
	s.EqualError(validateHookBindings(d), "unsupported_hooks[1]: a second row for attach")
}

func (s *hookBindingsSuite) TestARowNamingAnotherHarnessIsRefused() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Harness = "other"
	s.EqualError(validateHookBindings(d), `unsupported_hooks[0]: attach names harness "other", not "host"`)
}

func (s *hookBindingsSuite) TestARowNamingAnAdapterIsRefused() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Adapter = "magus-command"
	s.EqualError(validateHookBindings(d), `unsupported_hooks[0]: attach names adapter "magus-command", `+
		`but nothing runs at an unsupported point`)
}

func (s *hookBindingsSuite) TestAnUnknownPointOnAHookIsRefused() {
	d := bindsEveryPoint()
	d.Hooks[1].Points = []types.HookPoint{"read"}
	s.ErrorContains(validateHookBindings(d), `hooks[1]: unknown hook point "read"; one of shell, edit, stop`)
}

func (s *hookBindingsSuite) installHost(root, dir, host, limits string) {
	tools := filepath.Join(root, dir, "magus", "tools")
	s.Require().NoError(os.MkdirAll(tools, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(tools, types.ToolHostFile), []byte(host+"\n"), 0o644))
	s.Require().NoError(os.WriteFile(filepath.Join(tools, types.ToolLimitsFile), []byte(limits+"\n"), 0o644))
}

// twoHosts is a registry of two hosts, each with its own payload field.
func twoHosts() map[string]types.Limits {
	return map[string]types.Limits{
		"alpha": {PayloadFields: []string{"alpha_version"}},
		"beta":  {PayloadFields: []string{"beta_turn"}},
	}
}

func (s *hookBindingsSuite) TestInstalledHostsAreTheHostFilesUnderTheRootWithTheirLimits() {
	root := s.T().TempDir()
	s.installHost(root, ".beta", "beta", `{"payload_fields":["beta_turn"]}`)
	s.installHost(root, ".alpha", "alpha", `{"observes_skill_loads":true}`)
	s.Equal(map[string]types.Limits{
		"alpha": {ObservesSkillLoads: true},
		"beta":  {PayloadFields: []string{"beta_turn"}},
	}, InstalledHosts(root))
}

func (s *hookBindingsSuite) TestNoInstalledHostIsAnEmptyRegistry() {
	s.Empty(InstalledHosts(s.T().TempDir()))
}

func (s *hookBindingsSuite) TestAnInstalledHostPasses() {
	s.NoError(CheckHostInstalled(twoHosts(), "beta"))
}

func (s *hookBindingsSuite) TestAHostNoInstalledHarnessNamesIsRefusedListingTheInstalled() {
	s.EqualError(CheckHostInstalled(twoHosts(), "bet"),
		`--agent-name "bet" names no harness installed here; installed: alpha, beta. `+
			"Fix the hook entry's --agent-name, or install that harness's tool rows")
}

func (s *hookBindingsSuite) TestWithNothingInstalledNoHostIsRefused() {
	s.NoError(CheckHostInstalled(nil, "beta"))
}

func (s *hookBindingsSuite) TestAPayloadCarryingOnlyAnotherHostsFieldIsRefusedNamingBoth() {
	s.EqualError(CheckHostPayload(twoHosts(), "alpha", `{"beta_turn":"t1","session_id":"s1"}`),
		"--agent-name names alpha, but the payload carries beta_turn, a field only beta sends; "+
			"fix the hook entry's --agent-name to beta")
}

func (s *hookBindingsSuite) TestAPayloadCarryingTheNamedHostsOwnFieldPasses() {
	s.NoError(CheckHostPayload(twoHosts(), "alpha", `{"alpha_version":"1","beta_turn":"t1"}`))
}

func (s *hookBindingsSuite) TestAPayloadCarryingNoHostsFieldPasses() {
	s.NoError(CheckHostPayload(twoHosts(), "alpha", `{"session_id":"s1","tool_name":"Bash"}`))
}

func (s *hookBindingsSuite) TestAPayloadThatIsNotAnObjectCarriesNoFields() {
	s.NoError(CheckHostPayload(twoHosts(), "alpha", "git status beta_turn"))
}

func (s *hookBindingsSuite) TestAnEmptyPayloadFieldIsRefused() {
	d := bindsEveryPoint()
	d.Limits.PayloadFields = []string{"turn_id", " "}
	s.EqualError(validateHookBindings(d), "limits.payload_fields[1] is empty")
}

func (s *hookBindingsSuite) TestARepeatedPayloadFieldIsRefused() {
	d := bindsEveryPoint()
	d.Limits.PayloadFields = []string{"turn_id", "turn_id"}
	s.EqualError(validateHookBindings(d), `limits.payload_fields names "turn_id" twice`)
}

func (s *hookBindingsSuite) TestAnUnknownPointInARowIsRefused() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Point = "read"
	s.ErrorContains(validateHookBindings(d), `unsupported_hooks[0]: unknown hook point "read"`)
}
