package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

const catalogManifest = `apiVersion: waas.xorhub.io/catalog/v1
images:
  - image: ghcr.io/xorhub/waas-images/firefox:1.0.0@sha256:def
    os: linux
    app: firefox
    version: "1.0.0"
    icon: firefox
  - image: ghcr.io/xorhub/waas-images/ubuntu-xfce:1.1.0@sha256:abc
    os: weirdos
    app: ubuntu-xfce
`

// catalogFixture builds the catalog reconciler on a fake client with
// the status subresource enabled for WorkspaceImage (the shared
// newFixture predates that subresource and does not enable it).
func catalogFixture(t *testing.T, objs ...client.Object) (*WorkspaceImageCatalogReconciler, client.Client) {
	t.Helper()
	c := fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&waasv1alpha1.WorkspaceImage{}).
		Build()
	return &WorkspaceImageCatalogReconciler{Client: c, SyncInterval: 42 * time.Minute}, c
}

func registryImage(cat *waasv1alpha1.ImageCatalogSpec) *waasv1alpha1.WorkspaceImage {
	return &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "waas-images", Namespace: "default"},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "XorHub images",
			Registry:    "ghcr.io/xorhub/waas-images",
			Protocols:   []waasv1alpha1.Protocol{waasv1alpha1.ProtocolKasmVNC},
			Enabled:     true,
			Catalog:     cat,
		},
	}
}

func urlSource(url string) *waasv1alpha1.ImageCatalogSpec {
	return &waasv1alpha1.ImageCatalogSpec{From: waasv1alpha1.ImageCatalogSource{URL: url}}
}

func reconcileCatalog(t *testing.T, r *WorkspaceImageCatalogReconciler, img *waasv1alpha1.WorkspaceImage) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: img.Namespace, Name: img.Name},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return res
}

func catalogStatus(t *testing.T, c client.Client, img *waasv1alpha1.WorkspaceImage) *waasv1alpha1.ImageCatalogStatus {
	t.Helper()
	got := &waasv1alpha1.WorkspaceImage{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: img.Namespace, Name: img.Name}, got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	return got.Status.Catalog
}

func TestCatalogFetchOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(catalogManifest))
	}))
	defer srv.Close()

	img := registryImage(urlSource(srv.URL))
	r, c := catalogFixture(t, img)
	res := reconcileCatalog(t, r, img)

	if res.RequeueAfter != r.SyncInterval {
		t.Errorf("RequeueAfter = %v, want %v", res.RequeueAfter, r.SyncInterval)
	}
	st := catalogStatus(t, c, img)
	if st == nil || len(st.Entries) != 2 {
		t.Fatalf("status.catalog = %+v, want 2 entries", st)
	}
	if st.Source != catalogSourceFetched || st.LastSyncTime == nil || st.LastSyncError != "" {
		t.Errorf("sync bookkeeping wrong: %+v", st)
	}
	if st.Entries[0].Icon != "firefox" || st.Entries[0].OS != waasv1alpha1.OSLinux {
		t.Errorf("first entry mismatch: %+v", st.Entries[0])
	}
	// Unknown os values must degrade to empty (enum-safe), not fail the sync.
	if st.Entries[1].OS != "" {
		t.Errorf("unknown os should map to empty, got %q", st.Entries[1].OS)
	}
}

func TestCatalogFetchWithBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		_, _ = w.Write([]byte(catalogManifest))
	}))
	defer srv.Close()

	cat := urlSource(srv.URL)
	cat.Auth = &waasv1alpha1.ImageCatalogAuth{
		BearerToken: &waasv1alpha1.BearerTokenAuth{SecretRef: "catalog-token"},
	}
	img := registryImage(cat)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "catalog-token", Namespace: "default"},
		Data:       map[string][]byte{"token": []byte("s3cr3t")},
	}
	r, c := catalogFixture(t, img, secret)
	reconcileCatalog(t, r, img)

	if gotAuth != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q, want bearer token", gotAuth)
	}
	if st := catalogStatus(t, c, img); st == nil || len(st.Entries) != 2 {
		t.Fatalf("status.catalog = %+v, want 2 entries", st)
	}
}

