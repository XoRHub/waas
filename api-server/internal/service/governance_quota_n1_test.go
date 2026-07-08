package service

import (
	"context"
	"sync/atomic"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"

	"github.com/xorhub/waas/api-server/internal/model"
)

// countingClient tallies template reads: the quota view used to GET the
// template of EVERY workspace (an N+1 re-run by the portal's 15s poll).
type countingClient struct {
	client.Client
	templateGets  atomic.Int32
	templateLists atomic.Int32
}

func (c *countingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*waasv1alpha1.WorkspaceTemplate); ok {
		c.templateGets.Add(1)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *countingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*waasv1alpha1.WorkspaceTemplateList); ok {
		c.templateLists.Add(1)
	}
	return c.Client.List(ctx, list, opts...)
}

func TestQuotaDoesOneTemplateListNotNGets(t *testing.T) {
	svc := newGovernanceFixture(t, []model.User{{ID: "u1", Username: "u1"}},
		[]waasv1alpha1.WorkspacePolicy{pol("default", 0, 10)})
	ctx := context.Background()

	tpl := &waasv1alpha1.WorkspaceTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "xfce", Namespace: testNS},
		Spec: waasv1alpha1.WorkspaceTemplateSpec{
			DisplayName: "X", OS: waasv1alpha1.OSLinux, Image: "img:1",
			HomeSize: func() *resource.Quantity { q := resource.MustParse("5Gi"); return &q }(),
		},
	}
	if err := svc.kube.Create(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"w1", "w2", "w3", "w4"} {
		ws := &waasv1alpha1.Workspace{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNS},
			Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "xfce", Owner: "u1"},
		}
		if err := svc.kube.Create(ctx, ws); err != nil {
			t.Fatal(err)
		}
	}

	counter := &countingClient{Client: svc.kube}
	svc.kube = counter

	if _, err := svc.Quota(ctx, Actor{ID: "u1"}); err != nil {
		t.Fatal(err)
	}
	if got := counter.templateGets.Load(); got != 0 {
		t.Fatalf("quota must not GET templates per workspace (the old N+1); got %d gets", got)
	}
	if got := counter.templateLists.Load(); got != 1 {
		t.Fatalf("quota must LIST templates exactly once, got %d", got)
	}
}

// The vanished-template fallback had silently DIVERGED between the
// portal view (0 storage) and the enforcement (DefaultHomeSize): the
// portal undercounted what admission applied. One shared implementation
// now — this pins the contract.
func TestQuotaCountsVanishedTemplateLikeTheEnforcement(t *testing.T) {
	svc := newGovernanceFixture(t, []model.User{{ID: "u1", Username: "u1"}},
		[]waasv1alpha1.WorkspacePolicy{pol("default", 0, 10)})
	ctx := context.Background()

	ws := &waasv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{Name: "orphaned", Namespace: testNS},
		Spec:       waasv1alpha1.WorkspaceSpec{TemplateRef: "gone", Owner: "u1"},
	}
	if err := svc.kube.Create(ctx, ws); err != nil {
		t.Fatal(err)
	}

	quota, err := svc.Quota(ctx, Actor{ID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := quota.Used["storage"]; got != "10Gi" {
		t.Fatalf("a template-less workspace must weigh DefaultHomeSize (10Gi) of storage, exactly like the webhook counts it; got %q", got)
	}
}
