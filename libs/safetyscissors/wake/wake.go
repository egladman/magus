package wake

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

// APIVersion is the version every wake record is stamped with. A reader refuses a line of
// any other version.
const APIVersion apiversion.APIVersion = "safetyscissors.wake/v1alpha1"

// The kinds a wake record line carries beside [APIVersion].
const (
	KindTerms  apiversion.Kind = "Terms"
	KindStart  apiversion.Kind = "Start"
	KindReport apiversion.Kind = "Report"
	KindStop   apiversion.Kind = "Stop"
)

// Terms are the bounds a person set on a wake. They are written once by [Store.Begin] and
// never widened. Every time is a unix second.
type Terms struct {
	// ID names the wake. It is not empty and holds no whitespace.
	ID string `json:"id"`
	// Subject is what the wake works on, opaque to the package.
	Subject string `json:"subject,omitempty"`
	// By is who began the wake.
	By job.Origin `json:"by,omitzero"`
	// At is when the wake began. The store sets it; a caller supplying one is refused.
	At int64 `json:"at"`
	// Until is the deadline: no turn starts at or after it, and a turn still open at it
	// lapses. Required, and after At.
	Until int64 `json:"until"`
	// MaxWakes is how many reports the wake may file. Required.
	MaxWakes int `json:"max_wakes"`
	// MaxIdle is how many reports in a row may act on nothing. Required.
	MaxIdle int `json:"max_idle"`
	// MinGap is the fewest seconds from one report to the next start. Required.
	MinGap int64 `json:"min_gap"`
	// MaxUSD is the spend bound in US dollars, 0 for none. Under a bound every report is
	// priced, which needs [WithPricer].
	MaxUSD float64 `json:"max_usd,omitzero"`
}

// Start is a wake's turn begun and not yet reported. N numbers the turns from 1.
type Start struct {
	Wake string `json:"wake"`
	N    int    `json:"n"`
	// At is when the store recorded the start.
	At int64 `json:"at"`
}

// Report is what one turn of a wake did, filed by [Store.RecordWake].
type Report struct {
	// Wake and N name the turn, which [Store.Start] began.
	Wake string `json:"wake"`
	N    int    `json:"n"`
	// Started and Ended bound the turn: the store sets them from the start and from the
	// report's own filing. A caller supplying either is refused.
	Started int64 `json:"started"`
	Ended   int64 `json:"ended"`
	// Acted counts the items the turn acted on, and Pending those still waiting on
	// something outside the turn. A report with both 0 ends the wake as done.
	Acted   int `json:"acted"`
	Pending int `json:"pending"`
	// Held is what the turn found and left for a person. A held item does not hold the
	// wake open.
	Held []Held `json:"held,omitempty"`
	// Model is the key the pricer prices Calls by. It assigns no model to anything.
	Model string `json:"model,omitempty"`
	// Calls is the token usage of each model call the turn made.
	Calls []Usage `json:"calls,omitempty"`
	// USD is what Calls cost, priced by the store when the wake has a spend bound and 0
	// otherwise. A caller supplying it is refused.
	USD float64 `json:"usd,omitzero"`
	// Evidence is what the turn cites for its counts, opaque to the package.
	Evidence []string `json:"evidence,omitempty"`
}

// Held is one item a turn left for a person.
type Held struct {
	Item    string `json:"item"`
	Why     string `json:"why,omitempty"`
	Command string `json:"command,omitempty"`
}

// Usage is one model call's tokens, by the classes [budget.Price] has a rate for.
type Usage struct {
	Input        int64 `json:"input,omitzero"`
	CacheWrite5m int64 `json:"cache_write_5m,omitzero"`
	CacheWrite1h int64 `json:"cache_write_1h,omitzero"`
	CacheRead    int64 `json:"cache_read,omitzero"`
	Output       int64 `json:"output,omitzero"`
}

// Stop is a person ending a wake by [Store.Stop].
type Stop struct {
	Wake string     `json:"wake"`
	At   int64      `json:"at"`
	By   job.Origin `json:"by,omitzero"`
	Why  string     `json:"why,omitempty"`
}

