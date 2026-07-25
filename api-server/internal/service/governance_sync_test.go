package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"

	"github.com/xorhub/waas/api-server/internal/apierror"
	"github.com/xorhub/waas/api-server/internal/database"
	"github.com/xorhub/waas/api-server/internal/k8s"
	"github.com/xorhub/waas/api-server/internal/model"
	"github.com/xorhub/waas/api-server/internal/repository"
	"github.com/xorhub/waas/shared/auth"
)

// newSyncFixture wires a GovernanceService with a CatalogSyncWorker
// (interval 0 — the ticker is disabled, exactly the configuration
// where the admin force-sync must still work), seeded with the given
// WorkspaceImages.
func newSyncFixture(t *testing.T, images ...*waasv1alpha1.WorkspaceImage) (*GovernanceService, *AuditService) {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "gov-sync.db"))
	if err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	kube, err := k8s.NewClient(true)
	if err != nil {
		t.Fatalf("building fake kube client: %v", err)
	}
	for _, img := range images {
		img.Namespace = testNS
		if err := kube.Create(context.Background(), img); err != nil {
			t.Fatalf("seeding image %s: %v", img.Name, err)
		}
	}

	users := repository.NewSQLUserRepository(db)
	audit := NewAuditService(repository.NewSQLAuditRepository(db))
	catalogRepo := repository.NewSQLCatalogRepository(db)
	worker := NewCatalogSyncWorker(kube, testNS, catalogRepo, 0)
	svc := NewGovernanceService(kube, testNS, users, audit, catalogRepo).
		WithCatalogSyncer(worker)
	return svc, audit
}

func syncRegistryImage(name, url string) *waasv1alpha1.WorkspaceImage {
	return &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "XorHub images",
			Registry:    "docker.io/xorhub",
			Protocols:   []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC},
			Enabled:     true,
			Catalog:     workerURLSource(url),
		},
	}
}

func lastAudit(t *testing.T, audit *AuditService) *model.AuditLog {
	t.Helper()
	rows, _, err := audit.List(context.Background(), repository.AuditFilter{}, 1, 1)
	if err != nil {
		t.Fatalf("listing audit rows: %v", err)
	}
	if len(rows) == 0 {
		return nil
	}
	return &rows[0]
}

func TestAdminSyncImageSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(workerCatalogManifest))
	}))
	defer srv.Close()

	svc, audit := newSyncFixture(t, syncRegistryImage("waas-images", srv.URL))
	admin := Actor{ID: "a1", Username: "admin", Role: string(auth.RoleAdmin)}

	m, err := svc.AdminSyncImage(context.Background(), admin, "waas-images")
	if err != nil {
		t.Fatalf("AdminSyncImage: %v", err)
	}
	// The response must carry the fresh state, no second call needed.
	if m.Catalog == nil || m.Catalog.Source != catalogSourceFetched ||
		m.Catalog.LastSyncTime == nil || m.Catalog.LastSyncError != "" {
		t.Fatalf("catalog status = %+v, want fresh success", m.Catalog)
	}
	if len(m.Discovered) != 2 {
		t.Fatalf("discovered = %+v, want the 2 manifest entries", m.Discovered)
	}
	row := lastAudit(t, audit)
	if row == nil || row.Action != "catalog.image_synced" || row.Detail != "" {
		t.Fatalf("audit row = %+v, want clean catalog.image_synced", row)
	}
}

func TestAdminSyncImageFetchFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	svc, audit := newSyncFixture(t, syncRegistryImage("waas-images", srv.URL))
	admin := Actor{ID: "a1", Username: "admin", Role: string(auth.RoleAdmin)}
	ctx := context.Background()

	_, err := svc.AdminSyncImage(ctx, admin, "waas-images")
	if err == nil {
		t.Fatal("want an error on fetch failure")
	}
	var problem *apierror.Problem
	if !errors.As(err, &problem) || problem.Status != http.StatusBadGateway {
		t.Fatalf("err = %v, want a 502 problem", err)
	}
	// Fail-soft: the error must also land in the persisted status,
	// visible through the normal admin listing.
	images, err := svc.AdminListImages(ctx)
	if err != nil {
		t.Fatalf("AdminListImages: %v", err)
	}
	if len(images) != 1 || images[0].Catalog == nil || images[0].Catalog.LastSyncError == "" {
		t.Fatalf("images = %+v, want lastSyncError surfaced", images)
	}
	row := lastAudit(t, audit)
	if row == nil || row.Action != "catalog.image_synced" || row.Detail == "" {
		t.Fatalf("audit row = %+v, want catalog.image_synced with error detail", row)
	}
}

