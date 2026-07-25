package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"

	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/k8s"
	"github.com/xorhub/waas/api-server/internal/repository"
)

const workerCatalogManifest = `apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: docker.io/xorhub/firefox:1.0.0@sha256:def
    os: linux
    app: firefox
    version: "1.0.0"
    icon: firefox
    displayName: Firefox
    description: "  Managed, policy-hardened Firefox in a kiosk session.  "
  - image: docker.io/xorhub/ubuntu-xfce:1.1.0@sha256:abc
    os: weirdos
    app: ubuntu-xfce
`

// catalogWorkerFixture builds a CatalogSyncWorker on a fake k8s client
// (status subresource enabled for WorkspaceImage) and a real
// SQLCatalogRepository backed by a throwaway sqlite file.
func catalogWorkerFixture(t *testing.T, objs ...client.Object) (*CatalogSyncWorker, client.WithWatch, repository.CatalogRepository) {
	t.Helper()
	c := fake.NewClientBuilder().
		WithScheme(k8s.Scheme).
		WithObjects(objs...).
		WithStatusSubresource(&waasv1alpha1.WorkspaceImage{}).
		Build()
	db, err := database.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	catalogRepo := repository.NewSQLCatalogRepository(db)
	w := NewCatalogSyncWorker(c, "default", catalogRepo, 42*time.Minute)
	return w, c, catalogRepo
}

func workerRegistryImage(name string, cat *waasv1alpha1.ImageCatalogSpec) *waasv1alpha1.WorkspaceImage {
	return &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "XorHub images",
			Registry:    "docker.io/xorhub",
			Protocols:   []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC},
			Enabled:     true,
			Catalog:     cat,
		},
	}
}

func workerURLSource(url string) *waasv1alpha1.ImageCatalogSpec {
	return &waasv1alpha1.ImageCatalogSpec{From: waasv1alpha1.ImageCatalogSource{URL: url}}
}

func workerCatalogStatus(t *testing.T, c client.Client, namespace, name string) *waasv1alpha1.ImageCatalogStatus {
	t.Helper()
	got := &waasv1alpha1.WorkspaceImage{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	return got.Status.Catalog
}

// waitForCondition is the package's bounded-wait helper: every wait
// synchronizes on an observable effect (an HTTP hit, a database row) —
// never an arbitrary sleep.
func waitForCondition(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for !cond() {
		select {
		case <-deadline:
			t.Fatal(msg)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// waitForEntries blocks until the image has n catalog entries — the
// observable end of an asynchronous sync.
func waitForEntries(t *testing.T, repo repository.CatalogRepository, name string, n int) {
	t.Helper()
	waitForCondition(t, fmt.Sprintf("%s never reached %d catalog entries", name, n), func() bool {
		entries, err := repo.ListEntries(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		return len(entries) == n
	})
}

// repointSource re-points an image's catalog source, retrying on
// conflict: the worker status-patches the same object as soon as a sync
// lands (and waitForEntries returns BEFORE that patch, since
// ReplaceEntries precedes it), so a plain read-modify-Update would flake
// on the resourceVersion check.
func repointSource(t *testing.T, c client.Client, name, url string) {
	t.Helper()
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "default", Name: name}
	waitForCondition(t, "re-pointing "+name+" kept conflicting", func() bool {
		got := &waasv1alpha1.WorkspaceImage{}
		if err := c.Get(ctx, key, got); err != nil {
			t.Fatal(err)
		}
		got.Spec.Catalog = workerURLSource(url)
		err := c.Update(ctx, got)
		if err != nil && !apierrors.IsConflict(err) {
			t.Fatal(err)
		}
		return err == nil
	})
}

// countingCatalogServer serves workerCatalogManifest on every path and
// counts hits per path, so a test can prove which sources were fetched
// and how many times.
func countingCatalogServer(t *testing.T) (*httptest.Server, func(path string) int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		_, _ = wr.Write([]byte(workerCatalogManifest))
	}))
	t.Cleanup(srv.Close)
	return srv, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return hits[path]
	}
}

