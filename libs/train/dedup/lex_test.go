package dedup

import ()

func (s *dedupSuite) TestTokensPerLanguage() {
	buzz, err := Tokens(Buzz, "final x = 1; // note\n")
	s.Require().NoError(err)
	s.Len(buzz, 5)

	gotoks, err := Tokens(Go, "package p\n\nfunc F() { /* c */ return }\n")
	s.Require().NoError(err)
	s.Equal([]string{"package", "p", "func", "F", "(", ")", "{", "return", "}"}, gotoks)

	ts, err := Tokens(TypeScript, "const a = `x${y}`; // z\nlet b = 'q';")
	s.Require().NoError(err)
	s.Equal([]string{"const", "a", "=", "`x${y}`", ";", "let", "b", "=", "'q'", ";"}, ts)

	_, err = Tokens("cobol", "x")
	var unknown *UnknownLanguageError
	s.ErrorAs(err, &unknown)
}
