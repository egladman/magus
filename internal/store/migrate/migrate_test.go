package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/stretchr/testify/suite"
)

type migrateSuite struct {
	suite.Suite
}

func TestMigrateSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(migrateSuite))
}

// thing is the api_version a test store's n-th migration converts to.
func thing(n int) apiversion.APIVersion {
	return apiversion.APIVersion(fmt.Sprintf("magus.thing/v1alpha%d", n))
}

// renameMember is a migration to version that renames one member of every entry carrying it.
func renameMember(version int, from, to string) Migration {
	return Migration{Name: fmt.Sprintf("%s to %s", from, to), To: thing(version), Up: func(e Entry) (Entry, error) {
		if v, ok := e[from]; ok {
			delete(e, from)
			e[to] = v
		}
		return e, nil
	}}
}

func (s *migrateSuite) store(body string, migrations ...Migration) Store {
	path := filepath.Join(s.T().TempDir(), "things.jsonl")
	if body != "" {
		s.Require().NoError(os.WriteFile(path, []byte(body), 0o644))
	}
	return Store{
		Name:       "thing store",
		Path:       path,
		Header:     func(e Entry) bool { _, ok := e["id"]; return !ok },
		Migrations: migrations,
	}
}

func (s *migrateSuite) contents(st Store) string {
	raw, err := os.ReadFile(st.Path)
	s.Require().NoError(err)
	return string(raw)
}

