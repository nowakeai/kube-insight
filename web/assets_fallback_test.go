//go:build !embedwebui

package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFallbackHandlerReportsMissingEmbeddedAssets(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(recorder.Body.String(), "embedded Web UI assets are not built") {
		t.Fatalf("fallback response did not explain missing assets: %q", recorder.Body.String())
	}
}
