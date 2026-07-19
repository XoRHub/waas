package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/shared/catalog"

	"github.com/xorhub/waas/api-server/internal/repository"
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

// CatalogSyncWorker periodically syncs every registry-mode
// WorkspaceImage's published catalog manifest (spec.catalog) into the
// catalog_entries table — display metadata (os/app/version/icon) for
// the portal picker, and NOTHING else: enforcement (FindImage/
// ImageAllowed) never reads it, and a sync can never block or delay
// workspace creation.
//
// The split with the operator is by data ownership: only the
// api-server has both database and k8s access, so IT owns the sync —
// same rule as IdleSweeper/SessionSweeper. This worker replaces the
// operator's former workspaceimage_catalog reconciler; status.catalog
// keeps only source/lastSyncTime/lastSyncError (small, useful for
// `kubectl get workspaceimage`) — the discovered entries themselves
// live in Postgres now (catalog_entries), never in etcd.
//
// Failure doctrine is fail-soft and stale-but-served, unchanged from
// the former reconciler: any fetch/read/parse/auth problem only
// updates status.catalog.lastSyncError and never clears
// already-published catalog_entries rows — see syncOne.
type CatalogSyncWorker struct {
	kube      client.Client
	namespace string
	catalog   repository.CatalogRepository
	interval  time.Duration
	// mu serializes syncs: the admin force-sync (SyncNow) must never
	// interleave its fetch/ReplaceEntries/status-patch with the ticker's
	// pass over the same image.
	mu sync.Mutex
	// HTTPClient serves the live fetches; nil uses a default client
	// with catalogFetchTimeout (injectable for tests).
	HTTPClient *http.Client
}

// NewCatalogSyncWorker builds the worker; interval <= 0 disables it.
func NewCatalogSyncWorker(kube client.Client, namespace string, catalogRepo repository.CatalogRepository, interval time.Duration) *CatalogSyncWorker {
	return &CatalogSyncWorker{kube: kube, namespace: namespace, catalog: catalogRepo, interval: interval}
}

// Run blocks until ctx is done. Unlike IdleSweeper/SessionSweeper, it
// syncs IMMEDIATELY on start and then every interval: with a 6h
// default, waiting for the first tick would make a broken catalog
// source invisible for up to 6 hours right after a deploy.
func (w *CatalogSyncWorker) Run(ctx context.Context) {
	if w.interval <= 0 {
		return
	}
	slog.Info("catalog sync worker started", "interval", w.interval)
	w.syncAll(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.syncAll(ctx)
		}
	}
}

// syncAll syncs every eligible WorkspaceImage in its own iteration: a
// registry in trouble (HTTP timeout, missing ConfigMap/Secret) never
// blocks the sync of the others.
func (w *CatalogSyncWorker) syncAll(ctx context.Context) {
	images := &waasv1alpha1.WorkspaceImageList{}
	if err := w.kube.List(ctx, images, client.InNamespace(w.namespace)); err != nil {
		slog.Error("catalog sync: listing workspace images failed", "error", err)
		return
	}
	for i := range images.Items {
		img := &images.Items[i]
		if img.Spec.Registry == "" || img.Spec.Catalog == nil {
			continue
		}
		if err := w.syncOne(ctx, img); err != nil {
			slog.Error("catalog sync failed", "workspaceImage", img.Name, "error", err)
		}
	}
}

// SyncNow forces one immediate sync of img, serialized with the
// periodic ticker. Same fail-soft/stale-but-served semantics as the
// ticker path, but the sync error is returned to the caller; works
// even when interval <= 0 disabled Run. Mutates img's status in place
// on success, so the caller can project it without a re-Get.
func (w *CatalogSyncWorker) SyncNow(ctx context.Context, img *waasv1alpha1.WorkspaceImage) error {
	return w.syncOne(ctx, img)
}

// syncOne fetches/reads and parses one WorkspaceImage's catalog
// manifest.
//
// On success: catalog_entries is atomically replaced (ReplaceEntries)
// and status.catalog.lastSyncError is cleared.
//
// On failure (fetch, parse, or Secret/ConfigMap read): catalog_entries
// is left COMPLETELY untouched — stale-but-served, the whole point of
// this doctrine — and only status.catalog.lastSyncError is patched,
// then the sync error is returned (logged by syncAll, surfaced as a
// problem response by the admin force-sync).
func (w *CatalogSyncWorker) syncOne(ctx context.Context, img *waasv1alpha1.WorkspaceImage) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	entries, source, syncErr := w.fetchAndParse(ctx, img)

	orig := img.DeepCopy()
	if img.Status.Catalog == nil {
		img.Status.Catalog = &waasv1alpha1.ImageCatalogStatus{}
	}
	if syncErr != nil {
		img.Status.Catalog.LastSyncError = syncErr.Error()
		if err := w.kube.Status().Patch(ctx, img, client.MergeFrom(orig)); err != nil {
			return fmt.Errorf("patching catalog status of %s: %w", img.Name, err)
		}
		return fmt.Errorf("syncing catalog of %s: %w", img.Name, syncErr)
	}

	now := time.Now()
	repoEntries := make([]repository.CatalogEntry, 0, len(entries))
	for _, e := range entries {
		repoEntries = append(repoEntries, repository.CatalogEntry{
			Image:         e.Image,
			OS:            normalizeOS(e.OS),
			App:           e.App,
			Version:       e.Version,
			Icon:          e.Icon,
			DisplayName:   e.DisplayName,
			Profile:       normalizeProfile(e.Profile),
			Recommended:   marshalRecommended(e.Recommended),
			Architectures: normalizeArchitectures(e.Architectures),
			SyncedAt:      now,
		})
	}
	if err := w.catalog.ReplaceEntries(ctx, img.Name, repoEntries); err != nil {
		return fmt.Errorf("replacing catalog entries of %s: %w", img.Name, err)
	}

	img.Status.Catalog.Source = source
	img.Status.Catalog.LastSyncTime = &metav1.Time{Time: now}
	img.Status.Catalog.LastSyncError = ""
	if err := w.kube.Status().Patch(ctx, img, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("patching catalog status of %s: %w", img.Name, err)
	}
	return nil
}

