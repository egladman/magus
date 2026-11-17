package job

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
)

// Migrate brings the store file at path from migration found to the version given with it
// to [WithMigrations], and refuses a file at a newer one. lines are the file as read, which
// a migration that refuses reads rows from. It runs holding the store's lock, and the store
// reads the file again after.
type Migrate func(ctx context.Context, path string, found int, lines []codec.Line) error

// NewerWriterError is a store file a newer writer stamped: a migration past this build's,
// or a feature the header requires that this build lacks. Nothing in the file was read.
type NewerWriterError struct {
	Path string
	// Found and Known are the file's migration and this build's, both zero for a
	// feature refusal.
	Found, Known int
	// Lacks are the features the file requires that this build does not implement.
	Lacks []string
}

func (e *NewerWriterError) Error() string {
	if len(e.Lacks) > 0 {
		return fmt.Sprintf("%s requires %s, which this build (%s) lacks", e.Path, quoteAll(e.Lacks), RecordAPIVersion)
	}
	return fmt.Sprintf("job store %s is at migration %d, past this build's %d", e.Path, e.Found, e.Known)
}

// NewerRecordsError is a record a newer writer stored that this build may read but not
// write: a row requiring features this build lacks, or an attempt of another version or
// kind.
type NewerRecordsError struct {
	// Record names the row or attempt, and Path the file when known.
	Record, Path string
	// Lacks are the features a row requires that this build does not implement.
	Lacks []string
	// Want and Got are an attempt line's type, set when the refusal is of one.
	Want, Got apiversion.TypeMeta
}

func (e *NewerRecordsError) Error() string {
	if len(e.Lacks) > 0 {
		return fmt.Sprintf("%s requires %s, which this build (%s) lacks", e.Record, quoteAll(e.Lacks), RecordAPIVersion)
	}
	return fmt.Sprintf("%s: %s is %s %s, and this build reads %s %s", e.Path, e.Record, e.Got.APIVersion, e.Got.Kind, e.Want.APIVersion, e.Want.Kind)
}

// RecordVersionError is a line of the store stamped with an apiVersion this build does not
// read. A line of a known version but another kind is a [*codec.LineError] instead.
type RecordVersionError struct {
	Path string
	Line int
	Want apiversion.APIVersion
	Got  apiversion.APIVersion
}

func (e *RecordVersionError) Error() string {
	return fmt.Sprintf("job: %s:%d is %s, and this build reads %s: %s", e.Path, e.Line, e.Got, e.Want, StaleStoreFix(e.Path))
}

// LegacyDocumentError is the store's directory still holding the plan an older writer kept
// as one JSON document, jobs.json. Reading past it would show an empty plan while the rows
// sit beside it, and an empty plan reads as nothing leased.
type LegacyDocumentError struct {
	// Path is the store file, and Document the jobs.json beside it.
	Path, Document string
	Err            error
}

func (e *LegacyDocumentError) Error() string {
	return fmt.Sprintf("cannot open job plan %s: %v: once no older writer writes it, set it aside with `mv %s %s.old`",
		e.Path, e.Err, shellQuote(e.Document), shellQuote(e.Document))
}

func (e *LegacyDocumentError) Unwrap() error { return e.Err }

// StaleStoreFix is how a person gets past the job store at path, which an older writer
// wrote. A newer writer's lines are kept whole.
func StaleStoreFix(path string) string {
	return fmt.Sprintf("move %s aside, or delete it, and fork the jobs again", path)
}

// refuseRecord refuses the record at line of the store, got where this build reads only
// want. Another version is a [*RecordVersionError], another kind a line no writer stores.
func (s *Store) refuseRecord(want, got apiversion.TypeMeta, line int) error {
	if got.APIVersion != want.APIVersion {
		return s.wrap(&RecordVersionError{Path: s.path, Line: line, Want: want.APIVersion, Got: got.APIVersion})
	}
	return s.wrap(&codec.LineError{Path: s.path, Line: line, Err: &apiversion.RefusedError{Want: want, Got: got}})
}

// quoteAll is names each quoted, joined by commas.
func quoteAll(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return strings.Join(quoted, ", ")
}

// shellQuote is p as one POSIX shell word.
func shellQuote(p string) string {
	if p != "" && strings.Trim(p, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+@:,") == "" {
		return p
	}
	return fmt.Sprintf("'%s'", strings.ReplaceAll(p, "'", `'\''`))
}
