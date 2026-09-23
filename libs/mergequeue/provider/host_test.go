package provider

import (
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
		`export fun kick_back(io: {str: any}) > bool {
    return io["report"] == "report";`, `export fun kick_back(io: {str: any}) > bool !> any {
    final res = mergequeue\request("POST", url: "`+srv.URL+`", body: "hi", headers: {"Authorization": "Bearer t"});
    return res["status"] == 201 and res["body"] == "POST Bearer t hi";`,
	).Replace(script)
	require.NoError(t, open(t, src).KickBack(context.Background(), change, headA, ""))
}
