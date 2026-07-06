package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDashboardRoutesServeExport(t *testing.T) {
	webOut := t.TempDir()
	mustWrite(t, filepath.Join(webOut, "dashboard.html"), "dashboard")
	mustWrite(t, filepath.Join(webOut, "index.html"), "index")
	mustWrite(t, filepath.Join(webOut, "favicon.ico"), "icon")
	mustWrite(t, filepath.Join(webOut, "_next", "static", "app.js"), "next")

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("api"))
	})
	registerDashboardRoutes(mux, webOut)

	assertBody(t, mux, "/dashboard", http.StatusOK, "dashboard")
	assertBody(t, mux, "/", http.StatusOK, "dashboard")
	assertBody(t, mux, "/_next/static/app.js", http.StatusOK, "next")
	assertBody(t, mux, "/favicon.ico", http.StatusOK, "icon")
	assertBody(t, mux, "/health", http.StatusOK, "api")
}

func TestDashboardRoutesMissingBuildFallback(t *testing.T) {
	mux := http.NewServeMux()
	registerDashboardRoutes(mux, "")

	assertBody(t, mux, "/dashboard", http.StatusOK, dashboardFallback)
	assertBody(t, mux, "/", http.StatusOK, dashboardFallback)
	assertBody(t, mux, "/missing", http.StatusNotFound, "404 page not found")
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertBody(t *testing.T, mux *http.ServeMux, path string, status int, want string) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != status {
		t.Fatalf("%s: status %d, want %d", path, rec.Code, status)
	}
	if body := rec.Body.String(); !strings.Contains(body, want) {
		t.Fatalf("%s: body %q does not contain %q", path, body, want)
	}
}
