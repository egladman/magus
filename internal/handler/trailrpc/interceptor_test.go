package trailrpc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	// Blank-imported so every magus.*.v1 service descriptor is registered in protoregistry.GlobalFiles
	// for TestKnownVerbs to enumerate. This is the whole point of the ratchet: a new service/method
	// linked into the daemon is visible here, so an unclassified verb cannot ship unnoticed.
	_ "github.com/egladman/magus/proto/gen/go/magus/activity/v1"
	_ "github.com/egladman/magus/proto/gen/go/magus/graph/v1"
	_ "github.com/egladman/magus/proto/gen/go/magus/job/v1"
	_ "github.com/egladman/magus/proto/gen/go/magus/memory/v1"
	_ "github.com/egladman/magus/proto/gen/go/magus/metrics/v1"
	_ "github.com/egladman/magus/proto/gen/go/magus/query/v1"
	_ "github.com/egladman/magus/proto/gen/go/magus/status/v1"
	_ "github.com/egladman/magus/proto/gen/go/magus/viewer/v1"

	"github.com/egladman/magus/internal/trail"
	tokenv1 "github.com/egladman/magus/proto/gen/go/magus/token/v1"
	"github.com/egladman/magus/proto/gen/go/magus/token/v1/tokenv1connect"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		method   string
		mutating bool
		known    bool
	}{
		{"RevokeToken", true, true},
		{"ClearCache", true, true},
		{"DeleteMemory", true, true},
		{"UpdateMemory", true, true},
		{"PutMemory", true, true},
		{"RotateActivities", true, true},
		{"SyncGraph", true, true},
		{"ListTokens", false, true},
		{"GetStatus", false, true},
		{"StreamStatus", false, true},
		{"FrobnicateWorkspace", true, false}, // unknown verb: fail-closed to mutating, flagged unknown
		{"Listen", true, false},              // single-word (no internal capital): leadingWord returns it whole, unknown
	}
	for _, c := range cases {
		mut, known := classify(c.method)
		if mut != c.mutating || known != c.known {
			t.Errorf("classify(%q) = (%v,%v), want (%v,%v)", c.method, mut, known, c.mutating, c.known)
		}
	}
}

func TestMethodName(t *testing.T) {
	if got := methodName("/magus.token.v1.TokenService/RevokeToken"); got != "RevokeToken" {
		t.Errorf("methodName = %q, want RevokeToken", got)
	}
	if got := methodName("Bare"); got != "Bare" {
		t.Errorf("methodName(bare) = %q, want Bare", got)
	}
}

// TestKnownVerbs is the arch ratchet: every method on every magus.*.v1 service linked into this test
// binary must classify to a KNOWN verb. A new RPC whose leading word is in neither the mutating nor the
// read set fails here, forcing the author to add it to one bucket in interceptor.go - which is the moment
// they decide whether it needs auditing. This is what keeps the audit boundary from silently drifting as
// the service surface grows.
func TestKnownVerbs(t *testing.T) {
	var unknown []string
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "magus.") {
			return true
		}
		svcs := fd.Services()
		for i := 0; i < svcs.Len(); i++ {
			methods := svcs.Get(i).Methods()
			for j := 0; j < methods.Len(); j++ {
				name := string(methods.Get(j).Name())
				if _, known := classify(name); !known {
					unknown = append(unknown, string(svcs.Get(i).FullName())+"."+name)
				}
			}
		}
		return true
	})
	if len(unknown) > 0 {
		t.Fatalf("unclassified RPC verbs (add each verb to trailrpc's mutating or read set and decide auditing): %v", unknown)
	}
}

// fakeTokenService is a minimal TokenServiceHandler that always succeeds, so the interceptor's recording
// is exercised over a real Connect handler+client roundtrip (AnyRequest cannot be faked - it has
// unexported methods - so a real call is the only way to drive Spec().Procedure).
type fakeTokenService struct{}

func (fakeTokenService) ListTokens(context.Context, *connect.Request[tokenv1.ListTokensRequest]) (*connect.Response[tokenv1.ListTokensResponse], error) {
	return connect.NewResponse(&tokenv1.ListTokensResponse{}), nil
}
func (fakeTokenService) RevokeToken(context.Context, *connect.Request[tokenv1.RevokeTokenRequest]) (*connect.Response[tokenv1.RevokeTokenResponse], error) {
	return connect.NewResponse(&tokenv1.RevokeTokenResponse{}), nil
}

