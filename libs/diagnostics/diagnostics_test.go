package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// registerTST reserves the TST prefix for a fake catalog, as a composition point would.
func registerTST(t *testing.T) {
	t.Helper()
	if err := RegisterCatalog("tst", []Code{"TST0001", "TST0002"}); err != nil {
		t.Fatalf("RegisterCatalog(tst) = %v", err)
	}
}

func TestPrefixIsTheLeadingCapitals(t *testing.T) {
	if got := Prefix("ACME1001"); got != "ACME" {
		t.Errorf("Prefix(ACME1001) = %q, want ACME", got)
	}
}

func TestPrefixOfACodeWithNoCapitalsIsEmpty(t *testing.T) {
	if got := Prefix("1001"); got != "" {
		t.Errorf("Prefix(1001) = %q, want empty", got)
	}
}

func TestRegisterCatalogReservesItsPrefix(t *testing.T) {
	registerTST(t)
	if !slices.Contains(ReservedPrefixes(), "TST") {
		t.Errorf("ReservedPrefixes() = %v, want TST among them", ReservedPrefixes())
	}
}

func TestRegisterTheSamePrefixForItsOwnerAgainIsANoOp(t *testing.T) {
	registerTST(t)
	if err := Register("TST", "tst"); err != nil {
		t.Errorf("Register(TST, tst) again = %v, want nil", err)
	}
}

func TestOwnerIsWhoRegistered(t *testing.T) {
	registerTST(t)
	if o, ok := Owner("TST0009"); !ok || o != "tst" {
		t.Errorf("Owner(TST0009) = %q, %v, want tst", o, ok)
	}
}

func TestOwnerOfAnUnregisteredPrefixIsNone(t *testing.T) {
	if _, ok := Owner("NOPE0001"); ok {
		t.Error("Owner(NOPE0001) found an owner nobody registered")
	}
}

func TestRegisterRefusesAnotherOwnersPrefix(t *testing.T) {
	registerTST(t)
	if err := Register("TST", "other"); err == nil || !strings.Contains(err.Error(), "collides with TST") {
		t.Errorf("Register(TST, other) = %v, want a collision with TST", err)
	}
}

func TestRegisterRefusesAPrefixOfAReservedOne(t *testing.T) {
	registerTST(t)
	if err := Register("TS", "other"); err == nil || !strings.Contains(err.Error(), "collides with TST") {
		t.Errorf("Register(TS, other) = %v, want a collision with TST", err)
	}
}

func TestRegisterRefusesAPrefixAReservedOneStarts(t *testing.T) {
	registerTST(t)
	if err := Register("TSTX", "other"); err == nil || !strings.Contains(err.Error(), "collides with TST") {
		t.Errorf("Register(TSTX, other) = %v, want a collision with TST", err)
	}
}

func TestRegisterRefusesAMalformedPrefix(t *testing.T) {
	if err := Register("ws", "other"); err == nil || !strings.Contains(err.Error(), "capital letters") {
		t.Errorf("Register(ws, other) = %v, want a malformed-prefix error", err)
	}
}

func TestEventOfCarriesTheCapturedURL(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", New(testURL).Errorf("TST0001", "boom"))
	ev, ok := ErrorEvent(err, "a:build")
	want := Event{Code: "TST0001", Message: "boom", Unit: "a:build", URL: "https://example/docs/TST0001.md"}
	if !ok || ev != want {
		t.Errorf("ErrorEvent = %+v, %v, want %+v", ev, ok, want)
	}
}

func TestEventOfAnUncodedErrorIsNone(t *testing.T) {
	if _, ok := ErrorEvent(errors.New("plain"), "a:build"); ok {
		t.Error("ErrorEvent found a code in a plain error")
	}
}

// testURL is a stand-in docs-URL layout for a fake "TST" domain, so the framework can be tested without
// any consumer's real code catalog.
func testURL(c Code) string { return fmt.Sprintf("https://example/docs/%s.md", string(c)) }