// watchSyncFixture wires the fixture exactly like main.go: the shared
// image watch with the worker as observer plus the RunEventSync
// consumer — with the ticker disabled (interval 0), the configuration
// where the watch path must carry manifest creations on its own.
//
// It only returns once the watch is proven live: a fake-client watch
// delivers nothing emitted before its (goroutine-side) registration, so
// a warmup image's source is re-pointed until one of its syncs lands.
func watchSyncFixture(t *testing.T, srvURL string) (*CatalogSyncWorker, client.WithWatch, repository.CatalogRepository) {
	t.Helper()
	w, c, catalogRepo := catalogWorkerFixture(t)
	w.interval = 0
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	hub := NewEventHub()
	go hub.RunWatchWithObserver(ctx, c, &waasv1alpha1.WorkspaceImageList{}, "images", nil,
		w.OnImageEvent, client.InNamespace("default"))
	go w.RunEventSync(ctx)

	warmup := workerRegistryImage("watch-warmup", workerURLSource(srvURL+"/warmup"))
	if err := c.Create(ctx, warmup); err != nil {
		t.Fatalf("creating warmup image: %v", err)
	}
	deadline := time.After(3 * time.Second)
	for i := 0; ; i++ {
		entries, err := catalogRepo.ListEntries(ctx, "watch-warmup")
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) > 0 {
			return w, c, catalogRepo
		}
		select {
		case <-deadline:
			t.Fatal("watch never became live")
		case <-time.After(20 * time.Millisecond):
		}
		repointSource(t, c, "watch-warmup", fmt.Sprintf("%s/warmup/%d", srvURL, i))
	}
}

// TestCatalogWorkerWatchSyncsManifestCreations covers the GitOps path
// end to end: a WorkspaceImage applied through the k8s API syncs
// without any tick (the ticker is disabled), a pure status patch does
// not refetch, and repointing spec.catalog does.
func TestCatalogWorkerWatchSyncsManifestCreations(t *testing.T) {
	srv, hitCount := countingCatalogServer(t)
	_, c, catalogRepo := watchSyncFixture(t, srv.URL)
	ctx := context.Background()

	img := workerRegistryImage("manifest-img", workerURLSource(srv.URL+"/a"))
	if err := c.Create(ctx, img); err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitForEntries(t, catalogRepo, "manifest-img", 2)
	if got := hitCount("/a"); got != 1 {
		t.Fatalf("manifest fetches = %d after creation, want 1", got)
	}

	// A pure status patch (what every sync emits) must not refetch. The
	// proof is ordered, not timed: watch events are delivered in order,
	// so once the later source change has synced, the status event has
	// necessarily been processed — without a fetch.
	got := &waasv1alpha1.WorkspaceImage{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "default", Name: "manifest-img"}, got); err != nil {
		t.Fatal(err)
	}
	orig := got.DeepCopy()
	got.Status.Catalog.LastSyncError = "poked by test"
	if err := c.Status().Patch(ctx, got, client.MergeFrom(orig)); err != nil {
		t.Fatalf("status patch: %v", err)
	}
	repointSource(t, c, "manifest-img", srv.URL+"/b")
	waitForCondition(t, "source change never triggered a re-fetch", func() bool { return hitCount("/b") == 1 })
	if got := hitCount("/a"); got != 1 {
		t.Fatalf("manifest fetches = %d on the old source, want still 1 (status patch must not refetch)", got)
	}
}

