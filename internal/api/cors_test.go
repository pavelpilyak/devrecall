package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func corsHandler() http.Handler {
	return corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"secret":"work history"}`))
	}))
}

// The API is unauthenticated and serves the user's whole work history, so the
// only thing standing between a random web page and that data is the browser
// refusing to expose the response body. Echoing an origin back is what removes
// that protection — so a foreign origin must never get the header.
func TestCORS_ForeignOriginGetsNoAllowHeader(t *testing.T) {
	for _, origin := range []string{
		"https://evil.example.com",
		"http://evil.example.com",
		// Substring games: these are NOT loopback despite containing "localhost".
		"https://localhost.evil.com",
		"https://notlocalhost",
		"http://127.0.0.1.evil.com",
	} {
		t.Run(origin, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/search?q=x", nil)
			req.Header.Set("Origin", origin)
			corsHandler().ServeHTTP(rr, req)

			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("Access-Control-Allow-Origin = %q for %s; a page at that origin could read the response", got, origin)
			}
		})
	}
}

func TestCORS_AllowsDesktopAppAndLoopback(t *testing.T) {
	for _, origin := range []string{
		"tauri://localhost",      // packaged app, macOS
		"http://tauri.localhost", // packaged app, Windows
		"http://localhost:5173",  // vite dev server
		"http://127.0.0.1:3725",
		"http://localhost:1420",
	} {
		t.Run(origin, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/search?q=x", nil)
			req.Header.Set("Origin", origin)
			corsHandler().ServeHTTP(rr, req)

			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != origin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, origin)
			}
		})
	}
}

// curl, the MCP server and scripts send no Origin. CORS never applied to them,
// and tightening it must not look like it did.
func TestCORS_NonBrowserClientsUnaffected(t *testing.T) {
	rr := httptest.NewRecorder()
	corsHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/search?q=x", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "work history") {
		t.Error("body was withheld from a client that never sent an Origin")
	}
}

// The response depends on the request's Origin, so a shared cache must not
// serve one origin's response to another.
func TestCORS_VariesOnOrigin(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/search?q=x", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	corsHandler().ServeHTTP(rr, req)

	if !strings.Contains(rr.Header().Get("Vary"), "Origin") {
		t.Errorf("Vary = %q, want it to include Origin", rr.Header().Get("Vary"))
	}
}

func TestCORS_PreflightFromForeignOriginIsNotApproved(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/search", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	corsHandler().ServeHTTP(rr, req)

	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("preflight approved a foreign origin: %q", got)
	}
}
