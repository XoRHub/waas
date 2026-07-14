package envtest

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// CEL rules of WorkspaceImage.spec.catalog: the source is exactly one
// of url/configMapKeyRef/secretKeyRef, and auth only combines with the
// url variant. Plus the status subresource, new on this CRD.

func TestWorkspaceImageCatalogCEL(t *testing.T) {
	requireEnv(t)
	ns := newNS(t, "crd-catalog")
	ctx := context.Background()

	base := func(name string, cat *waasv1alpha1.ImageCatalogSpec) *waasv1alpha1.WorkspaceImage {
		return &waasv1alpha1.WorkspaceImage{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: waasv1alpha1.WorkspaceImageSpec{
				DisplayName: "Test",
				Registry:    "ghcr.io/xorhub/waas-images",
				Protocols:   []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC},
				Catalog:     cat,
			},
		}
	}
	cmRef := &waasv1alpha1.CatalogConfigMapSource{Name: "catalog"}
	secretRef := &waasv1alpha1.CatalogSecretSource{Name: "catalog", Key: "manifest.yaml"}

	noSource := base("no-source", &waasv1alpha1.ImageCatalogSpec{})
	if err := adminCli.Create(ctx, noSource); err == nil || !strings.Contains(err.Error(), "exactly one of url, configMapKeyRef, or secretKeyRef") {
		t.Fatalf("empty from must be rejected by the CEL rule, got %v", err)
	}

	twoSources := base("two-sources", &waasv1alpha1.ImageCatalogSpec{
		From: waasv1alpha1.ImageCatalogSource{
			URL:             "https://example.com/catalog.yaml",
			ConfigMapKeyRef: cmRef,
		},
	})
	if err := adminCli.Create(ctx, twoSources); err == nil || !strings.Contains(err.Error(), "exactly one of url, configMapKeyRef, or secretKeyRef") {
		t.Fatalf("two sources must be rejected by the CEL rule, got %v", err)
	}

	authOnStatic := base("auth-static", &waasv1alpha1.ImageCatalogSpec{
		From: waasv1alpha1.ImageCatalogSource{ConfigMapKeyRef: cmRef},
		Auth: &waasv1alpha1.ImageCatalogAuth{
			BearerToken: &waasv1alpha1.BearerTokenAuth{SecretRef: "token"},
		},
	})
	if err := adminCli.Create(ctx, authOnStatic); err == nil || !strings.Contains(err.Error(), "auth is only meaningful when from.url is set") {
		t.Fatalf("auth on a static source must be rejected, got %v", err)
	}

	authOnURL := base("auth-url", &waasv1alpha1.ImageCatalogSpec{
		From: waasv1alpha1.ImageCatalogSource{URL: "https://example.com/catalog.yaml"},
		Auth: &waasv1alpha1.ImageCatalogAuth{
			BearerToken: &waasv1alpha1.BearerTokenAuth{SecretRef: "token"},
		},
	})
	if err := adminCli.Create(ctx, authOnURL); err != nil {
		t.Fatalf("auth with url must be accepted: %v", err)
	}

	secretSource := base("secret-source", &waasv1alpha1.ImageCatalogSpec{
		From: waasv1alpha1.ImageCatalogSource{SecretKeyRef: secretRef},
	})
	if err := adminCli.Create(ctx, secretSource); err != nil {
		t.Fatalf("secretKeyRef source must be accepted: %v", err)
	}
}

func TestWorkspaceImageStatusSubresource(t *testing.T) {
	requireEnv(t)
	ns := newNS(t, "crd-catalog-status")
	ctx := context.Background()

	img := &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "with-status", Namespace: ns},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "Test",
			Registry:    "ghcr.io/xorhub/waas-images",
			Protocols:   []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC},
		},
	}
	if err := adminCli.Create(ctx, img); err != nil {
		t.Fatalf("creating image: %v", err)
	}

	img.Status.Catalog = &waasv1alpha1.ImageCatalogStatus{
		Source:       "Fetched",
		LastSyncTime: &metav1.Time{Time: metav1.Now().Time},
	}
	if err := adminCli.Status().Update(ctx, img); err != nil {
		t.Fatalf("status subresource write must be accepted: %v", err)
	}

	got := &waasv1alpha1.WorkspaceImage{}
	if err := adminCli.Get(ctx, types.NamespacedName{Namespace: ns, Name: "with-status"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Catalog == nil || got.Status.Catalog.Source != "Fetched" || got.Status.Catalog.LastSyncTime == nil {
		t.Fatalf("status.catalog not persisted: %+v", got.Status)
	}
}