func TestCatalogFetchAuthSecretProblemsFailSoft(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(catalogManifest))
	}))
	defer srv.Close()

	for name, secret := range map[string]client.Object{
		"missing secret": nil,
		"missing token key": &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "catalog-token", Namespace: "default"},
			Data:       map[string][]byte{"password": []byte("nope")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			cat := urlSource(srv.URL)
			cat.Auth = &waasv1alpha1.ImageCatalogAuth{
				BearerToken: &waasv1alpha1.BearerTokenAuth{SecretRef: "catalog-token"},
			}
			img := registryImage(cat)
			objs := []client.Object{img}
			if secret != nil {
				objs = append(objs, secret)
			}
			r, c := catalogFixture(t, objs...)
			res := reconcileCatalog(t, r, img)

			if res.RequeueAfter != r.SyncInterval {
				t.Errorf("RequeueAfter = %v, want %v (same cadence on failure)", res.RequeueAfter, r.SyncInterval)
			}
			st := catalogStatus(t, c, img)
			if st == nil || st.LastSyncError == "" {
				t.Fatalf("want lastSyncError, got %+v", st)
			}
			if len(st.Entries) != 0 || st.Source != "" || st.LastSyncTime != nil {
				t.Errorf("failed first sync must not fabricate results: %+v", st)
			}
		})
	}
}

func TestCatalogFetchFailureKeepsStaleEntries(t *testing.T) {
	healthy := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(catalogManifest))
	}))
	defer srv.Close()

	img := registryImage(urlSource(srv.URL))
	r, c := catalogFixture(t, img)
	reconcileCatalog(t, r, img)
	before := catalogStatus(t, c, img)
	if before == nil || len(before.Entries) != 2 {
		t.Fatalf("precondition failed: %+v", before)
	}

	healthy = false
	res := reconcileCatalog(t, r, img)
	if res.RequeueAfter != r.SyncInterval {
		t.Errorf("RequeueAfter = %v, want %v", res.RequeueAfter, r.SyncInterval)
	}
	st := catalogStatus(t, c, img)
	if len(st.Entries) != 2 || st.Source != catalogSourceFetched {
		t.Errorf("stale-but-served violated: %+v", st)
	}
	if !st.LastSyncTime.Equal(before.LastSyncTime) {
		t.Errorf("lastSyncTime must keep the last SUCCESS time")
	}
	if !strings.Contains(st.LastSyncError, "HTTP 500") {
		t.Errorf("lastSyncError = %q, want the HTTP failure", st.LastSyncError)
	}
}

func TestCatalogUnknownAPIVersionFailSoft(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("apiVersion: waas.xorhub.io/catalog/v99\nimages: []\n"))
	}))
	defer srv.Close()

	img := registryImage(urlSource(srv.URL))
	r, c := catalogFixture(t, img)
	reconcileCatalog(t, r, img)

	st := catalogStatus(t, c, img)
	if st == nil || !strings.Contains(st.LastSyncError, "unsupported catalog apiVersion") {
		t.Fatalf("want apiVersion rejection in lastSyncError, got %+v", st)
	}
}

func TestCatalogConfigMapSource(t *testing.T) {
	for name, ref := range map[string]*waasv1alpha1.CatalogConfigMapSource{
		"default key":  {Name: "catalog"},
		"explicit key": {Name: "catalog", Key: "custom.yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			key := ref.Key
			if key == "" {
				key = "catalog.yaml"
			}
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "catalog", Namespace: "default"},
				Data:       map[string]string{key: catalogManifest},
			}
			img := registryImage(&waasv1alpha1.ImageCatalogSpec{
				From: waasv1alpha1.ImageCatalogSource{ConfigMapKeyRef: ref},
			})
			r, c := catalogFixture(t, img, cm)
			res := reconcileCatalog(t, r, img)

			if res.RequeueAfter != r.SyncInterval {
				t.Errorf("RequeueAfter = %v, want %v (static sources re-read on the same cadence)", res.RequeueAfter, r.SyncInterval)
			}
			st := catalogStatus(t, c, img)
			if st == nil || len(st.Entries) != 2 || st.Source != catalogSourceStatic {
				t.Fatalf("status.catalog = %+v, want 2 Static entries", st)
			}
		})
	}
}

func TestCatalogSecretSource(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "catalog", Namespace: "default"},
		Data:       map[string][]byte{"manifest.yaml": []byte(catalogManifest)},
	}
	img := registryImage(&waasv1alpha1.ImageCatalogSpec{
		From: waasv1alpha1.ImageCatalogSource{
			SecretKeyRef: &waasv1alpha1.CatalogSecretSource{Name: "catalog", Key: "manifest.yaml"},
		},
	})
	r, c := catalogFixture(t, img, secret)
	reconcileCatalog(t, r, img)

	st := catalogStatus(t, c, img)
	if st == nil || len(st.Entries) != 2 || st.Source != catalogSourceStatic {
		t.Fatalf("status.catalog = %+v, want 2 Static entries", st)
	}
}