func TestErrorRendering(t *testing.T) {
	d := New(testURL)
	got := d.Errorf(Code("TST0001"), "bad thing %d", 7).Error()
	want := "[TST0001] bad thing 7\n  see: https://example/docs/TST0001.md"
	if got != want {
		t.Errorf("Errorf render = %q, want %q", got, want)
	}
	// A bare literal (an errors.Is target) has no URL, so it renders without the see: line.
	bare := (&Error{Code: "TST0001", Msg: "x"}).Error()
	if bare != "[TST0001] x" {
		t.Errorf("bare render = %q, want %q", bare, "[TST0001] x")
	}
}

func TestIsMatching(t *testing.T) {
	d := New(testURL)
	err := d.Errorf(Code("TST0001"), "boom")
	if !errors.Is(err, ErrSentinel) {
		t.Error("a diagnostic error must match ErrSentinel")
	}
	// The idiomatic sentinel form: a bare Code as the errors.Is target.
	if !errors.Is(err, Code("TST0001")) {
		t.Error("must match a same-code Code sentinel")
	}
	if errors.Is(err, Code("TST0002")) {
		t.Error("must NOT match a different-code Code sentinel")
	}
	// The struct-literal target form stays supported for back-compat.
	if !errors.Is(err, &Error{Code: "TST0001"}) {
		t.Error("must match a same-code target literal")
	}
	if errors.Is(err, &Error{Code: "TST0002"}) {
		t.Error("must NOT match a different-code target")
	}
	if errors.Is(errors.New("plain"), ErrSentinel) {
		t.Error("a plain error must not match ErrSentinel")
	}
	// A target that is neither ErrSentinel, a Code, nor an *Error takes Is's default (no match).
	if errors.Is(err, errors.New("some other error")) {
		t.Error("must not match an unrelated error target")
	}
}

// TestCodeIsError pins that a Code satisfies the error interface (so it can be an errors.Is sentinel) and
// renders as the bare code.
func TestCodeIsError(t *testing.T) {
	var e error = Code("TST0001")
	if e.Error() != "TST0001" {
		t.Errorf("Code.Error() = %q, want TST0001", e.Error())
	}
}

// TestWrapfChainsCause pins that a Wrapf error carries the code AND unwraps to its cause, so a pre-existing
// sentinel still matches via errors.Is while the error gains a lookupable code.
func TestWrapfChainsCause(t *testing.T) {
	d := New(testURL)
	sentinel := errors.New("underlying sentinel")
	err := d.Wrapf(Code("TST0001"), sentinel, "boom %d", 3)

	// The rendered message does NOT splice the cause; it is the coded form.
	if err.Error() != "[TST0001] boom 3\n  see: https://example/docs/TST0001.md" {
		t.Errorf("render = %q", err.Error())
	}
	// Both the code sentinel and the wrapped cause match.
	if !errors.Is(err, Code("TST0001")) {
		t.Error("must still match its own code")
	}
	if !errors.Is(err, sentinel) {
		t.Error("must unwrap to the wrapped cause")
	}
	// Unwrap is nil for a plain Errorf.
	if plain := d.Errorf(Code("TST0002"), "x"); plain.Unwrap() != nil {
		t.Error("a non-wrapping Errorf must Unwrap to nil")
	}
}

// A coded error formatted into another names its code inline, and the result carries one see:
// line, the outer one, even through a plain fmt wrapper between the two.
func TestNestedCodeRendersOneSeeLine(t *testing.T) {
	d := New(testURL)
	inner := d.Errorf(Code("TST0002"), "go reports no version")
	wrapped := fmt.Errorf("pkg: %w; install it", inner)

	got := d.Errorf(Code("TST0001"), "index not current for %v; fix it", wrapped).Error()

	want := "[TST0001] index not current for pkg: go reports no version (TST0002); install it; fix it\n" +
		"  see: https://example/docs/TST0001.md"
	if got != want {
		t.Errorf("nested render = %q, want %q", got, want)
	}
	if n := strings.Count(got, "see:"); n != 1 {
		t.Errorf("rendered %d see: lines, want 1", n)
	}
}