// marshalRecommended serializes a parsed recommendation for the JSON
// column; nil (absent in the manifest) stays nil rather than "null" so
// the repository can tell "no recommendation" from "an empty one".
func marshalRecommended(r *catalog.Recommendation) json.RawMessage {
	if r == nil {
		return nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return nil
	}
	return b
}

// normalizeOS degrades an unrecognized os value to "" (treated as
// linux by the frontend) instead of propagating it — the manifest is
// untrusted content, this mirrors the OSType enum guard the former
// operator reconciler applied on the CRD status field.
func normalizeOS(os string) string {
	if os != string(waasv1alpha1.OSLinux) && os != string(waasv1alpha1.OSWindows) {
		return ""
	}
	return os
}

// normalizeProfile degrades an unrecognized profile value to "" (no
// badge shown by the frontend) instead of propagating it — same
// untrusted-manifest guard as normalizeOS, for the same reason: the
// badge would otherwise render a confidently wrong label for a typo'd
// or malicious value.
func normalizeProfile(profile string) string {
	if profile != catalog.ProfileHardened && profile != catalog.ProfileNormal {
		return ""
	}
	return profile
}

// normalizeArchitectures drops any value that is not a known
// architecture instead of propagating it — same untrusted-manifest
// guard as normalizeOS/normalizeProfile: a typo'd arch would otherwise
// end up prefilled into a template's nodeSelector. Nothing valid left
// = nil (unknown, no hint).
func normalizeArchitectures(archs []string) []string {
	var out []string
	for _, a := range archs {
		if (a == "amd64" || a == "arm64") && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// fetchAndParse loads and parses the manifest from the configured
// source and returns the parsed entries with their
// status.catalog.source label. Exactly one From variant is set
// (CEL-enforced at admission).
func (w *CatalogSyncWorker) fetchAndParse(ctx context.Context, img *waasv1alpha1.WorkspaceImage) ([]catalog.Entry, string, error) {
	from := img.Spec.Catalog.From
	var (
		raw    []byte
		source string
		err    error
	)
	switch {
	case from.URL != "":
		raw, err = w.fetch(ctx, img)
		source = catalogSourceFetched
	case from.ConfigMapKeyRef != nil:
		raw, err = w.readConfigMapKey(ctx, img.Namespace, from.ConfigMapKeyRef)
		source = catalogSourceStatic
	case from.SecretKeyRef != nil:
		raw, err = w.readSecretKey(ctx, img.Namespace, from.SecretKeyRef)
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
	return file.Images, source, nil
}

// fetch GETs the live manifest, with the optional bearer token read
// from the platform workspace namespace (uncached read, like every
// Secret read below).
func (w *CatalogSyncWorker) fetch(ctx context.Context, img *waasv1alpha1.WorkspaceImage) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, img.Spec.Catalog.From.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("building catalog request: %w", err)
	}
	if auth := img.Spec.Catalog.Auth; auth != nil && auth.BearerToken != nil {
		secret := &corev1.Secret{}
		key := types.NamespacedName{Namespace: img.Namespace, Name: auth.BearerToken.SecretRef}
		if err := w.kube.Get(ctx, key, secret); err != nil {
			return nil, fmt.Errorf("reading bearer-token secret %s: %w", key.Name, err)
		}
		token, ok := secret.Data[catalogTokenKey]
		if !ok {
			return nil, fmt.Errorf("bearer-token secret %s has no %q key", key.Name, catalogTokenKey)
		}
		req.Header.Set("Authorization", "Bearer "+string(token))
	}
	httpClient := w.HTTPClient
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

func (w *CatalogSyncWorker) readConfigMapKey(ctx context.Context, namespace string, ref *waasv1alpha1.CatalogConfigMapSource) ([]byte, error) {
	cm := &corev1.ConfigMap{}
	if err := w.kube.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, cm); err != nil {
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

func (w *CatalogSyncWorker) readSecretKey(ctx context.Context, namespace string, ref *waasv1alpha1.CatalogSecretSource) ([]byte, error) {
	secret := &corev1.Secret{}
	if err := w.kube.Get(ctx, types.NamespacedName{Namespace: namespace, Name: ref.Name}, secret); err != nil {
		return nil, fmt.Errorf("reading catalog secret %s: %w", ref.Name, err)
	}
	// No default key for Secrets (the CRD requires one); grandfathered
	// empty keys surface as a plain missing-key sync failure.
	if data, ok := secret.Data[ref.Key]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("catalog secret %s has no %q key", ref.Name, ref.Key)
}
