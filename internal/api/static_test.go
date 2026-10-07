package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestRootServesEmbeddedIndex(t *testing.T) {
	s, e := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<main>HERDR</main>")}}, fakeProvider{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 200 || rr.Body.String() != "<main>HERDR</main>" {
		t.Fatalf("root %d %q", rr.Code, rr.Body.String())
	}
}

func TestServiceWorkerAssetHasUpdateHeadersAndNeverFallsBackToHTML(t *testing.T) {
	const script = "self.addEventListener('activate', () => {});"
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<main>HERDR</main>")},
		"sw.js":      &fstest.MapFile{Data: []byte(script)},
	}
	s, e := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, assets, fakeProvider{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, method := range []string{"GET", "HEAD"} {
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, httptest.NewRequest(method, "/sw.js", nil))
		if rr.Code != 200 || rr.Header().Get("Content-Type") != "application/javascript; charset=utf-8" || rr.Header().Get("Service-Worker-Allowed") != "/" {
			t.Fatalf("%s worker headers/status: %d %v", method, rr.Code, rr.Header())
		}
		cache := rr.Header().Get("Cache-Control")
		for _, directive := range []string{"no-store", "no-cache", "max-age=0"} {
			if !strings.Contains(cache, directive) {
				t.Fatalf("%s worker cache header lacks %s: %q", method, directive, cache)
			}
		}
		if method == "GET" && rr.Body.String() != script || method == "HEAD" && rr.Body.Len() != 0 {
			t.Fatalf("%s worker body: %q", method, rr.Body.String())
		}
	}
	missing, e := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<main>HERDR</main>")}}, fakeProvider{})
	if e != nil {
		t.Fatal(e)
	}
	defer missing.Close()
	rr := httptest.NewRecorder()
	missing.ServeHTTP(rr, httptest.NewRequest("GET", "/sw.js", nil))
	if rr.Code != 404 || strings.Contains(rr.Body.String(), "<main>") || !strings.Contains(rr.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("missing worker response: %d %q %q", rr.Code, rr.Body.String(), rr.Header().Get("Cache-Control"))
	}
}
func TestAPIRootMissIsJSON(t *testing.T) {
	s, e := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<main>HERDR</main>")}}, fakeProvider{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest("GET", "/api/v1", nil))
	if rr.Code != 404 || rr.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("API root %d %q", rr.Code, rr.Body.String())
	}
}