// TestCatalogWorkerOnImageEventDiscriminant pins the watch handler's
// decision table without goroutines: what lands in the pending set is
// asserted directly, so re-deliveries and status noise are proven
// no-ops deterministically (regardless of fake-vs-real generation
// semantics — the discriminant is the source itself).
func TestCatalogWorkerOnImageEventDiscriminant(t *testing.T) {
	srv, _ := countingCatalogServer(t)
	img := workerRegistryImage("waas-images", workerURLSource(srv.URL))
	w, _, _ := catalogWorkerFixture(t, img)
	ctx := context.Background()

	if err := w.syncOne(ctx, img); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	// ADDED re-delivery (watch restart re-lists everything): no-op.
	w.OnImageEvent(watch.Added, img)
	if pending := w.drainPending(); len(pending) != 0 {
		t.Fatalf("ADDED re-delivery queued %v, want nothing", pending)
	}

	// Status-only MODIFIED (every sync emits one): no-op.
	statusOnly := img.DeepCopy()
	statusOnly.Status.Catalog = &waasv1alpha1.ImageCatalogStatus{LastSyncError: "x"}
	w.OnImageEvent(watch.Modified, statusOnly)
	if pending := w.drainPending(); len(pending) != 0 {
		t.Fatalf("status-only change queued %v, want nothing", pending)
	}

	// Non-WorkspaceImage objects flow through the same hub: ignored.
	w.OnImageEvent(watch.Added, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "s", Namespace: "default"}})
	if pending := w.drainPending(); len(pending) != 0 {
		t.Fatalf("foreign object queued %v, want nothing", pending)
	}

	// A moved source queues a sync.
	moved := img.DeepCopy()
	moved.Spec.Catalog = workerURLSource(srv.URL + "/v2")
	w.OnImageEvent(watch.Modified, moved)
	if pending := w.drainPending(); len(pending) != 1 || pending[0] != "waas-images" {
		t.Fatalf("source change queued %v, want [waas-images]", pending)
	}

	// Removing the source forgets it, so re-adding the SAME source
	// resyncs instead of being mistaken for already-synced.
	removed := img.DeepCopy()
	removed.Spec.Catalog = nil
	w.OnImageEvent(watch.Modified, removed)
	w.OnImageEvent(watch.Modified, img)
	if pending := w.drainPending(); len(pending) != 1 {
		t.Fatalf("re-added source queued %v, want one sync", pending)
	}

	// DELETED purges the tracking: a deleted-then-recreated image (same
	// name, same source) resyncs.
	if err := w.syncOne(ctx, img); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	w.OnImageEvent(watch.Deleted, img)
	w.OnImageEvent(watch.Added, img)
	if pending := w.drainPending(); len(pending) != 1 {
		t.Fatalf("recreated image queued %v, want one sync", pending)
	}
}

func TestCatalogSyncWorkerSuccessPopulatesTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(workerCatalogManifest))
	}))
	defer srv.Close()

	img := workerRegistryImage("waas-images", workerURLSource(srv.URL))
	w, c, catalogRepo := catalogWorkerFixture(t, img)
	ctx := context.Background()

	w.syncAll(ctx)

	entries, err := catalogRepo.ListEntries(ctx, "waas-images")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("catalog_entries = %+v, want 2 rows", entries)
	}
	// Unknown os values degrade to empty (enum-safe), not fail the sync.
	for _, e := range entries {
		if e.Image == "docker.io/xorhub/ubuntu-xfce:1.1.0@sha256:abc" && e.OS != "" {
			t.Errorf("unknown os should normalize to empty, got %q", e.OS)
		}
		// The description survives full-length (no 80-char shortening),
		// only trimmed.
		if e.Image == "docker.io/xorhub/firefox:1.0.0@sha256:def" && e.Description != "Managed, policy-hardened Firefox in a kiosk session." {
			t.Errorf("description should round-trip trimmed, got %q", e.Description)
		}
	}

	st := workerCatalogStatus(t, c, "default", "waas-images")
	if st == nil || st.Source != catalogSourceFetched || st.LastSyncTime == nil || st.LastSyncError != "" {
		t.Fatalf("sync bookkeeping wrong: %+v", st)
	}
}

const workerRecommendationManifest = `apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: docker.io/xorhub/ubuntu-xfce:1.1.0@sha256:abc
    os: linux
    app: ubuntu-xfce
    profile: hardened
    recommended:
      podSecurityContext:
        runAsUser: 1000
      securityContext:
        readOnlyRootFilesystem: true
      env:
        - name: WAAS_SSH_ENABLED
          protocols: [ssh]
          requires: [WAAS_SSH_AUTHORIZED_KEYS_FILE]
`