// Wake is what the record holds for one wake: facts only. It stores no end; [Decide]
// derives one from these facts each time it is asked.
type Wake struct {
	Terms Terms
	// N is how many reports the wake has filed, and Last the newest, zero before the first.
	N    int
	Last Report
	// Idle is how many of the newest reports in a row acted on nothing.
	Idle int
	// SpentUSD is the sum of the reports' USD.
	SpentUSD float64
	// Open is the turn started and not yet reported, its N 0 when there is none.
	Open Start
	// Stopped is the person's stop, its Wake "" when there is none.
	Stopped Stop
}

// validID reports whether id may name a wake. A record key separates its parts by a
// space, so an ID holds no whitespace.
func validID(id string) bool {
	return id != "" && !strings.ContainsFunc(id, unicode.IsSpace)
}

func (t Terms) key() string  { return "terms " + t.ID }
func (s Start) key() string  { return fmt.Sprintf("start %s %d", s.Wake, s.N) }
func (r Report) key() string { return fmt.Sprintf("report %s %d", r.Wake, r.N) }
func (s Stop) key() string   { return "stop " + s.Wake }

func header(kind apiversion.Kind) apiversion.TypeMeta {
	return apiversion.TypeMeta{APIVersion: APIVersion, Kind: kind}
}

// The *Fields types have their entry's fields and tags but none of its methods, so
// encoding one cannot call MarshalJSON again.
type (
	termsFields  Terms
	startFields  Start
	reportFields Report
	stopFields   Stop
)

type termsWire struct {
	apiversion.TypeMeta
	termsFields
}

type startWire struct {
	apiversion.TypeMeta
	startFields
}

type reportWire struct {
	apiversion.TypeMeta
	reportFields
}

type stopWire struct {
	apiversion.TypeMeta
	stopFields
}

// MarshalJSON encodes the terms stamped with [APIVersion] and [KindTerms].
func (t Terms) MarshalJSON() ([]byte, error) {
	return codec.Marshal(termsWire{header(KindTerms), termsFields(t)})
}

// UnmarshalJSON decodes terms, refusing a line of another version or kind with an
// [*apiversion.RefusedError].
func (t *Terms) UnmarshalJSON(b []byte) error {
	var w termsWire
	if err := decode(b, &w, &w.TypeMeta, KindTerms); err != nil {
		return err
	}
	*t = Terms(w.termsFields)
	return nil
}

// MarshalJSON encodes the start stamped with [APIVersion] and [KindStart].
func (s Start) MarshalJSON() ([]byte, error) {
	return codec.Marshal(startWire{header(KindStart), startFields(s)})
}

// UnmarshalJSON decodes a start, refusing a line of another version or kind with an
// [*apiversion.RefusedError].
func (s *Start) UnmarshalJSON(b []byte) error {
	var w startWire
	if err := decode(b, &w, &w.TypeMeta, KindStart); err != nil {
		return err
	}
	*s = Start(w.startFields)
	return nil
}

// MarshalJSON encodes the report stamped with [APIVersion] and [KindReport].
func (r Report) MarshalJSON() ([]byte, error) {
	return codec.Marshal(reportWire{header(KindReport), reportFields(r)})
}

// UnmarshalJSON decodes a report, refusing a line of another version or kind with an
// [*apiversion.RefusedError].
func (r *Report) UnmarshalJSON(b []byte) error {
	var w reportWire
	if err := decode(b, &w, &w.TypeMeta, KindReport); err != nil {
		return err
	}
	*r = Report(w.reportFields)
	return nil
}

// MarshalJSON encodes the stop stamped with [APIVersion] and [KindStop].
func (s Stop) MarshalJSON() ([]byte, error) {
	return codec.Marshal(stopWire{header(KindStop), stopFields(s)})
}