func TestInline(t *testing.T) {
	d := New(testURL)
	if got := Inline(d.Errorf(Code("TST0003"), "boom")); got != "boom (TST0003)" {
		t.Errorf("Inline(coded) = %q", got)
	}
	if got := Inline(errors.New("plain")); got != "plain" {
		t.Errorf("Inline(plain) = %q", got)
	}
	joined := errors.Join(errors.New("a"), d.Errorf(Code("TST0004"), "b"))
	if got := Inline(joined); got != "a\nb (TST0004)" {
		t.Errorf("Inline(joined) = %q", got)
	}
}

// Why is for a reader who asks for it, so no rendering of the error carries it.
func TestWhyNeverRenders(t *testing.T) {
	d := New(testURL)
	const why = "a stale index answers with symbols that no longer exist"
	err := d.Errorf(Code("TST0005"), "index is stale").WithWhy(why)

	if got := err.Error(); strings.Contains(got, why) {
		t.Errorf("Error() = %q, carries the why", got)
	}
	if got := Inline(fmt.Errorf("load: %w", err)); got != "load: index is stale (TST0005)" {
		t.Errorf("Inline = %q", got)
	}
	if got := d.Errorf(Code("TST0006"), "outer: %v", err).Error(); strings.Contains(got, why) {
		t.Errorf("nested Errorf = %q, carries the why", got)
	}
}

func TestFixCrossesIntoBuzzOnly(t *testing.T) {
	d := New(testURL)
	const fix = "run magus graph build"
	err := d.Errorf(Code("TST0010"), "index is stale").WithFix(fix)

	if got := err.Error(); strings.Contains(got, fix) {
		t.Errorf("Error() = %q, carries the fix", got)
	}
	want := map[string]string{
		"code":    "TST0010",
		"message": "index is stale",
		"url":     "https://example/docs/TST0010.md",
		"fix":     fix,
	}
	if got := err.BuzzError(); !maps.Equal(got, want) {
		t.Errorf("BuzzError = %v, want %v", got, want)
	}
	if _, ok := d.Errorf(Code("TST0010"), "no fix").BuzzError()["fix"]; ok {
		t.Error("BuzzError carries fix for an error without one")
	}
}

func TestRationale(t *testing.T) {
	d := New(testURL)
	inner := d.Errorf(Code("TST0007"), "inner").WithWhy("inner why")
	bare := d.Errorf(Code("TST0008"), "bare")

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"plain error", errors.New("plain"), ""},
		{"coded without why", bare, ""},
		{"coded with why", inner, "inner why"},
		{"through a fmt wrapper", fmt.Errorf("ctx: %w", inner), "inner why"},
		{"outer without why falls through", d.Wrapf(Code("TST0009"), inner, "outer"), "inner why"},
		{"outermost why wins", d.Wrapf(Code("TST0009"), inner, "outer").WithWhy("outer why"), "outer why"},
		{"joined", errors.Join(errors.New("a"), inner), "inner why"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Rationale(tc.err); got != tc.want {
				t.Errorf("Rationale = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDomainURLAndFormat(t *testing.T) {
	d := New(testURL)
	if got := d.URL(Code("TST0009")); got != "https://example/docs/TST0009.md" {
		t.Errorf("URL = %q", got)
	}
	if got := d.Format(Code("TST0009"), "note"); got != "[TST0009] note (see https://example/docs/TST0009.md)" {
		t.Errorf("Format = %q", got)
	}
}

// recordingSink captures emitted events.
type recordingSink struct{ events []Event }

func (r *recordingSink) Record(e Event) { r.events = append(r.events, e) }

func TestSinkPlumbing(t *testing.T) {
	// No sink installed: Emit is a silent no-op.
	Emit(context.Background(), Event{Code: "TST0001"})

	sink := &recordingSink{}
	ctx := WithSink(context.Background(), sink)
	Emit(ctx, Event{Code: "TST0001", Unit: "a:build"})
	Emit(ctx, Event{Code: "TST0002", Unit: "b:test"})
	if len(sink.events) != 2 || sink.events[0].Code != "TST0001" || sink.events[1].Unit != "b:test" {
		t.Errorf("recorded = %+v, want the two emitted events", sink.events)
	}
}
