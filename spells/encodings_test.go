package spells

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// goEncodings is the go spell's declaration from the emit design, section 5.5.
func goEncodings() []Encoding {
	return []Encoding{
		{Name: "ident", Pattern: `^[\p{L}_][\p{L}\p{Nd}_]*$`, Reserved: []string{"func", "type"}},
		{
			Name: "string", Open: `"`, Close: `"`,
			Escapes: []Escape{{Text: `\`, To: `\\`}, {Text: `"`, To: `\"`}, {Text: "\n", To: `\n`}},
			Control: `\x{hex2}`,
		},
	}
}

func goLanguage(encs []Encoding) Language {
	return Language{Name: "go", Extensions: []string{".go"}, Syntax: &Syntax{Encodings: encs}}
}

type encodingValidateSuite struct{ suite.Suite }

func TestEncodingValidate(t *testing.T) { suite.Run(t, new(encodingValidateSuite)) }

func (s *encodingValidateSuite) TestAcceptsTheGoDeclaration() {
	s.NoError(ValidateEncodings(goLanguage(goEncodings())))
}

func (s *encodingValidateSuite) TestAcceptsALanguageWithoutSyntax() {
	s.NoError(ValidateEncodings(Language{Name: "go"}))
}

func (s *encodingValidateSuite) TestRefusesEncodingsWithoutExtensions() {
	l := goLanguage(goEncodings())
	l.Extensions = nil
	s.ErrorContains(ValidateEncodings(l), "without extensions")
}

func (s *encodingValidateSuite) TestRefusesANameOutsideTheGrammar() {
	s.ErrorContains(ValidateEncodings(goLanguage([]Encoding{{Name: "Ident"}})), `encodings[0] (Ident): name "Ident" must match`)
}

func (s *encodingValidateSuite) TestRefusesTheEnginesOwnNames() {
	s.ErrorContains(ValidateEncodings(goLanguage([]Encoding{{Name: "raw"}})), "engine's own")
	s.ErrorContains(ValidateEncodings(goLanguage([]Encoding{{Name: "block"}})), "engine's own")
}

func (s *encodingValidateSuite) TestRefusesANameDeclaredTwice() {
	s.ErrorContains(ValidateEncodings(goLanguage([]Encoding{{Name: "ident"}, {Name: "ident"}})), `encodings[1] (ident): name "ident" is declared twice`)
}

func (s *encodingValidateSuite) TestRefusesAPatternThatDoesNotCompile() {
	s.ErrorContains(ValidateEncodings(goLanguage([]Encoding{{Name: "ident", Pattern: "(a"}})), "does not compile as RE2")
}

func (s *encodingValidateSuite) TestRefusesAnEmptyEscape() {
	s.ErrorContains(ValidateEncodings(goLanguage([]Encoding{{Name: "string", Escapes: []Escape{{To: "x"}}}})), "escapes[0].text must be non-empty")
}

func (s *encodingValidateSuite) TestRefusesAnEscapeAnEarlierOneShadows() {
	encs := []Encoding{{Name: "string", Escapes: []Escape{{Text: `\`, To: `\\`}, {Text: `\n`, To: "x"}}}}
	s.ErrorContains(ValidateEncodings(goLanguage(encs)), `escapes[1].text "\\n" is never reached`)
}

func (s *encodingValidateSuite) TestRefusesAnUnknownControlPlaceholder() {
	s.ErrorContains(ValidateEncodings(goLanguage([]Encoding{{Name: "string", Control: `\x{hex}`}})), "must hold exactly one of")
}

func (s *encodingValidateSuite) TestRefusesTwoControlPlaceholders() {
	s.ErrorContains(ValidateEncodings(goLanguage([]Encoding{{Name: "string", Control: `{hex2}{hex2}`}})), "must hold exactly one of")
}

func (s *encodingValidateSuite) TestAcceptsRustsBracedControl() {
	s.NoError(ValidateEncodings(goLanguage([]Encoding{{Name: "string", Control: `\u{{hex2}}`}})))
}

type encodingIndexSuite struct{ suite.Suite }

func TestEncodingIndex(t *testing.T) { suite.Run(t, new(encodingIndexSuite)) }

func (s *encodingIndexSuite) TestRoutesAnExtensionToItsSpell() {
	golang := NewSpell("golang", WithLanguage("go"), WithLanguageExtensions([]string{".go"}),
		WithSyntax(&Syntax{Encodings: goEncodings()}))
	s.Equal(map[string]LanguageEncodings{
		".go": {Spell: "golang", Language: "go", Encodings: goEncodings()},
	}, EncodingIndex([]*Spell{golang}))
}

func (s *encodingIndexSuite) TestKeepsALanguageThatDeclaresNoEncodings() {
	bash := NewSpell("bash", WithLanguage("shell"), WithLanguageExtensions([]string{".sh"}))
	s.Equal(map[string]LanguageEncodings{".sh": {Spell: "bash", Language: "shell"}}, EncodingIndex([]*Spell{bash}))
}

func (s *encodingIndexSuite) TestTheFirstClaimWins() {
	builtin := NewSpell("golang", WithLanguage("go"), WithLanguageExtensions([]string{".go"}))
	workspace := NewSpell("mygo", WithLanguage("go"), WithLanguageExtensions([]string{".go"}),
		WithSyntax(&Syntax{Encodings: goEncodings()}))
	s.Equal("golang", EncodingIndex([]*Spell{builtin, workspace})[".go"].Spell)
}
