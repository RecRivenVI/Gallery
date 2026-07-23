// Package webapp 将阶段 6 的生产 Web/PWA 产物随 galleryd 一起发行。
package webapp

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

type Manifest struct {
	WebVersion      string `json:"webVersion"`
	ContractVersion string `json:"contractVersion"`
	APIVersion      string `json:"apiVersion"`
}

type Handler struct {
	assets  fs.FS
	ready   bool
	detail  string
	version string
}

func New(expectedContractVersion, expectedAPIVersion string) *Handler {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		return &Handler{detail: "WEB_ASSETS_UNAVAILABLE"}
	}
	manifestBytes, err := fs.ReadFile(dist, "gallery-web.json")
	if err != nil {
		return &Handler{assets: dist, detail: "WEB_ASSETS_UNAVAILABLE"}
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return &Handler{assets: dist, detail: "WEB_ASSETS_INVALID"}
	}
	if manifest.ContractVersion != expectedContractVersion || manifest.APIVersion != expectedAPIVersion {
		return &Handler{assets: dist, detail: "WEB_VERSION_MISMATCH", version: manifest.WebVersion}
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		return &Handler{assets: dist, detail: "WEB_ASSETS_UNAVAILABLE", version: manifest.WebVersion}
	}
	return &Handler{assets: dist, ready: true, version: manifest.WebVersion}
}

func (h *Handler) Version() string { return h.version }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header())
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if reservedPath(r.URL.Path) {
		writeWebError(w, http.StatusNotFound, "NOT_FOUND")
		return
	}
	if !h.ready {
		writeWebError(w, http.StatusServiceUnavailable, h.detail)
		return
	}

	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	if info, err := fs.Stat(h.assets, name); err != nil || info.IsDir() {
		name = "index.html"
	}
	contents, err := fs.ReadFile(h.assets, name)
	if err != nil {
		writeWebError(w, http.StatusServiceUnavailable, "WEB_ASSETS_UNAVAILABLE")
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	setCachePolicy(w.Header(), name)
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(contents)))
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(contents)
}

func reservedPath(requestPath string) bool {
	return requestPath == "/api" || requestPath == "/ws" ||
		strings.HasPrefix(requestPath, "/api/") || strings.HasPrefix(requestPath, "/ws/")
}

func setCachePolicy(header http.Header, name string) {
	if strings.HasPrefix(name, "assets/") {
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	header.Set("Cache-Control", "no-cache")
	if name == "sw.js" {
		header.Set("Service-Worker-Allowed", "/")
	}
}

func setSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob: data:; media-src 'self' blob:; connect-src 'self' ws: wss:; worker-src 'self'; manifest-src 'self'")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
}

func writeWebError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "retryable": status >= 500, "correlationId": "web-assets"},
	})
}
