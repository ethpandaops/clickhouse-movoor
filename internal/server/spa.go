package server

import (
	"io"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves static assets from the embedded filesystem, falling back to
// index.html for unknown paths so client-side routing can take over.
func (s *server) spaHandler() http.Handler {
	fileServer := http.FileServerFS(s.webFS)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
			if f, err := s.webFS.Open(name); err == nil {
				_ = f.Close()
				fileServer.ServeHTTP(w, r)

				return
			}
		}

		s.serveIndex(w, r)
	})
}

// serveIndex writes the SPA entrypoint, or a 404 when the web UI has not been
// built into the binary (see web/embed_stub.go).
func (s *server) serveIndex(w http.ResponseWriter, r *http.Request) {
	index, err := s.webFS.Open("index.html")
	if err != nil {
		http.Error(w, "web UI not built", http.StatusNotFound)

		return
	}
	defer func() { _ = index.Close() }()

	stat, err := index.Stat()
	if err != nil {
		http.Error(w, "web UI unavailable", http.StatusInternalServerError)

		return
	}

	seeker, ok := index.(io.ReadSeeker)
	if !ok {
		http.Error(w, "web UI unavailable", http.StatusInternalServerError)

		return
	}

	http.ServeContent(w, r, "index.html", stat.ModTime(), seeker)
}
