package artifacthttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"harness-forge.local/control-plane/internal/artifacts"
	"harness-forge.local/control-plane/internal/objectstore"
)

type artifactReader interface {
	Read(context.Context, uuid.UUID) (artifacts.Artifact, error)
}

// NewServer is deliberately separate from the control-plane router: no API
// routes, authentication cookies or CORS middleware are installed here.
func NewServer(metadata artifactReader, objects objectstore.Store, webOrigin string) http.Handler {
	if webOrigin == "" {
		webOrigin = "'none'"
	}
	csp := "default-src 'self' data: blob:; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'none'; frame-ancestors " + webOrigin
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// Parse directly, without ServeMux's path-cleaning redirects. EscapedPath
		// retains encoded separators; each component is decoded exactly once.
		raw, ok := strings.CutPrefix(r.URL.EscapedPath(), "/artifacts/")
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rawID, rawRelative, _ := strings.Cut(raw, "/")
		idText, err := url.PathUnescape(rawID)
		if err != nil {
			http.Error(w, "invalid artifact ID", 400)
			return
		}
		id, err := uuid.Parse(idText)
		if err != nil {
			http.Error(w, "invalid artifact ID", 400)
			return
		}
		record, err := metadata.Read(r.Context(), id)
		if errors.Is(err, artifacts.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "artifact metadata unavailable", 500)
			return
		}
		relative, err := url.PathUnescape(rawRelative)
		if err != nil {
			http.Error(w, "invalid artifact path", 400)
			return
		}
		if relative == "" {
			if !artifacts.ValidRelativePath(record.EntryPath) {
				http.Error(w, "invalid artifact entry", 400)
				return
			}
			entry := (&url.URL{Path: "/artifacts/" + id.String() + "/" + record.EntryPath}).EscapedPath()
			http.Redirect(w, r, entry, http.StatusFound)
			return
		}
		if !artifacts.ValidRelativePath(relative) {
			http.Error(w, "invalid artifact path", 400)
			return
		}
		prefix := record.ObjectPrefix
		parts := strings.Split(strings.TrimSuffix(prefix, "/"), "/")
		if len(parts) != 4 || parts[0] != "projects" || parts[2] != "artifacts" || parts[3] != id.String() || !strings.HasSuffix(prefix, "/") {
			http.Error(w, "invalid artifact prefix", 400)
			return
		}
		if _, err := uuid.Parse(parts[1]); err != nil {
			http.Error(w, "invalid artifact prefix", 400)
			return
		}
		key := path.Join(prefix, relative)
		if !strings.HasPrefix(key, prefix) {
			http.Error(w, "invalid artifact path", 400)
			return
		}
		info, err := objects.Stat(r.Context(), key)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		body, err := objects.Open(r.Context(), key)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer body.Close()
		contentType := info.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.Copy(w, body)
		}
	})
}
