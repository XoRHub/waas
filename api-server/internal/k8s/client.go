// Package k8s builds the Kubernetes client the API server uses to manage
// Workspace and WorkspaceTemplate CRs. This is the only path by which the
// API server touches the cluster: it never creates pods or VMs directly —
// the operator owns that translation.
package k8s

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

// Scheme registers only the CRD types the API server is allowed to touch.
var Scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(waasv1alpha1.AddToScheme(Scheme))
}

// NewClient returns a cluster client, or an in-memory fake in dev mode so
// the whole API can run on a laptop without a cluster.
func NewClient(devMode bool) (client.Client, error) {
	if devMode {
		return fake.NewClientBuilder().
			WithScheme(Scheme).
			WithStatusSubresource(&waasv1alpha1.Workspace{}).
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
	c, err := client.New(cfg, client.Options{Scheme: Scheme})
	if err != nil {
		return nil, fmt.Errorf("building kubernetes client: %w", err)
	}
	return c, nil
}