func TestInterceptorRecordsMutationSkipsRead(t *testing.T) {
	dir := t.TempDir()
	path, handler := tokenv1connect.NewTokenServiceHandler(
		fakeTokenService{},
		connect.WithInterceptors(Interceptor(dir, "operator", trail.KindTokenLifecycle)),
	)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := tokenv1connect.NewTokenServiceClient(srv.Client(), srv.URL)
	ctx := context.Background()

	// A read is NOT recorded.
	if _, err := client.ListTokens(ctx, connect.NewRequest(&tokenv1.ListTokensRequest{})); err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	// A mutation IS recorded, with the server-stamped actor and the method as the action.
	if _, err := client.RevokeToken(ctx, connect.NewRequest(&tokenv1.RevokeTokenRequest{Identifier: "abc"})); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	events, err := trail.ReadRecent(dir, 10)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want exactly 1 (the mutation; the read must not record): %+v", len(events), events)
	}
	got := events[0]
	if got.Action != "RevokeToken" || got.Actor != "operator" || got.Kind != trail.KindTokenLifecycle || got.Outcome != trail.OutcomeOK {
		t.Errorf("recorded event = %+v, want RevokeToken/operator/token_lifecycle/ok", got)
	}
}

// TestInterceptorAuditReadsRecordsRead pins the WithAuditReads opt-in: with it set, a read verb
// (ListTokens) IS recorded, alongside the mutation. This is the memory service's mode; the token
// service (TestInterceptorRecordsMutationSkipsRead) leaves the option off and skips the read.
func TestInterceptorAuditReadsRecordsRead(t *testing.T) {
	dir := t.TempDir()
	path, handler := tokenv1connect.NewTokenServiceHandler(
		fakeTokenService{},
		connect.WithInterceptors(Interceptor(dir, "operator", trail.KindMemory, WithAuditReads())),
	)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := tokenv1connect.NewTokenServiceClient(srv.Client(), srv.URL)
	ctx := context.Background()

	// Both a read and a mutation are recorded when audit-reads is on.
	if _, err := client.ListTokens(ctx, connect.NewRequest(&tokenv1.ListTokensRequest{})); err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if _, err := client.RevokeToken(ctx, connect.NewRequest(&tokenv1.RevokeTokenRequest{Identifier: "abc"})); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}

	events, err := trail.ReadRecent(dir, 10)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("recorded %d events, want 2 (the read AND the mutation, audit-reads on): %+v", len(events), events)
	}
	// Newest-first: the mutation, then the read. Both stamped memory/operator/ok.
	got := []struct{ action, actor string }{
		{events[0].Action, events[0].Actor},
		{events[1].Action, events[1].Actor},
	}
	want := []struct{ action, actor string }{
		{"RevokeToken", "operator"},
		{"ListTokens", "operator"},
	}
	if got[0] != want[0] || got[1] != want[1] {
		t.Errorf("recorded events = %+v, want RevokeToken then ListTokens (both operator)", got)
	}
	if events[0].Kind != trail.KindMemory || events[1].Kind != trail.KindMemory {
		t.Errorf("recorded kinds = %v,%v, want both %v", events[0].Kind, events[1].Kind, trail.KindMemory)
	}
}

// erroringTokenService fails RevokeToken, so the interceptor's error-outcome branch is exercised: a failed
// mutation is still recorded, with OutcomeError and the error text.
type erroringTokenService struct{ fakeTokenService }

func (erroringTokenService) RevokeToken(context.Context, *connect.Request[tokenv1.RevokeTokenRequest]) (*connect.Response[tokenv1.RevokeTokenResponse], error) {
	return nil, connect.NewError(connect.CodeNotFound, errors.New("no token matches"))
}

func TestInterceptorRecordsFailedMutation(t *testing.T) {
	dir := t.TempDir()
	path, handler := tokenv1connect.NewTokenServiceHandler(
		erroringTokenService{},
		connect.WithInterceptors(Interceptor(dir, "operator", trail.KindTokenLifecycle)),
	)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := tokenv1connect.NewTokenServiceClient(srv.Client(), srv.URL)
	if _, err := client.RevokeToken(context.Background(), connect.NewRequest(&tokenv1.RevokeTokenRequest{Identifier: "x"})); err == nil {
		t.Fatal("expected RevokeToken to fail")
	}

	events, err := trail.ReadRecent(dir, 10)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1 (the failed mutation is still audited)", len(events))
	}
	if events[0].Outcome != trail.OutcomeError || events[0].Error == "" {
		t.Errorf("recorded event = %+v, want OutcomeError with a non-empty error", events[0])
	}
}
