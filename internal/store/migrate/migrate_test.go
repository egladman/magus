package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/suite"
)

type migrateSuite struct {
	suite.Suite
}

func TestMigrateSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(migrateSuite))
}

// renameMember is a migration that renames one member of every entry carrying it.
func renameMember(from, to string) Migration {
	return Migration{Name: from + " to " + to, Up: func(e Entry) (Entry, error) {
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
		renameMember("attempt", "run"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.Equal(result{Store: "thing store", Path: st.Path, From: 0, To: 1, Entries: 3}, res)
	s.Equal("{\"migration\":1,\"plan\":\"p\"}\n{\"id\":\"a\",\"run\":1}\n{\"id\":\"b\"}\n{\"id\":\"a\",\"run\":2}\n", s.contents(st),
		"every line is migrated, superseded ones included, and the header carries the version")
	s.Equal("migrated thing store "+st.Path+" from 0 to 1 (3 entries)", res.String())
}

func (s *migrateSuite) TestUpSeveralVersionsInOrder() {
	st := s.store("{\"migration\":1}\n{\"id\":\"a\",\"run\":1}\n",
		renameMember("attempt", "run"), renameMember("run", "try"), renameMember("try", "pass"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.Equal(1, res.From)
	s.Equal(3, res.To)
	s.Equal("{\"migration\":3}\n{\"id\":\"a\",\"pass\":1}\n", s.contents(st), "only the migrations past 1 ran, in order")
}

func (s *migrateSuite) TestAddsAHeaderToAFileWithoutOne() {
	st := s.store("{\"id\":\"a\",\"attempt\":1}\n", renameMember("attempt", "run"))

	_, err := st.Apply()
	s.Require().NoError(err)
	s.Equal("{\"migration\":1}\n{\"id\":\"a\",\"run\":1}\n", s.contents(st))
}

func (s *migrateSuite) TestAlreadyCurrentIsLeftAlone() {
	body := "{\"id\":\"a\",\"run\":1}\n{\"migration\":1}\n"
	st := s.store(body, renameMember("attempt", "run"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.False(res.Migrated())
	s.Equal(1, res.From)
	s.Equal(body, s.contents(st), "not a byte moved")
}

func (s *migrateSuite) TestAbsentFileIsLeftAbsent() {
	st := s.store("", renameMember("attempt", "run"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.False(res.Migrated())
	s.NoFileExists(st.Path)
}

func (s *migrateSuite) TestNewerIsRefusedNamingBothVersions() {
	body := "{\"migration\":4}\n{\"id\":\"a\"}\n"
	st := s.store(body, renameMember("attempt", "run"))

	_, err := st.Apply()
	var newer *NewerError
	s.Require().ErrorAs(err, &newer)
	s.Equal(NewerError{Store: "thing store", Path: st.Path, Found: 4, Known: 1}, *newer)
	s.Equal("thing store "+st.Path+" is at migration 4, newer than this magus knows (1): update magus", err.Error())
	s.Equal(body, s.contents(st))
}

func (s *migrateSuite) TestAFailingMigrationLeavesTheOldFileIntact() {
	body := "{\"id\":\"a\",\"attempt\":1}\n{\"id\":\"b\",\"attempt\":\"x\"}\n"
	st := s.store(body, renameMember("attempt", "run"), Migration{Name: "number the run", Up: func(e Entry) (Entry, error) {
		var n int
		if err := json.Unmarshal(e["run"], &n); err != nil {
			return nil, errors.New("run is not a number")
		}
		return e, nil
	}})

	_, err := st.Apply()
	s.Require().EqualError(err, "thing store: "+st.Path+": migration 2 (number the run): run is not a number")
	s.Equal(body, s.contents(st), "the first migration's work never reached the file")
	entries, err := os.ReadDir(filepath.Dir(st.Path))
	s.Require().NoError(err)
	s.Len(entries, 1, "and no temporary file is left beside it")
}

func (s *migrateSuite) TestAMigrationReturningNoEntryFails() {
	st := s.store("{\"id\":\"a\"}\n", Migration{Name: "drop", Up: func(Entry) (Entry, error) { return nil, nil }})

	_, err := st.Apply()
	s.Require().EqualError(err, "thing store: "+st.Path+": migration 1 (drop): returned no entry")
}

func (s *migrateSuite) TestAMalformedLineNamesItsLine() {
	st := s.store("{\"id\":\"a\"}\nnot json\n", renameMember("attempt", "run"))

	_, err := st.Apply()
	s.Require().ErrorContains(err, "thing store: "+st.Path+":2: ")
}

func (s *migrateSuite) TestAnUnfinishedLastLineIsDroppedAndSaid() {
	st := s.store("{\"id\":\"a\",\"attempt\":1}\n{\"id\":\"b\",\"att", renameMember("attempt", "run"))

	res, err := st.Apply()
	s.Require().NoError(err)
	s.True(res.Unfinished)
	s.Equal("{\"migration\":1}\n{\"id\":\"a\",\"run\":1}\n", s.contents(st))
	s.Contains(res.String(), "; dropped a last line no writer finished")
}