// TestAdminSyncImageBusyAnswers503 pins the status the F8 bound answers
// with: a force-sync that hits its deadline while queued behind another
// image's in-flight sync is a retryable server condition — 503, never
// the 502 reserved for a broken catalog source.
func TestAdminSyncImageBusyAnswers503(t *testing.T) {
	svc, _ := newSyncFixture(t, syncRegistryImage("waas-images", "http://catalog.invalid"))
	admin := Actor{ID: "a1", Username: "admin", Role: string(auth.RoleAdmin)}

	// Stand-in for an unrelated image's sync in flight; the short caller
	// deadline keeps the test fast (WithTimeout only ever lowers it).
	svc.syncer.syncSem <- struct{}{}
	defer func() { <-svc.syncer.syncSem }()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := svc.AdminSyncImage(ctx, admin, "waas-images")
	var problem *apierror.Problem
	if !errors.As(err, &problem) || problem.Status != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want a 503 problem", err)
	}
}

// TestAdminSyncImageHangingSourceAnswers502 pins the boundary of the
// busy 503: a source that hangs until the fetch timeout is a broken
// catalog source and must keep answering 502 with the fetch error,
// exactly like a source answering 500. It exists because a Go
// client-timeout error ALSO satisfies errors.Is(err,
// context.DeadlineExceeded) — mapping the 503 on that instead of the
// busy sentinel would misfile this case as "server busy".
func TestAdminSyncImageHangingSourceAnswers502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // hang until the client gives up
	}))
	defer srv.Close()

	svc, _ := newSyncFixture(t, syncRegistryImage("waas-images", srv.URL))
	// Injectable client: shrink the fetch timeout so the test does not
	// sit through the real catalogFetchTimeout.
	svc.syncer.HTTPClient = &http.Client{Timeout: 100 * time.Millisecond}
	admin := Actor{ID: "a1", Username: "admin", Role: string(auth.RoleAdmin)}

	_, err := svc.AdminSyncImage(context.Background(), admin, "waas-images")
	var problem *apierror.Problem
	if !errors.As(err, &problem) || problem.Status != http.StatusBadGateway {
		t.Fatalf("err = %v, want a 502 problem", err)
	}
}

func TestAdminSyncImageNotFound(t *testing.T) {
	svc, _ := newSyncFixture(t)
	admin := Actor{ID: "a1", Role: string(auth.RoleAdmin)}
	_, err := svc.AdminSyncImage(context.Background(), admin, "ghost")
	if !apierror.IsNotFound(err) {
		t.Fatalf("err = %v, want 404 problem", err)
	}
}

// urlCatalogInput is registryImageInput pointed at a live url source —
// the shape the governance editor sends for a fetched catalog.
func urlCatalogInput(url string) UpsertImageInput {
	return registryImageInput(&model.CatalogSourceModel{From: model.CatalogSourceFrom{URL: url}})
}

// TestAdminUpsertImageSyncsWhenItBecomesEligible covers the entry that
// carried a catalog block while it could never sync (exact-image mode —
// nothing forbids the block there) and is then switched to registry
// mode: the source did not move, but this is the first PUT that can
// sync, so the response must already carry the entries.
func TestAdminUpsertImageSyncsWhenItBecomesEligible(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(workerCatalogManifest))
	}))
	defer srv.Close()

	svc, _ := newSyncFixture(t)
	admin := Actor{ID: "a1", Username: "admin", Role: string(auth.RoleAdmin)}
	ctx := context.Background()

	exact := urlCatalogInput(srv.URL)
	exact.Registry = ""
	exact.Image = "docker.io/xorhub/firefox:1.0.0"
	if _, err := svc.AdminUpsertImage(ctx, admin, "becomes-eligible", exact); err != nil {
		t.Fatalf("creating the exact-image entry: %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("manifest fetches = %d for an exact-image entry, want 0", hits.Load())
	}

	// Same catalog block, registry mode this time.
	m, err := svc.AdminUpsertImage(ctx, admin, "becomes-eligible", urlCatalogInput(srv.URL))
	if err != nil {
		t.Fatalf("switching to registry mode: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("manifest fetches = %d once the entry became eligible, want 1", hits.Load())
	}
	if len(m.Discovered) != 2 {
		t.Fatalf("discovered = %+v, want the 2 manifest entries in the PUT response", m.Discovered)
	}
}

// TestAdminUpsertImageCreateSyncsCatalog pins the auto-sync on the API
// creation path: the PUT response must already carry the discovered
// entries — no tick, no manual "Sync now".
func TestAdminUpsertImageCreateSyncsCatalog(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(workerCatalogManifest))
	}))
	defer srv.Close()

	svc, audit := newSyncFixture(t)
	admin := Actor{ID: "a1", Username: "admin", Role: string(auth.RoleAdmin)}

	m, err := svc.AdminUpsertImage(context.Background(), admin, "waas-images", urlCatalogInput(srv.URL))
	if err != nil {
		t.Fatalf("AdminUpsertImage: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("manifest fetches = %d, want exactly 1 on creation", hits.Load())
	}
	if m.Catalog == nil || m.Catalog.LastSyncTime == nil || m.Catalog.LastSyncError != "" {
		t.Fatalf("catalog status = %+v, want fresh success in the PUT response", m.Catalog)
	}
	if len(m.Discovered) != 2 {
		t.Fatalf("discovered = %+v, want the 2 manifest entries in the PUT response", m.Discovered)
	}
	row := lastAudit(t, audit)
	if row == nil || row.Action != "catalog.image_synced" || row.Detail != "" {
		t.Fatalf("audit row = %+v, want clean catalog.image_synced after image_created", row)
	}
}

