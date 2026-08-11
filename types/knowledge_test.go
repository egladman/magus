package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

// A lookup that searched everything it could and found nothing is a verified absence,
// and it carries no reason and no gap list - there is nothing to act on.
func TestAnswerAbsent(t *testing.T) {
	assert.Equal(t, KnowledgeAnswer{Verdict: VerdictAbsent}, Answer(false, "", nil))
}

// Symbols were loaded, but a project's declared index is missing, so a definition there
// would be invisible. The gaps ride along: naming them is what makes the verdict usable.
func TestAnswerIndexMissing(t *testing.T) {
	gaps := []KnowledgeSymbolGap{{Project: NewProjectRef("libs/api", ""), State: SymbolIndexNotBuilt}}
	want := KnowledgeAnswer{
		Verdict: VerdictUnknown,
		Reason:  ReasonSymbolIndexMissing,
		Gaps:    gaps,
	}
	assert.Equal(t, want, Answer(false, "", gaps))
}

// No symbol shard was merged at all, so no code symbol could have matched whatever is
// on disk. This is unknown even with every declared index built.
func TestAnswerSymbolsNotLoaded(t *testing.T) {
	want := KnowledgeAnswer{Verdict: VerdictUnknown, Reason: ReasonSymbolsNotLoaded}
	assert.Equal(t, want, Answer(false, ReasonSymbolsNotLoaded, nil))
}

// Both causes at once. A stated reason wins because it is the more specific fact and the
// more actionable fix: building the missing index would leave the result just as
// unsearchable by this lookup, so reporting the index first sends the caller somewhere
// useless.
func TestAnswerStatedReasonWinsOverMissingIndex(t *testing.T) {
	gaps := []KnowledgeSymbolGap{{Project: NewProjectRef("libs/api", ""), State: SymbolIndexNotBuilt}}
	got := Answer(false, ReasonSymbolsNotLoaded, gaps)
	assert.Equal(t, ReasonSymbolsNotLoaded, got.Reason)
	assert.Equal(t, gaps, got.Gaps, "the gaps still ride along; only the reason changes")
}

// The wire keys are a contract with agents and external consumers, so pin them. Verdict
// must survive even on the absent path, where reason and uncovered drop out.
func TestKnowledgeAnswerJSONKeys(t *testing.T) {
	b, err := json.Marshal(Answer(false, "", nil))
	require.NoError(t, err)
	assert.JSONEq(t, `{"verdict":"absent"}`, string(b))

	b, err = json.Marshal(Answer(false, "", []KnowledgeSymbolGap{
		{Project: NewProjectRef("libs/api", ""), State: SymbolIndexNotBuilt, Detail: "unreadable"},
	}))
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"verdict": "unknown",
		"reason": "symbol-index-missing",
		"gaps": [{
			"project": {"path": "libs/api", "name": "libs/api"},
			"state": "not-indexed",
			"detail": "unreadable"
		}]
	}`, string(b))
}
