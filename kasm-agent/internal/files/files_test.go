package files

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testPassword = "vnc-pw-1"

// newServer builds a home with a file, a directory and two escaping
// symlinks (a file and a directory outside the home), plus the handler
// serving it with a small upload cap.
func newServer(t *testing.T, maxUpload int64) (*httptest.Server, string) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(home, "Documents"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, content string) {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, "notes.txt"), "hello")
	write(filepath.Join(home, "Documents", "report.pdf"), "%PDF")
	write(filepath.Join(outside, "secret.txt"), "top secret")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(home, "leak")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "leakdir")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	srv := httptest.NewServer(NewHandler(root, testPassword, maxUpload))
	t.Cleanup(srv.Close)
	return srv, home
}

func do(t *testing.T, method, rawURL string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("kasm_user", testPassword)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRequiresTheInjectedPassword(t *testing.T) {
	srv, _ := newServer(t, 1<<20)
	for _, auth := range []func(*http.Request){
		func(*http.Request) {},
		func(r *http.Request) { r.SetBasicAuth("kasm_user", "wrong") },
	} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/files", nil)
		auth(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
		if resp.Header.Get("WWW-Authenticate") != "" {
			t.Fatal("a Basic challenge would pop the browser's login dialog through wwt")
		}
	}
}

func TestListsDirectoriesFirst(t *testing.T) {
	srv, _ := newServer(t, 1<<20)
	resp := do(t, http.MethodGet, srv.URL+"/files", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var entries []Entry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	// Documents is the only real directory (the symlinks are not
	// directories to Lstat); the rest follows ReadDir's name order.
	if want := "Documents,leak,leakdir,notes.txt"; strings.Join(names, ",") != want {
		t.Fatalf("expected %s, got %s", want, strings.Join(names, ","))
	}
	if !entries[0].Dir || entries[3].Dir || entries[3].Size != 5 || entries[3].MTime.IsZero() {
		t.Fatalf("unexpected entries: %+v", entries)
	}

	resp = do(t, http.MethodGet, srv.URL+"/files?path=Documents", nil, "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"report.pdf"`) {
		t.Fatalf("expected the Documents listing, got %d %s", resp.StatusCode, body)
	}
}

func TestConfinementToHome(t *testing.T) {
	srv, _ := newServer(t, 1<<20)
	cases := []struct {
		route string
		path  string
		want  int
	}{
		{"/files", "..", http.StatusBadRequest},
		{"/files", "../outside", http.StatusBadRequest},
		{"/files", "Documents/../../outside", http.StatusBadRequest},
		{"/files", "/etc", http.StatusBadRequest},
		{"/files", "leakdir", http.StatusBadRequest}, // symlink to a directory outside
		{"/files", "nope", http.StatusNotFound},
		{"/files", "notes.txt", http.StatusBadRequest}, // a file is not a listing
		{"/files/download", "../outside/secret.txt", http.StatusBadRequest},
		{"/files/download", "/etc/passwd", http.StatusBadRequest},
		{"/files/download", "leak", http.StatusBadRequest}, // symlink to a file outside
		{"/files/download", "leakdir/secret.txt", http.StatusBadRequest},
		{"/files/download", "Documents", http.StatusBadRequest}, // a directory is not a download
		{"/files/download", "", http.StatusBadRequest},
		{"/files/download", "missing.txt", http.StatusNotFound},
	}
	for _, tc := range cases {
		resp := do(t, http.MethodGet, srv.URL+tc.route+"?path="+url.QueryEscape(tc.path), nil, "")
		body := readBody(t, resp)
		if resp.StatusCode != tc.want {
			t.Errorf("%s path=%q: expected %d, got %d (%s)", tc.route, tc.path, tc.want, resp.StatusCode, strings.TrimSpace(body))
		}
		if strings.Contains(body, "top secret") {
			t.Fatalf("%s path=%q leaked a file outside the home", tc.route, tc.path)
		}
	}
	// Upload directories are confined the same way.
	resp := do(t, http.MethodPost, srv.URL+"/files/upload?dir="+url.QueryEscape("../outside"), multipartBody(t, "x.txt", "x"), multipartType)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an escaping upload dir, got %d", resp.StatusCode)
	}
	resp = do(t, http.MethodPost, srv.URL+"/files/upload?dir=leakdir", multipartBody(t, "x.txt", "x"), multipartType)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an upload through an escaping symlink, got %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(srv.URL), "x.txt")); err == nil {
		t.Fatal("nothing must land outside the home")
	}
}