func TestCatalogSyncWorkerCopiesRecommendation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(workerRecommendationManifest))
	}))
	defer srv.Close()

	img := workerRegistryImage("waas-images", workerURLSource(srv.URL))
	w, _, catalogRepo := catalogWorkerFixture(t, img)
	ctx := context.Background()

	w.syncAll(ctx)

	entries, err := catalogRepo.ListEntries(ctx, "waas-images")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("catalog_entries = %+v, want 1 row", entries)
	}
	e := entries[0]
	if e.Profile != "hardened" {
		t.Errorf("profile = %q, want hardened", e.Profile)
	}
	if e.Recommended == nil {
		t.Fatal("recommended = nil, want populated")
	}
	var got struct {
		PodSecurityContext struct {
			RunAsUser int64 `json:"runAsUser"`
		} `json:"podSecurityContext"`
		SecurityContext struct {
			ReadOnlyRootFilesystem bool `json:"readOnlyRootFilesystem"`
		} `json:"securityContext"`
		Env []struct {
			Name      string   `json:"name"`
			Protocols []string `json:"protocols"`
			Requires  []string `json:"requires"`
		} `json:"env"`
	}
	if err := json.Unmarshal(e.Recommended, &got); err != nil {
		t.Fatalf("recommended did not land as valid JSON: %v (%s)", err, e.Recommended)
	}
	if got.PodSecurityContext.RunAsUser != 1000 || !got.SecurityContext.ReadOnlyRootFilesystem {
		t.Errorf("recommended mismatch: %+v", got)
	}
	if len(got.Env) != 1 || got.Env[0].Name != "WAAS_SSH_ENABLED" || len(got.Env[0].Requires) != 1 || got.Env[0].Requires[0] != "WAAS_SSH_AUTHORIZED_KEYS_FILE" {
		t.Errorf("env hint mismatch: %+v", got.Env)
	}
}

const workerProfileManifest = `apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: docker.io/xorhub/firefox:1.0.0@sha256:def
    os: linux
    app: firefox
    profile: hardened
  - image: docker.io/xorhub/chrome:1.0.0@sha256:ghi
    os: linux
    app: chrome
    profile: normal
  - image: docker.io/xorhub/ubuntu-xfce:1.1.0@sha256:abc
    os: linux
    app: ubuntu-xfce
    profile: banana
  - image: docker.io/xorhub/ubuntu-mate:1.1.0@sha256:jkl
    os: linux
    app: ubuntu-mate
    profile: Hardened
`

func TestCatalogSyncWorkerNormalizesUnknownProfile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(workerProfileManifest))
	}))
	defer srv.Close()

	img := workerRegistryImage("waas-images", workerURLSource(srv.URL))
	w, _, catalogRepo := catalogWorkerFixture(t, img)
	ctx := context.Background()

	w.syncAll(ctx)

	entries, err := catalogRepo.ListEntries(ctx, "waas-images")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("catalog_entries = %+v, want 4 rows", entries)
	}
	want := map[string]string{
		"docker.io/xorhub/firefox:1.0.0@sha256:def":     "hardened",
		"docker.io/xorhub/chrome:1.0.0@sha256:ghi":      "normal",
		"docker.io/xorhub/ubuntu-xfce:1.1.0@sha256:abc": "", // unknown value
		"docker.io/xorhub/ubuntu-mate:1.1.0@sha256:jkl": "", // wrong case
	}
	for _, e := range entries {
		got, ok := want[e.Image]
		if !ok {
			t.Errorf("unexpected catalog entry %s", e.Image)
			continue
		}
		if e.Profile != got {
			t.Errorf("%s: profile = %q, want %q", e.Image, e.Profile, got)
		}
	}
}

const workerArchManifest = `apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: docker.io/xorhub/firefox:1.0.0@sha256:def
    architectures: [amd64]
  - image: docker.io/xorhub/chrome:1.0.0@sha256:ghi
    architectures: [amd64, arm64, riscv64, amd64]
  - image: docker.io/xorhub/ubuntu-xfce:1.1.0@sha256:abc
`

