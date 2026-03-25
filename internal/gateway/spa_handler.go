package gateway

import (
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
)

// SetDashboardFS configures the embedded SPA filesystem for serving
// the dashboard UI. The fsys should contain the built React app
// (index.html, assets/, etc.) at its root.
func (s *Server) SetDashboardFS(fsys fs.FS) {
	s.dashboardFS = fsys
}

// spaHandler serves a single-page application (SPA) from an embedded
// filesystem. It serves static files when they exist, and falls back
// to index.html for all other paths — this is how client-side routing
// works (React Router, etc.).
//
// Why fallback to index.html? When a user navigates to /settings/agents
// directly (not via in-app link), the server receives that path. Since
// there's no /settings/agents file on disk, we serve index.html and let
// the React Router handle the path client-side.
type spaHandler struct {
	fileServer http.Handler
	fsys       fs.FS
}

func newSPAHandler(fsys fs.FS) http.Handler {
	return &spaHandler{
		fileServer: http.FileServerFS(fsys),
		fsys:       fsys,
	}
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Clean the path — strip leading slash for fs.Open compatibility.
	// path.Clean + ".." guard prevents directory traversal attacks.
	p := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if p == "." || p == "" {
		p = "index.html"
	}
	if strings.HasPrefix(p, "..") {
		http.NotFound(w, r)
		return
	}

	// Check if the requested file exists in the embedded filesystem.
	f, err := h.fsys.Open(p)
	if err == nil {
		f.Close()
		// File exists — serve it directly (JS, CSS, images, etc.).
		h.fileServer.ServeHTTP(w, r)
		return
	}

	// File not found — serve index.html for SPA client-side routing.
	// This handles paths like /chat, /settings/agents, etc.
	r.URL.Path = "/"
	h.fileServer.ServeHTTP(w, r)
}

// registerDashboardRoutes adds the SPA catch-all handler to the mux.
// MUST be called last in BuildMux() because "/" matches everything
// that isn't matched by more specific routes.
func (s *Server) registerDashboardRoutes(mux *http.ServeMux) {
	if s.dashboardFS == nil {
		// No embedded dashboard — show a helpful message instead.
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Don't intercept API/WS paths that somehow fell through.
			if strings.HasPrefix(r.URL.Path, "/v1/") ||
				strings.HasPrefix(r.URL.Path, "/ws") ||
				strings.HasPrefix(r.URL.Path, "/health") ||
				strings.HasPrefix(r.URL.Path, "/mcp/") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<!DOCTYPE html><html><body style="font-family:system-ui;padding:2rem">
<h1>GoClaw Gateway</h1>
<p>Dashboard UI is not embedded in this binary.</p>
<p>To include it, build with: <code>make build-dist</code></p>
<p>Or run the dev server: <code>cd ui/web && pnpm dev</code></p>
</body></html>`))
		})
		slog.Info("dashboard UI not embedded, serving placeholder")
		return
	}

	mux.Handle("/", newSPAHandler(s.dashboardFS))
	slog.Info("serving embedded dashboard UI")
}
