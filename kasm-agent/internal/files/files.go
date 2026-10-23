// Package files serves the session user's home over HTTP for the portal's
// Files section: list a directory, download a file, upload files. Nothing
// else — no delete, no rename, no move.
//
// Auth is the Basic pair wwt injects for KasmVNC (the password is the
// kasmweb VNC_PW); wwt has already validated the connection JWT and the
// policy grant before any request gets here. Paths are confined to the
// home with os.Root, so "..", absolute paths and escaping symlinks are
// refused whatever the client sends.
package files

import (
	"cmp"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/xorhub/waas/shared/kasmagent"
)

// Handler serves the file routes of kasmagent under the given root.
type Handler struct {
	root      *os.Root
	password  string
	maxUpload int64
	mux       *http.ServeMux
}

// Entry is one directory listing row.
type Entry struct {
	Name  string    `json:"name"`
	Dir   bool      `json:"dir"`
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
}

func NewHandler(root *os.Root, password string, maxUpload int64) *Handler {
	h := &Handler{root: root, password: password, maxUpload: maxUpload, mux: http.NewServeMux()}
	// Exact paths: the mux answers 405 on a wrong method, 404 elsewhere.
	h.mux.HandleFunc("GET "+kasmagent.PathList, h.list)
	h.mux.HandleFunc("GET "+kasmagent.PathDownload, h.download)
	h.mux.HandleFunc("POST "+kasmagent.PathUpload, h.upload)
	return h
}

// ServeHTTP authenticates, then routes. No WWW-Authenticate challenge on
// 401: the response is relayed by wwt to a browser fetch, and a Basic
// challenge there would pop the browser's own login dialog.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, pw, ok := r.BasicAuth()
	if !ok || subtle.ConstantTimeCompare([]byte(pw), []byte(h.password)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	h.mux.ServeHTTP(w, r)
}

var errBadPath = errors.New("path must be relative to the home directory")

// relPath turns a query path into one os.Root accepts: relative, cleaned,
// not climbing out. os.Root is the actual guarantee (symlinks included);
// this only turns the obvious cases into a clear 400 before it.
func relPath(q string) (string, error) {
	if q == "" {
		return ".", nil
	}
	if strings.HasPrefix(q, "/") || strings.ContainsRune(q, 0) {
		return "", errBadPath
	}
	clean := path.Clean(q)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errBadPath
	}
	return clean, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	rel, err := relPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// fs.ReadDir (unlike File.ReadDir) sorts by name; root.FS() keeps
	// the confinement.
	entries, err := fs.ReadDir(h.root.FS(), rel)
	if err != nil {
		h.fsError(w, "listing", rel, err)
		return
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue // removed between readdir and stat
		}
		out = append(out, Entry{Name: e.Name(), Dir: e.IsDir(), Size: info.Size(), MTime: info.ModTime().UTC()})
	}
	// Already sorted by name; a stable sort puts directories first.
	slices.SortStableFunc(out, func(a, b Entry) int { return cmp.Compare(btoi(b.Dir), btoi(a.Dir)) })
	writeJSON(w, out)
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	rel, err := relPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// O_NONBLOCK: opening a FIFO the occupant left in their home would
	// otherwise block until a writer shows up, hanging the handler; the
	// regular-file check below then refuses it (and devices, sockets…).
	f, err := h.root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		h.fsError(w, "downloading", rel, err)
		return
	}
	defer f.Close() //nolint:errcheck // read-only handle
	info, err := f.Stat()
	if err != nil {
		h.fsError(w, "downloading", rel, err)
		return
	}
	if !info.Mode().IsRegular() {
		http.Error(w, "not a regular file", http.StatusBadRequest)
		return
	}
	// FormatMediaType quotes the name and switches to filename* for
	// non-ASCII, so a crafted name cannot break the header. The content is
	// an inert attachment whatever the extension says: never sniffed,
	// never rendered, never scripted if a browser opens it anyway.
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = kasmagent.DefaultUploadDir
	}
	rel, err := relPath(dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The cap is on the whole request body: multipart boundaries and part
	// headers ride along — a few hundred bytes against a 1 GiB default.
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "expected a multipart/form-data body", http.StatusBadRequest)
		return
	}
	if err := h.root.MkdirAll(rel, 0o755); err != nil {
		h.fsError(w, "creating upload directory", rel, err)
		return
	}
	// Parts are streamed straight to disk — no spooling — so a large
	// upload never costs memory or a writable filesystem beyond the home.
	stored := []string{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			h.uploadError(w, err)
			return
		}
		if part.FileName() == "" {
			continue // a plain form field, not a file
		}
		name, err := h.store(rel, part)
		if err != nil {
			h.uploadError(w, err)
			return
		}
		stored = append(stored, name)
	}
	writeJSON(w, map[string][]string{"stored": stored})
}

// store writes one part into dir, never overwriting: an existing name
// gets " (1)", " (2)"… before its extension. A write that fails midway
// (cap hit, client gone) leaves no truncated file behind.
func (h *Handler) store(dir string, part *multipart.Part) (string, error) {
	// Browsers send bare names; a hostile client's path is reduced to its
	// last element and the directory-like names are refused.
	base := filepath.Base(part.FileName())
	if base == "." || base == ".." || base == string(filepath.Separator) {
		return "", fmt.Errorf("invalid file name %q", part.FileName())
	}
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		target := path.Join(dir, name)
		f, err := h.root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = io.Copy(f, part)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			_ = h.root.Remove(target)
			return "", err
		}
		return name, nil
	}
}

func (h *Handler) uploadError(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		http.Error(w, fmt.Sprintf("upload exceeds the %d byte limit", h.maxUpload), http.StatusRequestEntityTooLarge)
		return
	}
	if errors.Is(err, fs.ErrPermission) {
		http.Error(w, "permission denied", http.StatusForbidden)
		return
	}
	slog.Warn("upload failed", "error", err)
	http.Error(w, "upload failed", http.StatusBadRequest)
}

// fsError maps what os.Root refused to a status. Anything that is not
// "missing" or "permission" is about the path the client named — an
// escaping symlink, a file where a directory was expected — not about
// the server, so it reads as 400; the detail goes to the log only.
func (h *Handler) fsError(w http.ResponseWriter, op, rel string, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, fs.ErrPermission):
		http.Error(w, "permission denied", http.StatusForbidden)
	default:
		slog.Warn(op+" refused", "path", rel, "error", err)
		http.Error(w, "invalid path", http.StatusBadRequest)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
