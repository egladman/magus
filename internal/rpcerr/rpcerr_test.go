package rpcerr

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

func TestHTTPStatusMatchesConnect(t *testing.T) {
	t.Parallel()
	for c := connect.CodeCanceled; c <= connect.CodeUnauthenticated; c++ {
		rr := httptest.NewRecorder()
		_ = connect.NewErrorWriter().Write(rr, httptest.NewRequest(http.MethodPost, "/", nil), connect.NewError(c, errors.New("probe")))
		assert.Equal(t, rr.Code, httpStatus(c), c.String())
	}
}

// The MGS9xxx range is the server's, so a writer can be handed any reason in it; a reason
// missing from titles would fall back to its bare code in the Help link.
func TestEveryServerReasonHasATitle(t *testing.T) {
	t.Parallel()
	for _, code := range types.AllDiagnosticCodes() {
		if !strings.HasPrefix(string(code), "MGS9") {
			continue
		}
		assert.Contains(t, titles, code, "%s has no Help link title", code)
	}
}

func assertProto(t *testing.T, want, got proto.Message) {
	t.Helper()
	assert.True(t, proto.Equal(want, got), "got %v, want %v", got, want)
}

func mustStatus(t *testing.T, e Error) *status.Status {
	t.Helper()
	st, err := e.Status()
	require.NoError(t, err)
	return st
}

func unpack(t *testing.T, e Error) []proto.Message {
	t.Helper()
	var out []proto.Message
	for _, a := range mustStatus(t, e).GetDetails() {
		m, err := a.UnmarshalNew()
		require.NoError(t, err)
		out = append(out, m)
	}
	return out
}

func TestWorkspaceFailedCarriesEveryDiagnostic(t *testing.T) {
	t.Parallel()
	f := &types.WorkspaceFailure{
		Message: "magus: repo: magusfile: exec magusfile.buzz: [BZZ1005] ...",
		Diagnostics: []types.SourceDiagnostic{{
			Code: "BZZ1005", URL: "https://example.test/BZZ1005.md",
			File: "magusfile.buzz", Line: 3, Column: 7, Message: "cannot assign str to int",
		}},
	}
	e := WorkspaceFailed("/repo", f)
	st := mustStatus(t, e)

	assert.Equal(t, int32(connect.CodeFailedPrecondition), st.GetCode())
	assert.Equal(t, types.FormatDiagnostic(types.WorkspaceLoadFailed,
		"workspace /repo failed to load: magusfile.buzz:3:7 [BZZ1005] cannot assign str to int"), st.GetMessage())
	want := []proto.Message{
		&errdetails.ErrorInfo{Reason: "MGS3016", Domain: Domain, Metadata: map[string]string{
			"workspace": "/repo", "causeCode": "BZZ1005", "causeDomain": BuzzDomain,
			"file": "magusfile.buzz", "line": "3", "column": "7",
		}},
		&errdetails.PreconditionFailure{Violations: []*errdetails.PreconditionFailure_Violation{{
			Type: "BZZ1005", Subject: "magusfile.buzz:3:7", Description: "cannot assign str to int",
		}}},
		&errdetails.ResourceInfo{ResourceType: workspaceResource, ResourceName: "/repo", Description: "failed to load"},
		&errdetails.Help{Links: []*errdetails.Help_Link{
			{Description: "workspace failed to load", Url: types.CodeURL(types.WorkspaceLoadFailed)},
			{Description: "BZZ1005", Url: "https://example.test/BZZ1005.md"},
		}},
	}
	got := unpack(t, e)
	require.Len(t, got, len(want))
	for i := range want {
		assertProto(t, want[i], got[i])
	}
}

// A failure with no position still names a violation: PreconditionFailure with none says
// nothing about what to fix.
func TestWorkspaceFailedWithoutPosition(t *testing.T) {
	t.Parallel()
	got := unpack(t, WorkspaceFailed("/repo", &types.WorkspaceFailure{Message: "magus.yaml: unknown key"}))
	want := &errdetails.PreconditionFailure{Violations: []*errdetails.PreconditionFailure_Violation{{
		Type: loadErrorViolation, Subject: "/repo", Description: "magus.yaml: unknown key",
	}}}
	assertProto(t, want, got[1])
}