// TestAdminUpsertImageSyncFailureDoesNotFailPut pins the best-effort
// contract: the CR write succeeds, the fetch error only lands in
// lastSyncError, and previously discovered entries survive
// (stale-but-served).
func TestAdminUpsertImageSyncFailureDoesNotFailPut(t *testing.T) {
	healthy := atomic.Bool{}
	healthy.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(workerCatalogManifest))
	}))
	defer srv.Close()

	svc, _ := newSyncFixture(t)
	admin := Actor{ID: "a1", Username: "admin", Role: string(auth.RoleAdmin)}
	ctx := context.Background()

	if _, err := svc.AdminUpsertImage(ctx, admin, "waas-images", urlCatalogInput(srv.URL)); err != nil {
		t.Fatalf("precondition upsert: %v", err)
	}

	// Repoint the source while it is broken: the update must still be a
	// success carrying the stale entries.
	healthy.Store(false)
	m, err := svc.AdminUpsertImage(ctx, admin, "waas-images", urlCatalogInput(srv.URL+"/v2"))
	if err != nil {
		t.Fatalf("upsert must not fail on a fetch error: %v", err)
	}
	if m.Catalog == nil || m.Catalog.LastSyncError == "" {
		t.Fatalf("catalog status = %+v, want lastSyncError surfaced", m.Catalog)
	}
	if len(m.Discovered) != 2 {
		t.Fatalf("discovered = %+v, want the stale 2 entries kept", m.Discovered)
	}
}

// TestAdminUpsertImageWithoutCatalogNoFetch: ineligible entries (exact
// image, or registry without spec.catalog) must not trigger any fetch.
func TestAdminUpsertImageWithoutCatalogNoFetch(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	svc, _ := newSyncFixture(t)
	admin := Actor{ID: "a1", Username: "admin", Role: string(auth.RoleAdmin)}
	ctx := context.Background()

	exact := UpsertImageInput{
		DisplayName: "Exact entry",
		Image:       "docker.io/xorhub/firefox:1.0.0@sha256:def",
		Protocols:   []string{"vnc"},
	}
	if _, err := svc.AdminUpsertImage(ctx, admin, "exact", exact); err != nil {
		t.Fatalf("AdminUpsertImage(exact): %v", err)
	}
	if _, err := svc.AdminUpsertImage(ctx, admin, "registry-plain", registryImageInput(nil)); err != nil {
		t.Fatalf("AdminUpsertImage(registry-plain): %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("manifest fetches = %d, want none for ineligible entries", hits.Load())
	}
}

// TestAdminUpsertImageResyncsOnlyWhenSourceChanges pins the settled
// update rule: editing display fields never refetches; repointing
// spec.catalog does.
func TestAdminUpsertImageResyncsOnlyWhenSourceChanges(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(workerCatalogManifest))
	}))
	defer srv.Close()

	svc, _ := newSyncFixture(t)
	admin := Actor{ID: "a1", Username: "admin", Role: string(auth.RoleAdmin)}
	ctx := context.Background()

	if _, err := svc.AdminUpsertImage(ctx, admin, "waas-images", urlCatalogInput(srv.URL)); err != nil {
		t.Fatalf("creation upsert: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("manifest fetches = %d after creation, want 1", hits.Load())
	}

	renamed := urlCatalogInput(srv.URL)
	renamed.DisplayName = "Renamed images"
	if _, err := svc.AdminUpsertImage(ctx, admin, "waas-images", renamed); err != nil {
		t.Fatalf("display-only update: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("manifest fetches = %d after a display-only edit, want still 1", hits.Load())
	}

	if _, err := svc.AdminUpsertImage(ctx, admin, "waas-images", urlCatalogInput(srv.URL+"/v2")); err != nil {
		t.Fatalf("source update: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("manifest fetches = %d after repointing the source, want 2", hits.Load())
	}
}

func TestAdminSyncImageWithoutCatalogSource(t *testing.T) {
	exact := &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "exact"},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "Exact entry",
			Image:       "docker.io/xorhub/firefox:1.0.0@sha256:def",
			Protocols:   []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC},
			Enabled:     true,
		},
	}
	svc, _ := newSyncFixture(t, exact)
	admin := Actor{ID: "a1", Role: string(auth.RoleAdmin)}

	_, err := svc.AdminSyncImage(context.Background(), admin, "exact")
	if !apierror.IsBadRequest(err) {
		t.Fatalf("err = %v, want 400 problem", err)
	}
	// And the listing must not offer the action: Catalog stays nil.
	images, err := svc.AdminListImages(context.Background())
	if err != nil {
		t.Fatalf("AdminListImages: %v", err)
	}
	if len(images) != 1 || images[0].Catalog != nil {
		t.Fatalf("images = %+v, want Catalog nil for a plain entry", images)
	}
}
