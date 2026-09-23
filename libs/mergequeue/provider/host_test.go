package provider

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHostRequestSendsHeadersAndReturnsStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, r.Method+" "+r.Header.Get("Authorization")+" "+string(body))
	}))
	t.Cleanup(srv.Close)
	src := strings.NewReplacer(
		`import "std";`, `import "std";
import "mergequeue";`,
		`export fun retarget(io: {str: any}) > bool {
    return io["base"] == "main";`, `export fun retarget(io: {str: any}) > bool !> any {
    final res = mergequeue\request("POST", url: "`+srv.URL+`", body: "hi", headers: {"Authorization": "Bearer t"});
    return res["status"] == 201 and res["body"] == "POST Bearer t hi";`,
	).Replace(script)
	require.NoError(t, open(t, src).Retarget(context.Background(), change, "main"))
}

// A body past the bound would read as a shorter answer: fewer pull requests, fewer
// reviews. Before, it was cut there silently.
func TestHostRequestRefusesAResponseLargerThanItsBound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), maxBody+1))
	}))
	t.Cleanup(srv.Close)
	src := strings.NewReplacer(
		`import "std";`, `import "std";
import "mergequeue";`,
		`export fun retarget(io: {str: any}) > bool {
    return io["base"] == "main";`, `export fun retarget(io: {str: any}) > bool !> any {
    final res = mergequeue\request("GET", url: "`+srv.URL+`");
    return res["status"] == 200;`,
	).Replace(script)
	require.ErrorContains(t, open(t, src).Retarget(context.Background(), change, "main"), "response larger than 33554432 bytes")
}