func TestWorkspaceLoadingIsRetryable(t *testing.T) {
	t.Parallel()
	e := WorkspaceLoading("/repo")
	cerr, err := e.connectError()
	require.NoError(t, err)
	assert.Equal(t, connect.CodeUnavailable, cerr.Code())
	got := unpack(t, e)
	assertProto(t, &errdetails.RetryInfo{RetryDelay: durationpb.New(loadingRetry)}, got[1])
}

// The JSON rendering is readable without decoding base64: that is why plain routes use it.
func TestWriteJSONRendersAIP193(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	FormatJSON.Write(rr, httptest.NewRequest(http.MethodGet, "/", nil), WorkspaceLoading("/repo"))

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", rr.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", rr.Header().Get("X-Content-Type-Options"))
	var body struct {
		Error struct {
			Code    int    `json:"code"`
			Status  string `json:"status"`
			Details []struct {
				Type       string `json:"@type"`
				Reason     string `json:"reason"`
				RetryDelay string `json:"retryDelay"`
			} `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, 503, body.Error.Code)
	assert.Equal(t, "UNAVAILABLE", body.Error.Status)
	require.Len(t, body.Error.Details, 4)
	assert.Equal(t, "type.googleapis.com/google.rpc.ErrorInfo", body.Error.Details[0].Type)
	assert.Equal(t, "MGS3017", body.Error.Details[0].Reason)
	assert.Equal(t, "2s", body.Error.Details[1].RetryDelay)
}

// A 405 has no google.rpc code, so the JSON body and status carry the override while the
// canonical status name stays the code's.
func TestWriteJSONHonorsTheHTTPStatusOverride(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	FormatJSON.Write(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/insight", nil), Error{
		Code: connect.CodeUnimplemented, Reason: types.MethodNotAllowed, Message: "use GET",
		HTTPStatus: http.StatusMethodNotAllowed,
	})

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	var body struct {
		Error struct {
			Code   int    `json:"code"`
			Status string `json:"status"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, http.StatusMethodNotAllowed, body.Error.Code)
	assert.Equal(t, "UNIMPLEMENTED", body.Error.Status)
}

// Each constructor pairs one reason with the google.rpc code whose HTTP mapping the /api/
// routes have always answered, so moving a route onto it changes no status.
func TestConstructorsKeepTheLegacyStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		err        Error
		wantReason types.DiagnosticCode
		wantStatus int
		wantName   string
	}{
		{"Invalid", Invalid("bad"), types.RequestInvalid, http.StatusBadRequest, "INVALID_ARGUMENT"},
		{"NotFound", NotFound("gone"), types.ResourceNotFound, http.StatusNotFound, "NOT_FOUND"},
		{"Conflict", Conflict("stale"), types.StateConflict, http.StatusConflict, "ABORTED"},
		{"WorkspaceNotWired", WorkspaceNotWired(), types.WorkspaceNotWired, http.StatusServiceUnavailable, "UNAVAILABLE"},
		{"Internal", Internal("plan"), types.InternalFailure, http.StatusInternalServerError, "INTERNAL"},
		{"StreamingUnsupported", StreamingUnsupported(), types.StreamingUnsupported, http.StatusInternalServerError, "INTERNAL"},
		{"ReviewHostFailed", ReviewHostFailed("publish: no token"), types.ReviewHostFailed, http.StatusBadGateway, "UNAVAILABLE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rr := httptest.NewRecorder()
			FormatJSON.Write(rr, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil), tc.err)

			var body struct {
				Error struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
					Status  string `json:"status"`
					Details []struct {
						Type   string `json:"@type"`
						Reason string `json:"reason"`
					} `json:"details"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
			assert.Equal(t, tc.wantStatus, rr.Code)
			assert.Equal(t, tc.wantStatus, body.Error.Code)
			assert.Equal(t, tc.wantName, body.Error.Status)
			assert.Equal(t, types.FormatDiagnostic(tc.wantReason, tc.err.Message), body.Error.Message)
			require.NotEmpty(t, body.Error.Details)
			assert.Equal(t, "type.googleapis.com/google.rpc.ErrorInfo", body.Error.Details[0].Type)
			assert.Equal(t, string(tc.wantReason), body.Error.Details[0].Reason)
		})
	}
}

// An internal failure names the step and never the cause, which can carry server paths.
func TestInternalNamesOnlyTheStep(t *testing.T) {
	t.Parallel()
	assert.Equal(t, Error{
		Code:    connect.CodeInternal,
		Reason:  types.InternalFailure,
		Message: "target graph failed",
	}, Internal("target graph"))
}
