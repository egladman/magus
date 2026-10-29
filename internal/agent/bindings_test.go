package agent

import (
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
			{Point: types.HookPointAttach, Status: types.Unsupported, Reason: "not verified: inline SVG"},
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
		Reason: "not verified: inline SVG"}, table[len(table)-1])
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
		Status: types.Unsupported, Reason: "not verified: shell"})
	table, err := HookBindings(d)
	s.Require().Error(err)
	s.Equal("host: mgs_listUnsupportedHooks says shell is unsupported, but magus-command binds it; drop the row",
		err.Error())
	s.Equal(types.Bound, table[0].Status, "the hook wins over the stale row")
}

func (s *hookBindingsSuite) TestValidRowsLoad() {
	s.NoError(validateHookBindings(bindsEveryPoint()))
}

func (s *hookBindingsSuite) TestAReasonCitingADocLoads() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Reason = "the host has no such event (testdata/hosts/codex/hooks.schema.json)"
	s.NoError(validateHookBindings(d))
}

func (s *hookBindingsSuite) TestAReasonCitingNothingIsRefused() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Reason = "the host cannot"
	s.EqualError(validateHookBindings(d), `unsupported_hooks[0]: attach gives the reason "the host cannot", `+
		`which cites nothing; cite the host's docs, or say "not verified: <what>" and what is unchecked`)
}

func (s *hookBindingsSuite) TestANotVerifiedReasonMustNameWhat() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Reason = "not verified: "
	s.ErrorContains(validateHookBindings(d), "cites nothing")
}

func (s *hookBindingsSuite) TestABoundRowInTheUnsupportedListIsRefused() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Status = types.Bound
	s.EqualError(validateHookBindings(d), `unsupported_hooks[0]: attach is "bound": this list holds `+
		`Unsupported rows only, and a Bound point is one a hook lists`)
}

func (s *hookBindingsSuite) TestARowWithNoStatusIsRefused() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Status = ""
	s.ErrorContains(validateHookBindings(d), `attach is "": this list holds Unsupported rows only`)
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

func (s *hookBindingsSuite) TestAnUnknownPointInARowIsRefused() {
	d := bindsEveryPoint()
	d.UnsupportedHooks[0].Point = "read"
	s.ErrorContains(validateHookBindings(d), `unsupported_hooks[0]: unknown hook point "read"`)
}
