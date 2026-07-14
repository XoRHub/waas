package controller

// The workspaceimage catalog reconciler periodically syncs the
// published catalog manifest (pkg/catalog wire-format) configured by
// WorkspaceImage.spec.catalog into status.catalog — display metadata
// (os/app/version/icon) for the portal picker, and NOTHING else:
// enforcement (FindImage/ImageAllowed) never reads it, and a sync can
// never block or delay workspace creation. It is the first reconciler
// of WorkspaceImage; the drift watch in workspace_controller.go is a
// different controller on the same GVK and is not triggered by these
// status writes (Status().Patch never bumps metadata.generation, so
// its GenerationChangedPredicate stays quiet).
//
// Failure doctrine is fail-soft and stale-but-served: any fetch/read/
// parse/auth problem only updates status.catalog.lastSyncError and
// never clears already-published entries. Note for the api-server
// side: each status patch emits one MODIFIED watch event on "images"
// (SSE), i.e. one catalog-picker refresh per image per sync interval —
// negligible volume and the refresh is exactly what the picker wants.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/shared/catalog"
)

const (
	// catalogDefaultKey is the ConfigMap key read when
	// from.configMapKeyRef.key is empty. Secrets get NO default: no
	// naming convention is assumed for a Secret.
	catalogDefaultKey = "catalog.yaml"
	// catalogTokenKey is the key of the bearer-token Secret named by
	// spec.catalog.auth.bearerToken.secretRef.
	catalogTokenKey = "token"
	// catalogFetchTimeout bounds one live manifest GET.
	catalogFetchTimeout = 10 * time.Second

	catalogSourceFetched = "Fetched"
	catalogSourceStatic  = "Static"
)

// WorkspaceImageCatalogReconciler syncs spec.catalog manifests into
// status.catalog on registry-mode WorkspaceImages.
type WorkspaceImageCatalogReconciler struct {
	client.Client
	Recorder record.EventRecorder
	// SyncInterval is the periodic re-sync cadence for BOTH source
	// kinds (live URL re-fetch and static ConfigMap/Secret re-read —
	// no dedicated watch on the referenced objects: the operator
	// deliberately reads Secrets/ConfigMaps uncached and without the
	// watch verb).
	SyncInterval time.Duration
	// HTTPClient serves the live fetches; nil uses a default client
	// with catalogFetchTimeout (injectable for httptest).
	HTTPClient *http.Client
}

// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspaceimages,verbs=get;list;watch
// +kubebuilder:rbac:groups=waas.xorhub.io,resources=workspaceimages/status,verbs=get;update;patch

// Reconcile syncs one WorkspaceImage's catalog. Purely cosmetic: it
// never touches spec, never blocks provisioning, and treats every
// data problem as a recorded sync failure instead of an error return.
func (r *WorkspaceImageCatalogReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	img := &waasv1alpha1.WorkspaceImage{}
	if err := r.Get(ctx, req.NamespacedName, img); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Catalog sync is only meaningful on registry-mode entries that
	// opted in; everything else is left alone, without requeue (an
	// edit of spec.catalog re-triggers through the For watch).
	if img.Spec.Registry == "" || img.Spec.Catalog == nil {
		return ctrl.Result{}, nil
	}

	orig := img.DeepCopy()
	entries, source, syncErr := r.sync(ctx, img)

	if img.Status.Catalog == nil {
		img.Status.Catalog = &waasv1alpha1.ImageCatalogStatus{}
	}
	if syncErr != nil {
		// Stale-but-served: entries, source and lastSyncTime all keep
		// their last good value; only the error is refreshed.
		img.Status.Catalog.LastSyncError = syncErr.Error()
	} else {
		img.Status.Catalog.Entries = entries
		img.Status.Catalog.Source = source
		img.Status.Catalog.LastSyncTime = &metav1.Time{Time: time.Now()}
		img.Status.Catalog.LastSyncError = ""
	}
	if err := r.Status().Patch(ctx, img, client.MergeFrom(orig)); err != nil {
		return ctrl.Result{}, fmt.Errorf("patching catalog status of %s: %w", req.Name, err)
	}
	// Same cadence on success and failure — a transient registry
	// hiccup heals at the next tick, not through error backoff.
	return ctrl.Result{RequeueAfter: r.SyncInterval}, nil
}

