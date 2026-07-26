package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func setup(t *testing.T) (*App, http.Handler) {
	t.Helper()
	t.Setenv("ADMIN_USERNAME", "admin")
	t.Setenv("ADMIN_PASSWORD", "correct-horse-battery")
	a, e := Open(filepath.Join(t.TempDir(), "test.db"), nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.DB.Close() })
	return a, a.Router()
}
func req(h http.Handler, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	var b bytes.Buffer
	if body != nil {
		json.NewEncoder(&b).Encode(body)
	}
	r := httptest.NewRequest(method, path, &b)
	r.Host = "example.test"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func login(t *testing.T, h http.Handler) *http.Cookie {
	w := req(h, "POST", "/api/auth/login", map[string]string{"username": "admin", "password": "correct-horse-battery"}, nil)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}
func TestAuthentication(t *testing.T) {
	_, h := setup(t)
	if w := req(h, "POST", "/api/auth/login", map[string]string{"username": "admin", "password": "wrong"}, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := req(h, "GET", "/api/admin/posts", nil, nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	c := login(t, h)
	if w := req(h, "GET", "/api/auth/me", nil, c); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := req(h, "POST", "/api/auth/logout", nil, c); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := req(h, "GET", "/api/auth/me", nil, c); w.Code != 401 {
		t.Fatal("session should be invalid")
	}
}
func TestPostLifecycleAndDraftPrivacy(t *testing.T) {
	_, h := setup(t)
	c := login(t, h)
	p := map[string]any{"title": "Secret", "slug": "secret-draft", "summary": "s", "contentMarkdown": "hidden phrase", "status": "draft", "category": "技术", "tags": []string{"Go"}}
	w := req(h, "POST", "/api/admin/posts", p, c)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var made Post
	json.Unmarshal(w.Body.Bytes(), &made)
	if req(h, "GET", "/api/posts/secret-draft", nil, nil).Code != 404 {
		t.Fatal("draft leaked")
	}
	if strings.Contains(req(h, "GET", "/api/search?q=hidden", nil, nil).Body.String(), "Secret") {
		t.Fatal("search leaked")
	}
	p["status"] = "published"
	w = req(h, "PUT", "/api/admin/posts/"+strconv.FormatInt(made.ID, 10), p, c)
	if w.Code != 200 {
		t.Fatalf("update %d %s", w.Code, w.Body.String())
	}
	if req(h, "GET", "/api/posts/secret-draft", nil, nil).Code != 200 {
		t.Fatal("published missing")
	}
	if req(h, "DELETE", "/api/admin/posts/"+strconv.FormatInt(made.ID, 10), nil, c).Code != 204 {
		t.Fatal("delete failed")
	}
	if req(h, "GET", "/api/posts/secret-draft", nil, nil).Code != 404 {
		t.Fatal("delete visible")
	}
}
func TestValidationAndSlugConflict(t *testing.T) {
	_, h := setup(t)
	c := login(t, h)
	bad := map[string]any{"title": "", "slug": "BAD slug", "contentMarkdown": "", "status": "oops", "category": ""}
	if w := req(h, "POST", "/api/admin/posts", bad, c); w.Code != 422 {
		t.Fatalf("validation %d", w.Code)
	}
	p := map[string]any{"title": "Duplicate", "slug": "reliable-service-boundaries", "contentMarkdown": "x", "status": "draft", "category": "技术"}
	if w := req(h, "POST", "/api/admin/posts", p, c); w.Code != 409 {
		t.Fatalf("conflict %d", w.Code)
	}
}
func TestFiltersSearchArchive(t *testing.T) {
	_, h := setup(t)
	for _, u := range []string{"/api/posts?category=技术", "/api/search?q=可靠", "/api/archive"} {
		w := req(h, "GET", u, nil, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "reliable-service-boundaries") {
			t.Fatalf("%s: %d %s", u, w.Code, w.Body.String())
		}
	}
}
func TestMain(m *testing.M) { os.Exit(m.Run()) }
