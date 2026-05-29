//go:build !embedwebui

package webui

import (
	"net/http"
)

// Handler returns a clear fallback for Go tooling and test builds that do not
// embed the React production assets.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "embedded Web UI assets are not built into this binary; rebuild with `make build` or the embedwebui tag", http.StatusServiceUnavailable)
	})
}
