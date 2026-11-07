package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveInRoot verifies the file-manager cannot escape the export root.
func TestResolveInRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "03-media"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		rel  string
		ok   bool
		want string
	}{
		{"03-media", true, filepath.Join(root, "03-media")},
		{"03-media/../05-output", true, filepath.Join(root, "05-output")},
		{"", true, filepath.Clean(root)},
		{"../outside", false, ""},
		{"..", false, ""},
		// Absolute paths must be coerced inside the root, never served from /.
		{"/etc/passwd", true, filepath.Join(root, "etc", "passwd")},
	}
	for _, tc := range cases {
		got, ok := resolveInRoot(root, tc.rel)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Fatalf("resolveInRoot(%q) = %q, %v; want %q, %v", tc.rel, got, ok, tc.want, tc.ok)
		}
	}
}

// TestServeEndpoints checks the SPA page, status JSON, and traversal rejection
// over a real HTTP mux against a small fixture dataset.
func TestServeEndpoints(t *testing.T) {
	l := newLayout(t.TempDir())
	if err := ensureDirs(l); err != nil {
		t.Fatal(err)
	}
	ds := &Dataset{Version: datasetVersion, AccountName: "t",
		Saved: []SavedPost{{URL: "https://www.instagram.com/p/abc/", Status: "pending"}}}
	if err := ds.save(l.Normalized); err != nil {
		t.Fatal(err)
	}
	s, err := newServer(l)
	if err != nil {
		t.Fatal(err)
	}

	// Page.
	rec := httptest.NewRecorder()
	s.handlePage(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "digital-exit") {
		t.Fatalf("page: %d %s", rec.Code, rec.Body.String())
	}

	// Status.
	rec = httptest.NewRecorder()
	s.handleStatus(rec, httptest.NewRequest("GET", "/api/status", nil))
	var st map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st["savedTotal"].(float64) != 1 {
		t.Fatalf("status: %v", st)
	}

	// Traversal attempt on the file endpoint.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/file?path="+url.QueryEscape("../etc/passwd"), nil)
	s.handleFile(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("traversal not blocked: %d", rec.Code)
	}

	// Category persistence.
	body := strings.NewReader(`{"username":"u","category":"Friend"}`)
	ds2 := &Dataset{Version: datasetVersion,
		Contacts: []Contact{{Username: "u", Kind: KindFollower}}, Saved: ds.Saved}
	s.ds = ds2
	rec = httptest.NewRecorder()
	s.handleCategory(rec, httptest.NewRequest("POST", "/api/contact/category", body))
	if rec.Code != 200 {
		t.Fatalf("category: %d %s", rec.Code, rec.Body.String())
	}
	if ds2.Contacts[0].Category != CatFriend {
		t.Fatalf("category not applied: %+v", ds2.Contacts[0])
	}
}