func TestCatalogStaticSourceProblemsFailSoft(t *testing.T) {
	cases := map[string]struct {
		objs []client.Object
		from waasv1alpha1.ImageCatalogSource
	}{
		"configmap missing": {
			from: waasv1alpha1.ImageCatalogSource{
				ConfigMapKeyRef: &waasv1alpha1.CatalogConfigMapSource{Name: "nope"},
			},
		},
		"configmap key absent": {
			objs: []client.Object{&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "catalog", Namespace: "default"},
				Data:       map[string]string{"other.yaml": catalogManifest},
			}},
			from: waasv1alpha1.ImageCatalogSource{
				ConfigMapKeyRef: &waasv1alpha1.CatalogConfigMapSource{Name: "catalog"},
			},
		},
		"secret missing": {
			from: waasv1alpha1.ImageCatalogSource{
				SecretKeyRef: &waasv1alpha1.CatalogSecretSource{Name: "nope", Key: "manifest.yaml"},
			},
		},
		"secret key absent": {
			objs: []client.Object{&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "catalog", Namespace: "default"},
				Data:       map[string][]byte{"other.yaml": []byte(catalogManifest)},
			}},
			from: waasv1alpha1.ImageCatalogSource{
				SecretKeyRef: &waasv1alpha1.CatalogSecretSource{Name: "catalog", Key: "manifest.yaml"},
			},
		},
		"content malformed": {
			objs: []client.Object{&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "catalog", Namespace: "default"},
				Data:       map[string]string{"catalog.yaml": "{images: ["},
			}},
			from: waasv1alpha1.ImageCatalogSource{
				ConfigMapKeyRef: &waasv1alpha1.CatalogConfigMapSource{Name: "catalog"},
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			img := registryImage(&waasv1alpha1.ImageCatalogSpec{From: tc.from})
			r, c := catalogFixture(t, append(tc.objs, img)...)
			res := reconcileCatalog(t, r, img)

			if res.RequeueAfter != r.SyncInterval {
				t.Errorf("RequeueAfter = %v, want %v", res.RequeueAfter, r.SyncInterval)
			}
			st := catalogStatus(t, c, img)
			if st == nil || st.LastSyncError == "" {
				t.Fatalf("want lastSyncError, got %+v", st)
			}
			if len(st.Entries) != 0 || st.Source != "" {
				t.Errorf("failed sync must not fabricate results: %+v", st)
			}
		})
	}
}

func TestCatalogStaticFailureKeepsStaleEntries(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "catalog", Namespace: "default"},
		Data:       map[string]string{"catalog.yaml": catalogManifest},
	}
	img := registryImage(&waasv1alpha1.ImageCatalogSpec{
		From: waasv1alpha1.ImageCatalogSource{
			ConfigMapKeyRef: &waasv1alpha1.CatalogConfigMapSource{Name: "catalog"},
		},
	})
	r, c := catalogFixture(t, img, cm)
	reconcileCatalog(t, r, img)
	if st := catalogStatus(t, c, img); st == nil || len(st.Entries) != 2 {
		t.Fatalf("precondition failed: %+v", st)
	}

	if err := c.Delete(context.Background(), cm); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	reconcileCatalog(t, r, img)
	st := catalogStatus(t, c, img)
	if len(st.Entries) != 2 || st.Source != catalogSourceStatic || st.LastSyncError == "" {
		t.Errorf("stale-but-served violated: %+v", st)
	}
}

func TestCatalogSkipsNonCatalogEntries(t *testing.T) {
	exact := &waasv1alpha1.WorkspaceImage{
		ObjectMeta: metav1.ObjectMeta{Name: "exact", Namespace: "default"},
		Spec: waasv1alpha1.WorkspaceImageSpec{
			DisplayName: "Exact entry",
			Image:       "ghcr.io/xorhub/waas-images/firefox:1.0.0@sha256:def",
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
	r, c := catalogFixture(t, exact, registryNoCatalog)
	for _, img := range []*waasv1alpha1.WorkspaceImage{exact, registryNoCatalog} {
		res := reconcileCatalog(t, r, img)
		if res.RequeueAfter != 0 {
			t.Errorf("%s: non-catalog entries must not requeue, got %v", img.Name, res.RequeueAfter)
		}
		got := &waasv1alpha1.WorkspaceImage{}
		if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: img.Name}, got); err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status.Catalog != nil {
			t.Errorf("%s: no status expected, got %+v", img.Name, got.Status.Catalog)
		}
	}
}
