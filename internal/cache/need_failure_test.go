package cache

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

func TestWrapNeedFailureKeepsTheErrorText(t *testing.T) {
	base := errors.New("[MGS4007] .:conventions failed")
	wrapped := WrapNeedFailure(fmt.Errorf("ctx.needs: %w", base), NeedMember{Target: "conventions", Ref: "outaaaaaaaaaaaa"})

	assert.Equal(t, "ctx.needs: [MGS4007] .:conventions failed", wrapped.Error(), "hopChain and causeSignature read this text")
	require.ErrorIs(t, wrapped, base)
	target, ref := asNeedFailure(fmt.Errorf("ctx.needs: %w", wrapped))
	assert.Equal(t, "conventions", target)
	assert.Equal(t, "outaaaaaaaaaaaa", ref)
}

func TestWrapNeedFailureKeepsTheDeepestMember(t *testing.T) {
	inner := WrapNeedFailure(errors.New("golangci exited 1"), NeedMember{Target: "lint-go", Ref: "outbbbbbbbbbbbb"})
	outer := WrapNeedFailure(fmt.Errorf("ctx.needs: %w", inner), NeedMember{Target: "lint", Ref: "outcccccccccccc"})

	target, ref := asNeedFailure(outer)
	assert.Equal(t, "lint-go", target)
	assert.Equal(t, "outbbbbbbbbbbbb", ref)
}

func TestWrapNeedFailureLeavesWhatItCannotName(t *testing.T) {
	require.NoError(t, WrapNeedFailure(nil, NeedMember{Target: "lint", Ref: "outaaaaaaaaaaaa"}))

	boom := errors.New("boom")
	assert.Same(t, boom, WrapNeedFailure(boom, NeedMember{}), "a member with no target names nothing")
	target, ref := asNeedFailure(boom)
	assert.Empty(t, target)
	assert.Empty(t, ref)
}

func TestNeedFailureSuite(t *testing.T) { suite.Run(t, new(NeedFailureSuite)) }

// NeedFailureSuite pins how a failure names the member a local ci repairs.
type NeedFailureSuite struct{ suite.Suite }

func (s *NeedFailureSuite) TestFailedNeedNamesTheDeepestMember() {
	inner := WrapNeedFailure(errors.New("gofmt -l listed a.go"), NeedMember{Project: ".", Target: "format"})
	outer := WrapNeedFailure(fmt.Errorf("format: %w", inner), NeedMember{Project: ".", Target: "lint", Ref: "outcccccccccccc"})

	s.Equal([]types.TargetRef{{Project: ".", Target: "format"}}, FailedNeeds(outer))
}

func (s *NeedFailureSuite) TestFailedNeedCarriesTheMembersProject() {
	err := WrapNeedFailure(errors.New("drift"), NeedMember{Project: "libs/a", Target: "generate"})

	s.Equal([]types.TargetRef{{Project: "libs/a", Target: "generate"}}, FailedNeeds(fmt.Errorf("ci: %w", err)))
}

func (s *NeedFailureSuite) TestErrorWithNoMemberNamesNone() {
	s.Empty(FailedNeeds(errors.New("the target's own body threw")))
}

func (s *NeedFailureSuite) TestFailedNeedNamesEveryMemberOneNeedsCallJoined() {
	a := WrapNeedFailure(errors.New("a failed"), NeedMember{Project: ".", Target: "tidy"})
	b := WrapNeedFailure(errors.New("b failed"), NeedMember{Project: ".", Target: "regen"})
	joined := errors.Join(fmt.Errorf("tidy: %w", a), fmt.Errorf("regen: %w", b), fmt.Errorf("tidy: %w", a))

	s.Equal([]types.TargetRef{{Project: ".", Target: "tidy"}, {Project: ".", Target: "regen"}}, FailedNeeds(joined))
}

func (s *NeedFailureSuite) TestInlineMemberLeavesItsComposersRefOnTheRecord() {
	inline := WrapNeedFailure(errors.New("drift"), NeedMember{Project: ".", Target: "generate"})
	step := WrapNeedFailure(inline, NeedMember{Project: ".", Target: "format", Ref: "outdddddddddddd"})

	target, ref := asNeedFailure(step)
	s.Equal("format", target, "an inline member has no log, so the step that ran it is the hop")
	s.Equal("outdddddddddddd", ref)
}
