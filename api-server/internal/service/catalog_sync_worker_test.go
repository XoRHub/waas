package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
  - image: docker.io/xorhub/ubuntu-xfce:1.1.0@sha256:abc
    os: weirdos
    app: ubuntu-xfce
`

// catalogWorkerFixture builds a CatalogSyncWorker on a fake k8s client
// (status subresource enabled for WorkspaceImage) and a real
// SQLCatalogRepository backed by a throwaway sqlite file.
func catalogWorkerFixture(t *testing.T, objs ...client.Object) (*CatalogSyncWorker, client.Client, repository.CatalogRepository) {
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
	}

	st := workerCatalogStatus(t, c, "default", "waas-images")
	if st == nil || st.Source != catalogSourceFetched || st.LastSyncTime == nil || st.LastSyncError != "" {
		t.Fatalf("sync bookkeeping wrong: %+v", st)
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

	deadline := time.After(time.Second)
	for {
		entries, err := catalogRepo.ListEntries(context.Background(), "waas-images")
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Run() did not sync immediately on start")
		case <-time.After(10 * time.Millisecond):
		}
	}
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