func TestCatalogSyncWorkerNormalizesArchitectures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(workerArchManifest))
	}))
	defer srv.Close()

	img := workerRegistryImage("waas-images", workerURLSource(srv.URL))
	w, _, catalogRepo := catalogWorkerFixture(t, img)
	ctx := context.Background()

	w.syncAll(ctx)

	entries, err := catalogRepo.ListEntries(ctx, "waas-images")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("catalog_entries = %+v, want 3 rows", entries)
	}
	want := map[string][]string{
		"docker.io/xorhub/firefox:1.0.0@sha256:def":     {"amd64"},
		"docker.io/xorhub/chrome:1.0.0@sha256:ghi":      {"amd64", "arm64"}, // unknown + duplicate dropped
		"docker.io/xorhub/ubuntu-xfce:1.1.0@sha256:abc": nil,                // absent = unknown
	}
	for _, e := range entries {
		got, ok := want[e.Image]
		if !ok {
			t.Errorf("unexpected catalog entry %s", e.Image)
			continue
		}
		if !slices.Equal(e.Architectures, got) {
			t.Errorf("%s: architectures = %v, want %v", e.Image, e.Architectures, got)
		}
	}
}

func TestNormalizeDescription(t *testing.T) {
	if got := normalizeDescription("  spaced out  "); got != "spaced out" {
		t.Errorf("trim: got %q", got)
	}
	// The cap is a defensive bound against a hostile manifest, sized
	// far above any legitimate description; the cut must stay valid
	// UTF-8 even when it lands mid-rune.
	long := strings.Repeat("a", 2047) + "héllo"
	got := normalizeDescription(long)
	if len(got) > 2048 || !utf8.ValidString(got) {
		t.Errorf("cap: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
	if short := normalizeDescription("short"); short != "short" {
		t.Errorf("under-cap description must pass through, got %q", short)
	}
}

func TestCatalogSyncWorkerFailureLeavesTableUntouched(t *testing.T) {
	healthy := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(workerCatalogManifest))
	}))
	defer srv.Close()

	img := workerRegistryImage("waas-images", workerURLSource(srv.URL))
	w, c, catalogRepo := catalogWorkerFixture(t, img)
	ctx := context.Background()

	w.syncAll(ctx)
	before, err := catalogRepo.ListEntries(ctx, "waas-images")
	if err != nil || len(before) != 2 {
		t.Fatalf("precondition failed: %+v %v", before, err)
	}
	beforeStatus := workerCatalogStatus(t, c, "default", "waas-images")

	healthy = false
	w.syncAll(ctx)

	after, err := catalogRepo.ListEntries(ctx, "waas-images")
	if err != nil {
		t.Fatal(err)
	}
	// Stale-but-served: the table must stay exactly as it was.
	if len(after) != 2 {
		t.Fatalf("catalog_entries changed on a failed sync: %+v", after)
	}
	st := workerCatalogStatus(t, c, "default", "waas-images")
	if st.LastSyncError == "" {
		t.Fatal("want lastSyncError set on failure")
	}
	if !st.LastSyncTime.Equal(beforeStatus.LastSyncTime) {
		t.Error("lastSyncTime must keep the last SUCCESS time, not the failed attempt")
	}
	if st.Source != catalogSourceFetched {
		t.Errorf("source must be kept from the last success, got %q", st.Source)
	}
}

func TestCatalogSyncWorkerSyncNowSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(workerCatalogManifest))
	}))
	defer srv.Close()

	img := workerRegistryImage("waas-images", workerURLSource(srv.URL))
	// Interval 0 would disable Run(); SyncNow must not depend on it.
	w, _, catalogRepo := catalogWorkerFixture(t, img)
	w.interval = 0
	ctx := context.Background()

	if err := w.SyncNow(ctx, img); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	entries, err := catalogRepo.ListEntries(ctx, "waas-images")
	if err != nil || len(entries) != 2 {
		t.Fatalf("catalog_entries = %+v %v, want 2 rows", entries, err)
	}
	// The passed img must carry the fresh status in-memory: the admin
	// endpoint projects it without a re-Get.
	if img.Status.Catalog == nil || img.Status.Catalog.Source != catalogSourceFetched ||
		img.Status.Catalog.LastSyncTime == nil || img.Status.Catalog.LastSyncError != "" {
		t.Fatalf("in-memory status not updated: %+v", img.Status.Catalog)
	}
}