func (s *migrateSuite) TestUpOneVersion() {
	st := s.store("{\"plan\":\"p\"}\n{\"id\":\"a\",\"attempt\":1}\n{\"id\":\"b\"}\n{\"id\":\"a\",\"attempt\":2}\n",
		renameMember(1, "attempt", "run"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.Equal(result{Store: "thing store", Path: st.Path, To: thing(1), Entries: 3}, res)
	s.Equal("{\"api_version\":\"magus.thing/v1alpha1\",\"plan\":\"p\"}\n{\"id\":\"a\",\"run\":1}\n{\"id\":\"b\"}\n{\"id\":\"a\",\"run\":2}\n", s.contents(st),
		"every line is migrated, superseded ones included, and the header carries the version")
	s.Equal(fmt.Sprintf("migrated thing store %s from unversioned to magus.thing/v1alpha1 (3 entries)", st.Path), res.String())
}

func (s *migrateSuite) TestUpSeveralVersionsInOrder() {
	st := s.store("{\"api_version\":\"magus.thing/v1alpha1\"}\n{\"id\":\"a\",\"run\":1}\n",
		renameMember(1, "attempt", "run"), renameMember(2, "run", "try"), renameMember(3, "try", "pass"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.Equal(result{Store: "thing store", Path: st.Path, From: thing(1), To: thing(3), Entries: 1}, res)
	s.Equal("{\"api_version\":\"magus.thing/v1alpha3\"}\n{\"id\":\"a\",\"pass\":1}\n", s.contents(st), "only the migrations past v1alpha1 ran, in order")
}

// compat(until: the next release): see [numberedMember].
func (s *migrateSuite) TestANumberedHeaderReadsAsTheVersionItCounts() {
	st := s.store("{\"migration\":1,\"plan\":\"p\"}\n{\"id\":\"a\",\"run\":1}\n",
		renameMember(1, "attempt", "run"), renameMember(2, "run", "try"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.Equal(result{Store: "thing store", Path: st.Path, From: thing(1), To: thing(2), Entries: 1, Numbered: true}, res)
	s.Equal("{\"api_version\":\"magus.thing/v1alpha2\",\"plan\":\"p\"}\n{\"id\":\"a\",\"try\":1}\n", s.contents(st))
	s.Contains(res.String(), `; the header's "migration" member became its "api_version"`)
}

// compat(until: the next release): see [numberedMember].
func (s *migrateSuite) TestACurrentNumberedHeaderIsRewrittenWithItsVersion() {
	st := s.store("{\"api_version\":\"magus.other/v1\",\"migration\":2}\n{\"id\":\"a\",\"try\":1}\n",
		renameMember(1, "attempt", "run"), renameMember(2, "run", "try"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.True(res.Migrated(), "the numbered header is respelled even though no migration ran")
	s.Equal("{\"api_version\":\"magus.thing/v1alpha2\"}\n{\"id\":\"a\",\"try\":1}\n", s.contents(st))
}

// compat(until: the next release): see [numberedMember].
func (s *migrateSuite) TestANumberedHeaderPastEveryMigrationIsRefused() {
	body := "{\"migration\":3}\n{\"id\":\"a\"}\n"
	st := s.store(body, renameMember(1, "attempt", "run"))

	_, err := st.Apply()
	s.Require().EqualError(err, fmt.Sprintf("thing store %s is at migration 3, newer than this magus knows (1, which is magus.thing/v1alpha1): update magus", st.Path))
	s.Equal(body, s.contents(st))
}

func (s *migrateSuite) TestAHeaderRewriteRunsBeforeTheVersionIsSet() {
	m := renameMember(1, "plan", "book")
	m.Header = func(e Entry) (Entry, error) {
		e["book"], e["api_version"] = e["plan"], codec.RawMessage(`"magus.thing/v9"`)
		delete(e, "plan")
		return e, nil
	}
	st := s.store("{\"plan\":\"p\"}\n{\"id\":\"a\",\"plan\":1}\n", m)

	_, err := st.Apply()
	s.Require().NoError(err)
	s.Equal("{\"api_version\":\"magus.thing/v1alpha1\",\"book\":\"p\"}\n{\"book\":1,\"id\":\"a\"}\n", s.contents(st))
}

func (s *migrateSuite) TestAFailingHeaderRewriteLeavesTheFile() {
	body := "{\"plan\":\"p\"}\n{\"id\":\"a\"}\n"
	m := renameMember(1, "plan", "book")
	m.Header = func(Entry) (Entry, error) { return nil, errors.New("no") }
	st := s.store(body, m)

	_, err := st.Apply()
	s.ErrorContains(err, "migration 1 (plan to book): header: no")
	s.Equal(body, s.contents(st))
}

func (s *migrateSuite) TestTheStampRunsAfterTheMigrationsAndBeforeTheVersion() {
	m := renameMember(1, "plan", "book")
	m.Header = func(e Entry) (Entry, error) {
		e["book"] = e["plan"]
		delete(e, "plan")
		return e, nil
	}
	st := s.store("{\"plan\":\"p\",\"writer\":\"old\"}\n{\"id\":\"a\"}\n", m)
	st.Stamp = func(e Entry) (Entry, error) {
		_, renamed := e["book"]
		s.True(renamed, "the migrations' header rewrite ran first")
		e["writer"], e["api_version"] = codec.RawMessage(`"new"`), codec.RawMessage(`"magus.thing/v9"`)
		return e, nil
	}

	_, err := st.Apply()
	s.Require().NoError(err)
	s.Equal("{\"api_version\":\"magus.thing/v1alpha1\",\"book\":\"p\",\"writer\":\"new\"}\n{\"id\":\"a\"}\n", s.contents(st))
}

func (s *migrateSuite) TestAFailingStampLeavesTheFile() {
	body := "{\"plan\":\"p\"}\n{\"id\":\"a\"}\n"
	st := s.store(body, renameMember(1, "plan", "book"))
	st.Stamp = func(Entry) (Entry, error) { return nil, errors.New("no") }

	_, err := st.Apply()
	s.ErrorContains(err, "stamping the header: no")
	s.Equal(body, s.contents(st))
}

func (s *migrateSuite) TestAStampReturningNoHeaderFails() {
	body := "{\"id\":\"a\"}\n"
	st := s.store(body, renameMember(1, "plan", "book"))
	st.Stamp = func(Entry) (Entry, error) { return nil, nil }

	_, err := st.Apply()
	s.ErrorContains(err, "stamping the header: returned no header")
	s.Equal(body, s.contents(st))
}

func (s *migrateSuite) TestACurrentFileIsNotStamped() {
	body := "{\"api_version\":\"magus.thing/v1alpha1\"}\n{\"id\":\"a\"}\n"
	st := s.store(body, renameMember(1, "plan", "book"))
	st.Stamp = func(Entry) (Entry, error) { s.Fail("a file with nothing to migrate is not rewritten"); return nil, nil }

	_, err := st.Apply()
	s.Require().NoError(err)
	s.Equal(body, s.contents(st))
}

func (s *migrateSuite) TestAddsAHeaderToAFileWithoutOne() {
	st := s.store("{\"id\":\"a\",\"attempt\":1}\n", renameMember(1, "attempt", "run"))

	_, err := st.Apply()
	s.Require().NoError(err)
	s.Equal("{\"api_version\":\"magus.thing/v1alpha1\"}\n{\"id\":\"a\",\"run\":1}\n", s.contents(st))
}

func (s *migrateSuite) TestAHeaderInAnotherGroupIsUnversioned() {
	st := s.store("{\"api_version\":\"magus.batch/v1alpha1\"}\n{\"id\":\"a\",\"attempt\":1}\n", renameMember(1, "attempt", "run"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.Empty(res.From, "a version no migration's group holds was written before the first migration")
	s.Equal("{\"api_version\":\"magus.thing/v1alpha1\"}\n{\"id\":\"a\",\"run\":1}\n", s.contents(st))
}

func (s *migrateSuite) TestAlreadyCurrentIsLeftAlone() {
	body := "{\"id\":\"a\",\"run\":1}\n{\"api_version\":\"magus.thing/v1alpha1\"}\n"
	st := s.store(body, renameMember(1, "attempt", "run"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.Equal(result{Store: "thing store", Path: st.Path, From: thing(1), To: thing(1)}, res, "not migrated")
	s.Equal(body, s.contents(st), "not a byte moved")
}

func (s *migrateSuite) TestAbsentFileIsLeftAbsent() {
	st := s.store("", renameMember(1, "attempt", "run"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.False(res.Migrated())
	s.NoFileExists(st.Path)
}

func (s *migrateSuite) TestNewerIsRefusedNamingBothVersions() {
	body := "{\"api_version\":\"magus.thing/v1alpha4\"}\n{\"id\":\"a\"}\n"
	st := s.store(body, renameMember(1, "attempt", "run"))

	_, err := st.Apply()
	var newer *NewerError
	s.Require().ErrorAs(err, &newer)
	s.Equal(NewerError{Store: "thing store", Path: st.Path, Found: thing(4), Known: thing(1)}, *newer)
	s.Equal(fmt.Sprintf("thing store %s is at magus.thing/v1alpha4, newer than this magus knows (magus.thing/v1alpha1): update magus", st.Path), err.Error())
	s.Equal(body, s.contents(st))
}

func (s *migrateSuite) TestAVersionNoMigrationConvertsToIsRefused() {
	body := "{\"api_version\":\"magus.thing/v1alpha2\"}\n{\"id\":\"a\"}\n"
	st := s.store(body, renameMember(1, "attempt", "run"), renameMember(3, "run", "try"))

	_, err := st.Apply()
	var unknown *unknownVersionError
	s.Require().ErrorAs(err, &unknown)
	s.Equal(unknownVersionError{Store: "thing store", Path: st.Path, Found: thing(2), Known: []apiversion.APIVersion{thing(1), thing(3)}}, *unknown)
	s.Equal(body, s.contents(st))
}

func (s *migrateSuite) TestMigrationsOutOfVersionOrderAreRefused() {
	st := s.store("{\"id\":\"a\"}\n", renameMember(2, "attempt", "run"), renameMember(1, "run", "try"))

	_, err := st.Apply()
	s.Require().EqualError(err, "thing store: migration 2 (run to try) converts to magus.thing/v1alpha1, which is not newer than migration 1's magus.thing/v1alpha2")
}

func (s *migrateSuite) TestAFailingMigrationLeavesTheOldFileIntact() {
	body := "{\"id\":\"a\",\"attempt\":1}\n{\"id\":\"b\",\"attempt\":\"x\"}\n"
	st := s.store(body, renameMember(1, "attempt", "run"), Migration{Name: "number the run", To: thing(2), Up: func(e Entry) (Entry, error) {
		var n int
		if err := codec.Unmarshal(e["run"], &n); err != nil {
			return nil, errors.New("run is not a number")
		}
		return e, nil
	}})

	_, err := st.Apply()
	s.Require().EqualError(err, fmt.Sprintf("thing store: %s: migration 2 (number the run): run is not a number", st.Path))
	s.Equal(body, s.contents(st), "the first migration's work never reached the file")
	entries, err := os.ReadDir(filepath.Dir(st.Path))
	s.Require().NoError(err)
	s.Len(entries, 1, "and no temporary file is left beside it")
}

func (s *migrateSuite) TestAMigrationReturningNoEntryFails() {
	st := s.store("{\"id\":\"a\"}\n", Migration{Name: "drop", To: thing(1), Up: func(Entry) (Entry, error) { return nil, nil }})

	_, err := st.Apply()
	s.Require().EqualError(err, fmt.Sprintf("thing store: %s: migration 1 (drop): returned no entry", st.Path))
}

func (s *migrateSuite) TestAMalformedLineNamesItsLine() {
	st := s.store("{\"id\":\"a\"}\nnot json\n", renameMember(1, "attempt", "run"))

	_, err := st.Apply()
	s.Require().ErrorContains(err, fmt.Sprintf("thing store: %s:2: ", st.Path))
}

// A reader that takes entries for headers would rewrite the store as its last one, so a
// second header is refused before anything is written.
func (s *migrateSuite) TestASecondHeaderLineIsRefusedAndTheFileKept() {
	body := "{\"plan\":\"p\"}\n{\"id\":\"a\",\"attempt\":1}\n{\"plan\":\"q\"}\n"
	st := s.store(body, renameMember(1, "attempt", "run"))

	_, err := st.Apply()

	s.Require().EqualError(err, fmt.Sprintf("thing store: %s: lines 1, 3 are each a header line, and a store has one:"+
		" rewriting it would keep the last and drop the rest, so nothing was migrated", st.Path))
	s.Equal(body, s.contents(st), "not a byte moved")
}

func (s *migrateSuite) TestAnUnfinishedLastLineIsDroppedAndSaid() {
	st := s.store("{\"id\":\"a\",\"attempt\":1}\n{\"id\":\"b\",\"att", renameMember(1, "attempt", "run"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.True(res.Unfinished)
	s.Equal("{\"api_version\":\"magus.thing/v1alpha1\"}\n{\"id\":\"a\",\"run\":1}\n", s.contents(st))
	s.Contains(res.String(), "; dropped a last line no writer finished")
}