// UnmarshalJSON decodes a stop, refusing a line of another version or kind with an
// [*apiversion.RefusedError].
func (s *Stop) UnmarshalJSON(b []byte) error {
	var w stopWire
	if err := decode(b, &w, &w.TypeMeta, KindStop); err != nil {
		return err
	}
	*s = Stop(w.stopFields)
	return nil
}

func decode(b []byte, wire any, got *apiversion.TypeMeta, kind apiversion.Kind) error {
	if err := codec.Unmarshal(b, wire); err != nil {
		return fmt.Errorf("wake: decode %s: %w", kind, err)
	}
	if want := header(kind); *got != want {
		return fmt.Errorf("wake: decode %s: %w", kind, &apiversion.RefusedError{Want: want, Got: *got})
	}
	return nil
}

// ErrUnknownWake is a call naming a wake the store does not hold.
var ErrUnknownWake = errors.New("wake: unknown wake")

// ErrDuplicateWake is [Store.Begin] given an ID the store already holds. Terms are
// written once, so beginning again under the same ID would widen them.
var ErrDuplicateWake = errors.New("wake: a wake with this ID was already begun")

// TermsError is one problem [Store.Begin] found in a [Terms]. Begin reports every
// problem at once, joined.
type TermsError struct {
	// Field is the field's name, such as "Until".
	Field   string
	Problem string
}

func (e *TermsError) Error() string {
	return fmt.Sprintf("wake: terms %s: %s", e.Field, e.Problem)
}

// EndedError is a write to a wake that has ended. End and At are what [Decide] answered.
type EndedError struct {
	Wake string
	End  End
	At   int64
}

func (e *EndedError) Error() string {
	return fmt.Sprintf("wake %s ended (%s) at %d", e.Wake, e.End, e.At)
}

// OpenError is [Store.Start] while turn N is started and not yet reported. Report it
// with [Store.RecordWake] first.
type OpenError struct {
	Wake string
	N    int
}

func (e *OpenError) Error() string {
	return fmt.Sprintf("wake %s: turn %d is open: record its report before starting another", e.Wake, e.N)
}

// TooSoonError is [Store.Start] before the minimum gap allows.
type TooSoonError struct {
	Wake           string
	Now, NotBefore int64
}

func (e *TooSoonError) Error() string {
	return fmt.Sprintf("wake %s: a turn may not start before %d, and it is %d", e.Wake, e.NotBefore, e.Now)
}

// NotNextError is a report for a turn other than the open one. Want is the open turn's
// N, 0 when none is open.
type NotNextError struct {
	Wake      string
	Want, Got int
}

func (e *NotNextError) Error() string {
	if e.Want == 0 {
		return fmt.Sprintf("wake %s: report %d names no open turn, so start one first", e.Wake, e.Got)
	}
	return fmt.Sprintf("wake %s: report %d is not for the open turn %d", e.Wake, e.Got, e.Want)
}

// ReportError is one problem [Store.RecordWake] found in a [Report]. RecordWake reports
// every problem at once, joined.
type ReportError struct {
	Wake    string
	N       int
	Field   string
	Problem string
}

func (e *ReportError) Error() string {
	return fmt.Sprintf("wake %s: report %d %s: %s", e.Wake, e.N, e.Field, e.Problem)
}

// UnpricedError is a report under a spend bound that the pricer cannot price. Class is
// the token class with tokens and a 0 rate, "" when the pricer does not know Model at
// all. A 0 rate is unknown, never free.
type UnpricedError struct {
	Model string
	Class string
}

func (e *UnpricedError) Error() string {
	if e.Class == "" {
		return fmt.Sprintf("wake: the pricer has no price for model %q", e.Model)
	}
	return fmt.Sprintf("wake: model %q has no %s rate, and a 0 rate is unknown, not free", e.Model, e.Class)
}

// CorruptError is a line of the [Log] the store cannot fold. The line does not decode,
// is of another version or kind, or does not follow the record before it. Line counts
// from 1, in the order [Log.Read] yielded it.
type CorruptError struct {
	Line int
	Err  error
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("wake: log line %d: %v", e.Line, e.Err)
}

func (e *CorruptError) Unwrap() error { return e.Err }
