package symbols

func (s *symbolsSuite) TestInstallHintNamesTheIndexerAndWhereToGetIt() {
	s.Equal("the typescript SCIP indexer (scip-typescript) may not be installed; get it from https://github.com/sourcegraph/scip-typescript",
		InstallHint("typescript"))
}

func (s *symbolsSuite) TestInstallHintIsEmptyForALanguageIndexedInProcess() {
	s.Empty(InstallHint("go"))
	s.Empty(InstallHint("buzz"))
	s.Empty(InstallHint("shell"))
}

func (s *symbolsSuite) TestInstallHintIsEmptyForAnUnknownLanguage() {
	s.Empty(InstallHint(""))
	s.Empty(InstallHint("cobol"))
}

// The hint names the binary the spell declares, which need not be the one the table
// knows, and looks nothing up: whether it is installed is the caller's question.
func (s *symbolsSuite) TestMissingIndexerHintNamesTheDeclaredBinary() {
	s.T().Setenv("PATH", s.T().TempDir())
	s.Equal("the rust SCIP indexer (my-rust-analyzer) is not installed; get it from https://github.com/rust-lang/rust-analyzer",
		MissingIndexerHint("rust", "my-rust-analyzer"))
}

func (s *symbolsSuite) TestMissingIndexerHintHasNoURLForAnUnknownLanguage() {
	s.Equal("the cobol SCIP indexer (scip-cobol) is not installed", MissingIndexerHint("cobol", "scip-cobol"))
}

func (s *symbolsSuite) TestMissingIndexerHintIsEmptyWithoutABinary() {
	s.Empty(MissingIndexerHint("go", ""))
}
