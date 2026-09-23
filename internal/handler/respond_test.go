package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A wrong method on a JSON read route is the structured 405 every /api/ refusal answers
// with, naming the allowed methods; a preflight still gets its bare 204.
func TestAllowGetRefusesInTheAIPShape(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	assert.False(t, AllowGet(rr, httptest.NewRequest(http.MethodDelete, "/api/v1/insight", nil)))
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	assert.Equal(t, http.MethodGet, rr.Header().Get("Allow"))
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.Contains(t, rr.Body.String(), `"reason":"MGS9012"`)
	assert.Contains(t, rr.Body.String(), "DELETE /api/v1/insight is not served; use GET")

	rr = httptest.NewRecorder()
	assert.False(t, AllowGet(rr, httptest.NewRequest(http.MethodOptions, "/api/v1/insight", nil)))
	assert.Equal(t, http.StatusNoContent, rr.Code)

	assert.True(t, AllowGet(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/insight", nil)))
}