func TestCatalogSyncWorkerSyncNowFailureReturnsErrorAndKeepsEntries(t *testing.T) {
	healthy := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(workerCatalogManifest))
	}))
	defer srv.Close()

	img := workerRegistryImage("waas-images", workerURLSource(srv.URL))
	w, c, catalogRepo := catalogWorkerFixture(t, img)
	ctx := context.Background()

	if err := w.SyncNow(ctx, img); err != nil {
		t.Fatalf("precondition sync: %v", err)
	}

	healthy = false
	err := w.SyncNow(ctx, img)
	// Unlike the ticker path, the caller must see the fetch error.
	if err == nil {
		t.Fatal("SyncNow must return the fetch error")
	}
	// Stale-but-served: the table keeps the last successful entries.
	entries, lerr := catalogRepo.ListEntries(ctx, "waas-images")
	if lerr != nil || len(entries) != 2 {
		t.Fatalf("catalog_entries changed on a failed sync: %+v %v", entries, lerr)
	}
	st := workerCatalogStatus(t, c, "default", "waas-images")
	if st.LastSyncError == "" {
		t.Fatal("want lastSyncError patched on failure")
	}
}

// TestCatalogSyncWorkerSyncNowBoundedWhileBusy pins the F8 bound: a
// force-sync queued behind another image's in-flight sync must give up
// at its deadline with errSyncBusy instead of hanging the
// HTTP request that carries it. The held semaphore stands in for that
// unrelated in-flight sync; the short caller deadline (tighter than
// catalogForceSyncTimeout, which WithTimeout only ever lowers to) keeps
// the test fast.
func TestCatalogSyncWorkerSyncNowBoundedWhileBusy(t *testing.T) {
	img := workerRegistryImage("waas-images", workerURLSource("http://catalog.invalid"))
	w, _, _ := catalogWorkerFixture(t, img)

	w.syncSem <- struct{}{}
	defer func() { <-w.syncSem }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.SyncNow(ctx, img) }()
	select {
	case err := <-done:
		// The sentinel, not context.DeadlineExceeded: only the give-up on
		// the wait may map to the admin 503.
		if !errors.Is(err, errSyncBusy) {
			t.Fatalf("SyncNow error = %v, want errSyncBusy", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SyncNow still blocked behind the busy sync at its deadline")
	}
}

func TestCatalogSyncWorkerIndependentPerImage(t *testing.T) {
	healthySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(workerCatalogManifest))
	}))
	defer healthySrv.Close()
	brokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer brokenSrv.Close()

	good := workerRegistryImage("good-registry", workerURLSource(healthySrv.URL))
	bad := workerRegistryImage("bad-registry", workerURLSource(brokenSrv.URL))
	w, c, catalogRepo := catalogWorkerFixture(t, good, bad)
	ctx := context.Background()

	w.syncAll(ctx)

	goodEntries, err := catalogRepo.ListEntries(ctx, "good-registry")
	if err != nil || len(goodEntries) != 2 {
		t.Fatalf("good-registry unaffected by the other's failure: %+v %v", goodEntries, err)
	}
	goodStatus := workerCatalogStatus(t, c, "default", "good-registry")
	if goodStatus == nil || goodStatus.LastSyncError != "" {
		t.Fatalf("good-registry status should be clean: %+v", goodStatus)
	}

	badEntries, err := catalogRepo.ListEntries(ctx, "bad-registry")
	if err != nil || len(badEntries) != 0 {
		t.Fatalf("bad-registry must have no entries: %+v %v", badEntries, err)
	}
	badStatus := workerCatalogStatus(t, c, "default", "bad-registry")
	if badStatus == nil || badStatus.LastSyncError == "" {
		t.Fatalf("bad-registry status should record the failure: %+v", badStatus)
	}
}

func TestCatalogSyncWorkerRunSyncsImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(workerCatalogManifest))
	}))
	defer srv.Close()

	img := workerRegistryImage("waas-images", workerURLSource(srv.URL))
	c := fake.NewClientBuilder().
		WithScheme(k8s.Scheme).
		WithObjects(img).
		WithStatusSubresource(&waasv1alpha1.WorkspaceImage{}).
		Build()
	db, err := database.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	catalogRepo := repository.NewSQLCatalogRepository(db)
	// A long interval: if Run() waited for the first tick, this test
	// would time out instead of observing an immediate sync.
	w := NewCatalogSyncWorker(c, "default", catalogRepo, time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(done)
	}()

	waitForEntries(t, catalogRepo, "waas-images", 2)
	cancel()
	<-done
}

func TestCatalogSyncWorkerBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		_, _ = w.Write([]byte(workerCatalogManifest))
	}))
	defer srv.Close()

	cat := workerURLSource(srv.URL)
	cat.Auth = &waasv1alpha1.ImageCatalogAuth{
		BearerToken: &waasv1alpha1.BearerTokenAuth{SecretRef: "catalog-token"},
	}
	img := workerRegistryImage("waas-images", cat)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "catalog-token", Namespace: "default"},
		Data:       map[string][]byte{"token": []byte("s3cr3t")},
	}
	w, _, catalogRepo := catalogWorkerFixture(t, img, secret)
	w.syncAll(context.Background())

	if gotAuth != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want bearer token", gotAuth)
	}
	entries, err := catalogRepo.ListEntries(context.Background(), "waas-images")
	if err != nil || len(entries) != 2 {
		t.Fatalf("catalog_entries = %+v %v, want 2 rows", entries, err)
	}
}

func TestCatalogSyncWorkerSkipsNonCatalogEntries(t *testing.T) {
	exact := &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "exact", Namespace: "default"},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "Exact entry",
			Image:       "docker.io/xorhub/firefox:1.0.0@sha256:def",
			Protocols:   []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC},
			Enabled:     true,
		},
	}
	registryNoCatalog := &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "registry-plain", Namespace: "default"},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "Registry without catalog",
			Registry:    "docker.io/kasmweb",
			Protocols:   []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC},
			Enabled:     true,
		},
	}
	w, c, catalogRepo := catalogWorkerFixture(t, exact, registryNoCatalog)
	w.syncAll(context.Background())

	for _, name := range []string{"exact", "registry-plain"} {
		entries, err := catalogRepo.ListEntries(context.Background(), name)
		if err != nil || len(entries) != 0 {
			t.Errorf("%s: want no catalog entries, got %+v %v", name, entries, err)
		}
		if st := workerCatalogStatus(t, c, "default", name); st != nil {
			t.Errorf("%s: no status expected, got %+v", name, st)
		}
	}
}

// syncPending re-reads the image at consume time because the event that
// queued the name may be stale. Both failures of that re-read are
// swallowed on purpose — the queue is best-effort and the next event
// re-queues — so nothing but a test proves they do not take the worker
// down with them. The distinction matters: a deleted image is the
// ORDINARY outcome of a delete event and must stay silent, while an
// unreachable API server is worth a log line.
func TestSyncPendingSurvivesAVanishedOrUnreadableImage(t *testing.T) {
	t.Run("image deleted between the event and the consume", func(t *testing.T) {
		w, _, catalogRepo := catalogWorkerFixture(t)

		w.syncPending(context.Background(), "gone")

		if entries, err := catalogRepo.ListEntries(context.Background(), "gone"); err != nil || len(entries) != 0 {
			t.Fatalf("a vanished image must sync nothing, got %+v %v", entries, err)
		}
	})

	t.Run("the read itself fails", func(t *testing.T) {
		boom := errors.New("apiserver unreachable")
		c := fake.NewClientBuilder().
			WithScheme(k8s.Scheme).
			WithStatusSubresource(&waasv1alpha1.WorkspaceImage{}).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
					return boom
				},
			}).
			Build()
		db, err := database.Open(filepath.Join(t.TempDir(), "catalog.db"))
		if err != nil {
			t.Fatalf("opening database: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		catalogRepo := repository.NewSQLCatalogRepository(db)
		w := NewCatalogSyncWorker(c, "default", catalogRepo, time.Minute)

		// The point: it returns rather than propagating or panicking, so
		// one unreadable image cannot stop the consumer loop.
		w.syncPending(context.Background(), "unreadable")

		if entries, err := catalogRepo.ListEntries(context.Background(), "unreadable"); err != nil || len(entries) != 0 {
			t.Fatalf("a failed read must sync nothing, got %+v %v", entries, err)
		}
	})
}
