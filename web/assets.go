package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the embedded React application and falls back to index.html for
// client-side routes.
func Handler() http.Handler {
	appFS, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return spaHandler{fsys: appFS}
}

type spaHandler struct {
	fsys fs.FS
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "." || name == "" {
		serveEmbeddedFile(w, r, h.fsys, "index.html")
		return
	}
	if exists, isDir := embeddedFileExists(h.fsys, name); exists && !isDir {
		serveEmbeddedFile(w, r, h.fsys, name)
		return
	}
	if strings.Contains(path.Base(name), ".") {
		http.NotFound(w, r)
		return
	}
	serveEmbeddedFile(w, r, h.fsys, "index.html")
}

func serveEmbeddedFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	http.ServeFileFS(w, r, fsys, name)
}

func embeddedFileExists(fsys fs.FS, name string) (bool, bool) {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return false, false
	}
	return true, info.IsDir()
}
