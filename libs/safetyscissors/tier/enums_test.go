package tier

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type EnumsSuite struct{ suite.Suite }

func TestEnums(t *testing.T) { suite.Run(t, new(EnumsSuite)) }

func (s *EnumsSuite) TestTheVocabulariesCloseTheirSets() {
	s.Equal([]string{"short", "full"}, SkillVariant("").Values())
	s.Equal([]string{"job", "matrix", "user-matrix", "default"}, Source("").Values())
	s.False(SkillVariant("medium").Valid())
	s.True(SkillVariantFull.Full())
	s.False(SkillVariantFull.Short())
	s.True(SkillVariantShort.Short())
	s.Equal("short", SkillVariantShort.String(), "the skill-variant stamp prints this")
}
