package docs

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static api
var content embed.FS

func Handler() http.Handler {
	staticFS, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}

	fileServer := http.FileServer(http.FS(staticFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.yaml":
			serveFile(w, r, "api/openapi.yaml", "application/yaml; charset=utf-8")
		case "/asyncapi.yaml":
			serveFile(w, r, "api/asyncapi.yaml", "application/yaml; charset=utf-8")
		case "/openapi":
			serveStatic(w, r, fileServer, "/openapi.html")
		case "/asyncapi":
			serveStatic(w, r, fileServer, "/asyncapi.html")
		default:
			http.NotFound(w, r)
		}
	})
}

func serveFile(w http.ResponseWriter, r *http.Request, name string, contentType string) {
	data, err := content.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func serveStatic(w http.ResponseWriter, r *http.Request, fileServer http.Handler, path string) {
	cloned := r.Clone(r.Context())
	cloned.URL.Path = path
	fileServer.ServeHTTP(w, cloned)
}
