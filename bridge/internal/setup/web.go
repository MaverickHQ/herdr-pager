package setup

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// webFiles are the page's own files and the vendored ESP Web Tools (design
// §The setup page, Assets): web/index.html, its script and its style, its
// font and the pager's screens, and web/esp-web-tools/<version>/. Never under
// a folder named vendor: a module zip leaves those out, so a bridge from `go
// install` would build without them (TestTheModuleZipLeavesNothingOut).
//
//go:embed web
var webFiles embed.FS

// webTypes are the types the page's files are served with, set here rather
// than read from the Mac's own tables, which differ from Mac to Mac; with
// nosniff, a script or a style needs its own type.
var webTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".png":  "image/png",
	".svg":  "image/svg+xml",
	".ttf":  "font/ttf",
	".json": "application/json",
	".txt":  "text/plain; charset=utf-8",
	".md":   "text/plain; charset=utf-8",
}

// pageFiles serves the page's files: / is index.html, and a folder is no
// listing of its files.
func pageFiles() http.Handler {
	files, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err) // the embed's own folder, so never
	}
	server := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		ext := path.Ext(r.URL.Path)
		if r.URL.Path == "/" {
			ext = ".html"
		}
		if t, ok := webTypes[ext]; ok {
			w.Header().Set("Content-Type", t)
		}
		server.ServeHTTP(w, r)
	})
}
