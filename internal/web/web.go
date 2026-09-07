package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:dist
var assets embed.FS

func Handler() http.Handler {
	root, _ := fs.Sub(assets, "dist")
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			if _, err := fs.Stat(root, "index.html"); err != nil {
				http.Error(w, "Frontend not built. Run make frontend, then rebuild the server.", http.StatusServiceUnavailable)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}
