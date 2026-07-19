package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

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

func TestAdminSyncImageNotFound(t *testing.T) {
	svc, _ := newSyncFixture(t)
	admin := Actor{ID: "a1", Role: string(auth.RoleAdmin)}
	_, err := svc.AdminSyncImage(context.Background(), admin, "ghost")
	if !apierror.IsNotFound(err) {
		t.Fatalf("err = %v, want 404 problem", err)
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
