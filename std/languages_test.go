package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/spells"
)

type LanguagesSuite struct{ suite.Suite }

func TestLanguages(t *testing.T) { suite.Run(t, new(LanguagesSuite)) }

func (s *LanguagesSuite) TestAnUnmarkedContextHasNone() {
	langs, ok := LanguagesFromContext(context.Background())
	s.False(ok)
	s.Empty(langs)
}

func (s *LanguagesSuite) TestAWorkspaceWithoutSpellsHasAnEmptyIndex() {
	langs, ok := LanguagesFromContext(WithLanguages(context.Background(), nil))
	s.True(ok)
	s.Empty(langs)
}

func (s *LanguagesSuite) TestEachSpellContributesItsDeclaredLanguage() {
	syntax := &spells.Syntax{Comments: &spells.CommentSyntax{LineComments: []string{"//"}}}
	golang := spells.NewSpell("golang",
		spells.WithLanguage("go"),
		spells.WithLanguageExtensions([]string{".go"}),
		spells.WithSyntax(syntax))
	bare := spells.NewSpell("bare")

	langs, ok := LanguagesFromContext(WithLanguages(context.Background(), []*spells.Spell{bare, golang}))
	s.Require().True(ok)
	s.Equal([]spells.Language{{Name: "go", Extensions: []string{".go"}, Syntax: syntax}}, langs)
}
