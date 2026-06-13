package governanceui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var assets embed.FS

func Handler() http.Handler {
	staticFiles, err := fs.Sub(assets, "static")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "governance ui unavailable", http.StatusInternalServerError)
		})
	}
	fileServer := http.FileServer(http.FS(staticFiles))
	// Embedded assets carry no modtime, so browsers cache them heuristically
	// and operators see stale UI after upgrades. Force revalidation.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
}

func Redirect() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusFound)
	})
}
