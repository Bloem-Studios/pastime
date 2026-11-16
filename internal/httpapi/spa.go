package httpapi

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/Bloem-Studios/pastime/internal/fault"
	"github.com/Bloem-Studios/pastime/internal/webassets"
)

// errUnknownRoute answers an API path Pastime does not serve. It has its own code so a
// client can tell a wrong address or version from a record that was not found: the Silo
// plugin treats not_found on an event as a definite refusal of that event.
var errUnknownRoute = &fault.Error{Status: 404, Code: "unknown_route", Message: "This API route does not exist."}

// webRoutes serves the embedded application build. Hashed bundles are immutable; every
// other client route receives index.html so deep links such as /join#token resolve.
func (s *Server) webRoutes() error {
	dist, err := fs.Sub(webassets.Files, "dist")
	if err != nil {
		return err
	}
	handlers := map[string]http.HandlerFunc{}
	var index http.HandlerFunc
	err = fs.WalkDir(dist, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(path.Base(name), ".") {
			return err
		}
		data, err := fs.ReadFile(dist, name)
		if err != nil {
			return err
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if path.Ext(name) == ".webmanifest" {
			contentType = "application/manifest+json"
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		policy := "public, max-age=0, must-revalidate"
		if strings.HasPrefix(name, "assets/") {
			policy = "public, max-age=31536000, immutable"
		}
		if name == "sw.js" {
			// Browsers revalidate the worker script itself on every navigation.
			policy = "no-cache"
		}
		if name == "index.html" {
			index = staticWith(data, "text/html; charset=utf-8", "no-cache")
			return nil
		}
		handlers["/"+name] = staticWith(data, contentType, policy)
		return nil
	})
	if err != nil {
		return err
	}
	s.Router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			s.problem(w, r, errUnknownRoute)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if h, ok := handlers[r.URL.Path]; ok {
			h(w, r)
			return
		}
		if index == nil || strings.HasPrefix(r.URL.Path, "/assets/") || path.Ext(r.URL.Path) != "" {
			http.NotFound(w, r)
			return
		}
		index(w, r)
	})
	return nil
}
