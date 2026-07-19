// Package k8s builds the Kubernetes client the API server uses to manage
// Workspace and WorkspaceTemplate CRs. This is the only path by which the
// API server touches the cluster: it never creates pods or VMs directly —
// the operator owns that translation.
package k8s

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// Scheme registers the CRD types the API server is allowed to touch, plus
// core Secrets: connect-time credential resolution reads the protocol
// credentialsSecretRef (read-only — RBAC grants get only, in the workspace
// namespace only).
var Scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(waasv1alpha1.AddToScheme(Scheme))
	utilruntime.Must(corev1.AddToScheme(Scheme))
}

// NewClient returns a cluster client, or an in-memory fake in dev mode so
// the whole API can run on a laptop without a cluster. The client carries
// Watch (client.WithWatch): the SSE event hub relays Workspace changes to
// the portal from one shared watch.
func NewClient(devMode bool) (client.WithWatch, error) {
	if devMode {
		return fake.NewClientBuilder().
			WithScheme(Scheme).
			WithStatusSubresource(&waasv1alpha1.Workspace{}, &waasv1alpha1.WorkspaceImage{}).
			// The real API server assigns UIDs; the fake does not, and the
			// API exposes UIDs as workspace IDs, so mimic it.
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if obj.GetUID() == "" {
						obj.SetUID(types.UID(uuid.NewString()))
					}
					return c.Create(ctx, obj, opts...)
				},
			}).
			Build(), nil
	}
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	c, err := client.NewWithWatch(cfg, client.Options{Scheme: Scheme})
	if err != nil {
		return nil, fmt.Errorf("building kubernetes client: %w", err)
	}
	return c, nil
}
