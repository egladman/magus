package diag

import (
	"context"
	"errors"
	"testing"
)

// testURL is a stand-in docs-URL layout for a fake "TST" domain, so the framework can be tested without
// any consumer's real code catalog.
func testURL(c Code) string { return "https://example/docs/" + string(c) + ".md" }

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