func TestDownloadsAsAttachment(t *testing.T) {
	srv, _ := newServer(t, 1<<20)
	resp := do(t, http.MethodGet, srv.URL+"/files/download?path="+url.QueryEscape("Documents/report.pdf"), nil, "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || body != "%PDF" {
		t.Fatalf("expected the file content, got %d %q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename=report.pdf` {
		t.Fatalf("unexpected Content-Disposition %q", got)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/pdf") {
		t.Fatalf("expected a pdf content type from the name, got %q", got)
	}
}

const multipartType = "multipart/form-data; boundary=b0undary"

func multipartBody(t *testing.T, files ...string) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.SetBoundary("b0undary"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(files); i += 2 {
		fw, err := mw.CreateFormFile("files", files[i])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(files[i+1])); err != nil {
			t.Fatal(err)
		}
	}
	mw.Close()
	return &buf
}

func TestUploadsNeverOverwrite(t *testing.T) {
	srv, home := newServer(t, 1<<20)

	// Default directory is created; two files in one request.
	resp := do(t, http.MethodPost, srv.URL+"/files/upload", multipartBody(t, "a.txt", "one", "b", "two"), multipartType)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || body != "{\"stored\":[\"a.txt\",\"b\"]}\n" {
		t.Fatalf("expected both files stored in Uploads, got %d %s", resp.StatusCode, body)
	}
	got, err := os.ReadFile(filepath.Join(home, "Uploads", "a.txt"))
	if err != nil || string(got) != "one" {
		t.Fatalf("expected Uploads/a.txt = one, got %q %v", got, err)
	}

	// Same names again: suffixed before the extension, originals intact.
	resp = do(t, http.MethodPost, srv.URL+"/files/upload", multipartBody(t, "a.txt", "three", "b", "four"), multipartType)
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || body != "{\"stored\":[\"a (1).txt\",\"b (1)\"]}\n" {
		t.Fatalf("expected suffixed names, got %d %s", resp.StatusCode, body)
	}
	got, _ = os.ReadFile(filepath.Join(home, "Uploads", "a.txt"))
	if string(got) != "one" {
		t.Fatalf("the original must not be overwritten, got %q", got)
	}
	got, _ = os.ReadFile(filepath.Join(home, "Uploads", "a (1).txt"))
	if string(got) != "three" {
		t.Fatalf("expected a (1).txt = three, got %q", got)
	}

	// Explicit directory; a path-like file name is reduced to its base.
	resp = do(t, http.MethodPost, srv.URL+"/files/upload?dir=Documents/in", multipartBody(t, "../../evil.sh", "x"), multipartType)
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK || body != "{\"stored\":[\"evil.sh\"]}\n" {
		t.Fatalf("expected the base name stored, got %d %s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(home, "Documents", "in", "evil.sh")); err != nil {
		t.Fatalf("expected the file in the requested directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "evil.sh")); err == nil {
		t.Fatal("a path-like name must not climb")
	}

	// Method and route discipline.
	resp = do(t, http.MethodGet, srv.URL+"/files/upload", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET upload, got %d", resp.StatusCode)
	}
	resp = do(t, http.MethodDelete, srv.URL+"/files/download?path=notes.txt", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for DELETE, got %d", resp.StatusCode)
	}
	resp = do(t, http.MethodGet, srv.URL+"/files/rename", nil, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown route, got %d", resp.StatusCode)
	}
}

func TestUploadCapIs413WithoutPartialFile(t *testing.T) {
	srv, home := newServer(t, 64)
	resp := do(t, http.MethodPost, srv.URL+"/files/upload", multipartBody(t, "big.bin", strings.Repeat("x", 200)), multipartType)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(home, "Uploads", "big.bin")); err == nil {
		t.Fatal("a capped upload must not leave a truncated file")
	}
}