// sync loads and parses the manifest from the configured source and
// returns the discovered entries with their status.catalog.source
// label. Exactly one From variant is set (CEL-enforced at admission).
func (r *WorkspaceImageCatalogReconciler) sync(ctx context.Context, img *waasv1alpha1.WorkspaceImage) ([]waasv1alpha1.DiscoveredImage, string, error) {
	from := img.Spec.Catalog.From
	var (
		raw    []byte
		source string
		err    error
	)
	switch {
	case from.URL != "":
		raw, err = r.fetch(ctx, img)
		source = catalogSourceFetched
	case from.ConfigMapKeyRef != nil:
		raw, err = r.readConfigMapKey(ctx, img.Namespace, from.ConfigMapKeyRef)
		source = catalogSourceStatic
	case from.SecretKeyRef != nil:
		raw, err = r.readSecretKey(ctx, img.Namespace, from.SecretKeyRef)
		source = catalogSourceStatic
	default:
		// Unreachable past admission; grandfathered bad data is a sync
		// failure like any other, never a crash.
		err = fmt.Errorf("spec.catalog.from has no source set")
	}
	if err != nil {
		return nil, "", err
	}
	file, err := catalog.Parse(raw)
	if err != nil {
		return nil, "", err
	}
	return toDiscovered(file.Images), source, nil
}

// fetch GETs the live manifest, with the optional bearer token read
// from the platform workspace namespace (uncached read, like every
// Secret read of this operator).
func (r *WorkspaceImageCatalogReconciler) fetch(ctx context.Context, img *waasv1alpha1.WorkspaceImage) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, img.Spec.Catalog.From.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("building catalog request: %w", err)
	}
	if auth := img.Spec.Catalog.Auth; auth != nil && auth.BearerToken != nil {
		secret := &corev1.Secret{}
		key := types.NamespacedName{Namespace: img.Namespace, Name: auth.BearerToken.SecretRef}
		if err := r.Get(ctx, key, secret); err != nil {
			return nil, fmt.Errorf("reading bearer-token secret %s: %w", key.Name, err)
		}
		token, ok := secret.Data[catalogTokenKey]
		if !ok {
			return nil, fmt.Errorf("bearer-token secret %s has no %q key", key.Name, catalogTokenKey)
		}
		req.Header.Set("Authorization", "Bearer "+string(token))
	}
	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: catalogFetchTimeout}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching catalog: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching catalog: HTTP %d from %s", resp.StatusCode, img.Spec.Catalog.From.URL)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading catalog response: %w", err)
	}
	return body, nil
}

func (r *WorkspaceImageCatalogReconciler) readConfigMapKey(ctx context.Context, namespace string, ref *waasv1alpha1.CatalogConfigMapSource) ([]byte, error) {
	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, cm); err != nil {
		return nil, fmt.Errorf("reading catalog configmap %s: %w", ref.Name, err)
	}
	key := ref.Key
	if key == "" {
		key = catalogDefaultKey
	}
	if data, ok := cm.Data[key]; ok {
		return []byte(data), nil
	}
	if data, ok := cm.BinaryData[key]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("catalog configmap %s has no %q key", ref.Name, key)
}

func (r *WorkspaceImageCatalogReconciler) readSecretKey(ctx context.Context, namespace string, ref *waasv1alpha1.CatalogSecretSource) ([]byte, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, secret); err != nil {
		return nil, fmt.Errorf("reading catalog secret %s: %w", ref.Name, err)
	}
	// No default key for Secrets (the CRD requires one); grandfathered
	// empty keys surface as a plain missing-key sync failure.
	if data, ok := secret.Data[ref.Key]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("catalog secret %s has no %q key", ref.Name, ref.Key)
}

// toDiscovered is the single seam between the inter-repo wire-format
// (pkg/catalog, its own compatibility contract) and the CRD status
// type (v1alpha1/ADR 0002 cadence).
func toDiscovered(entries []catalog.Entry) []waasv1alpha1.DiscoveredImage {
	out := make([]waasv1alpha1.DiscoveredImage, 0, len(entries))
	for _, e := range entries {
		os := waasv1alpha1.OSType(e.OS)
		if os != waasv1alpha1.OSLinux && os != waasv1alpha1.OSWindows {
			// OSType is enum-validated on the CRD; an unknown value in
			// the manifest must degrade to "treated as linux" at the
			// picker, not fail the whole status patch.
			os = ""
		}
		out = append(out, waasv1alpha1.DiscoveredImage{
			Image:       e.Image,
			OS:          os,
			App:         e.App,
			Version:     e.Version,
			Icon:        e.Icon,
			DisplayName: e.DisplayName,
		})
	}
	return out
}

// SetupWithManager registers the catalog reconciler. No predicate:
// Reconcile itself skips entries without spec.catalog, and any
// spec.catalog edit (including switching source kinds) must re-sync
// immediately. Deliberately NO watch on the referenced
// ConfigMap/Secret — those reads bypass the cache by existing design
// (no watch verb in RBAC); a static catalog accepts the same
// propagation delay as a live one (up to SyncInterval).
func (r *WorkspaceImageCatalogReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&waasv1alpha1.WorkspaceImage{}).
		Named("workspaceimage-catalog").
		Complete(r)
}
