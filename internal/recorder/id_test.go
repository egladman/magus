package recorder

import (
	"testing"

	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/stretchr/testify/suite"
)

type InvocationIDSuite struct{ suite.Suite }

func TestInvocationIDSuite(t *testing.T) { suite.Run(t, new(InvocationIDSuite)) }

// One minted id names both a journal file and a session file, so the two
// validators must agree on it.
func (s *InvocationIDSuite) TestMintedIDPassesBothValidators() {
	id := NewInvocationID()
	s.True(journal.ValidInvocationID(id), id)
	s.True(session.ValidFileID(id), id)
	s.True(LooksLikeInvocationID(id), id)
}
