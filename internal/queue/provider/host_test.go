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

	"github.com/egladman/magus/libs/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// retargetCalling is script with retarget replaced by body, which calls the mergequeue
// host module. It fails the test when script no longer holds the retarget it replaces,
// so a test can never pass against the unpatched script.
func retargetCalling(t *testing.T, body string) string {
	t.Helper()
	const retarget = `export fun retarget(req: QueueRetargetRequest) > Ack {
    return Ack{ ok = req.to == "main" };
}`
	if !strings.Contains(script, retarget) || !strings.Contains(script, `import "std";`) {
		t.Fatal("script no longer holds the retarget and the std import this test replaces")
	}
	return strings.NewReplacer(
		`import "std";`, "import \"std\";\nimport \"mergequeue\";",
		retarget, "export fun retarget(req: QueueRetargetRequest) > Ack !> any {\n"+body+"\n}",
	).Replace(script)
}

func TestHostRequestSendsHeadersAndReturnsStatusAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, r.Method+" "+r.Header.Get("Authorization")+" "+string(body))
	}))
	t.Cleanup(srv.Close)
	src := retargetCalling(t, `    final res = mergequeue\request("POST", url: "`+srv.URL+`", body: "hi", headers: {"Authorization": "Bearer t"});
    return Ack{ ok = res["status"] == 201 and res["body"] == "POST Bearer t hi" };`)
	require.NoError(t, open(t, src).Retarget(context.Background(), change, "main"))
}

// A body past the bound would read as a shorter answer: fewer pull requests, fewer
// reviews. Before, it was cut there silently.
func TestHostRequestRefusesAResponseLargerThanItsBound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), maxBody+1))
	}))
	t.Cleanup(srv.Close)
	src := retargetCalling(t, `    final res = mergequeue\request("GET", url: "`+srv.URL+`");
    return Ack{ ok = res["status"] == 200 };`)
	require.ErrorContains(t, open(t, src).Retarget(context.Background(), change, "main"), "response larger than 33554432 bytes")
}
