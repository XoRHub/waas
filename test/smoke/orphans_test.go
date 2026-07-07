package smoke

// Zero-orphan gate: for every desktop protocol the platform serves, create
// a real workspace through the public API, delete it (home volume
// included), and assert that NOTHING it owned survives — neither in the
// cluster (every managed type, derived from the operator's single
// managed-types inventory, so a reconciler creating a new type without
// registering it fails here) nor in the platform database (no session row
// left open).
//
// Needs the same running deployment as the connection smoke test PLUS
// cluster access (kubeconfig or in-cluster):
//
//	WAAS_SMOKE_URL=http://waas.127.0.0.1.nip.io:8080 go test ./test/smoke -run TestZeroOrphans

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
)

var namespaceGVK = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Namespace"}

func TestZeroOrphansAfterDeletion(t *testing.T) {
	base := os.Getenv("WAAS_SMOKE_URL")
	if base == "" {
		t.Skip("WAAS_SMOKE_URL not set — smoke test needs a running deployment (make smoke)")
	}
	kube := kubeClientOrSkip(t)
	c := &client_{client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 30 * time.Second}, t: t}}
	c.login(env("WAAS_SMOKE_USER", "admin"), env("WAAS_SMOKE_PASSWORD", "admin123"))

	byProtocol := c.templatesByProtocol()
	protocols := strings.Split(env("WAAS_SMOKE_PROTOCOLS", "vnc,rdp,ssh"), ",")
	for _, protocol := range protocols {
		protocol = strings.TrimSpace(protocol)
		t.Run(protocol, func(t *testing.T) {
			tpl, ok := byProtocol[protocol]
			if !ok {
				t.Fatalf("no template serves protocol %q", protocol)
			}
			sub := client_{client{base: c.base, token: c.token, http: c.http, t: t}}
			sub.zeroOrphanLifecycle(t, kube, tpl, protocol)
		})
	}
}

// client_ only exists to hang the orphan-test helpers on without
// touching the connection smoke test's client.
type client_ struct{ client }

func (c *client_) zeroOrphanLifecycle(t *testing.T, kube k8sclient.Client, template, protocol string) {
	ctx := context.Background()
	name := fmt.Sprintf("orphan %s %d", protocol, time.Now().UnixNano()%100000)
	var ws struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Phase        string `json:"phase"`
		Namespace    string `json:"namespace"`
		WorkloadName string `json:"workloadName"`
	}
	c.do("POST", "/api/v1/workspaces", map[string]any{
		"templateRef": template,
		"displayName": name,
	}, &ws)
	crName := ws.Name

	// Wait until the workspace actually acquired compute: deleting a
	// Pending shell would not exercise the teardown at all.
	deadline := time.Now().Add(readinessTimeout())
	resumed := false
	for ws.Phase != "Running" {
		if time.Now().After(deadline) {
			c.do("DELETE", "/api/v1/workspaces/"+ws.ID, nil, nil)
			t.Fatalf("workspace %s (%s) not Running after %s, last phase %q", ws.ID, template, readinessTimeout(), ws.Phase)
		}
		if !resumed && (ws.Phase == "Stopped" || ws.Phase == "Paused") {
			c.do("POST", "/api/v1/workspaces/"+ws.ID+"/resume", nil, nil)
			resumed = true
		}
		time.Sleep(3 * time.Second)
		c.do("GET", "/api/v1/workspaces/"+ws.ID, nil, &ws)
	}

	// Open a session so the deletion also has session state to clean.
	var conn struct {
		SessionID string `json:"sessionId"`
	}
	c.do("POST", "/api/v1/workspaces/"+ws.ID+"/connect", map[string]any{"protocol": protocol}, &conn)

	// Record whether this workspace's namespace is a DeleteWhenEmpty one
	// it is alone in — then the namespace itself must go too.
	nsMustGo := false
	ns := &unstructured.Unstructured{}
	ns.SetGroupVersionKind(namespaceGVK)
	if err := kube.Get(ctx, types.NamespacedName{Name: ws.Namespace}, ns); err == nil {
		labels := ns.GetLabels()
		nsMustGo = labels[waasv1alpha1.LabelManagedBy] == waasv1alpha1.ManagerName &&
			labels[waasv1alpha1.LabelCleanup] == string(waasv1alpha1.CleanupDeleteWhenEmpty) &&
			c.aloneInNamespace(ctx, kube, ws.Namespace, crName)
	}

	// Delete WITH the home volume: the explicit opt-in must leave nothing.
	c.do("DELETE", "/api/v1/workspaces/"+ws.ID+"?keepVolume=false", nil, nil)

	// The sweep runs against asynchronous deletions (pod grace period,
	// pvc-protection, janitor pass): poll until clean or fail with the
	// precise list of survivors.
	sweepDeadline := time.Now().Add(3 * time.Minute)
	for {
		leftovers := clusterLeftovers(ctx, t, kube, crName)
		if nsMustGo {
			probe := &unstructured.Unstructured{}
			probe.SetGroupVersionKind(namespaceGVK)
			if err := kube.Get(ctx, types.NamespacedName{Name: ws.Namespace}, probe); err == nil {
				leftovers = append(leftovers, "Namespace/"+ws.Namespace+" (DeleteWhenEmpty, was empty)")
			}
		}
		if open := c.openSessions(ws.ID); open > 0 {
			leftovers = append(leftovers, fmt.Sprintf("%d open session row(s) in the database", open))
		}
		if len(leftovers) == 0 {
			t.Logf("%s: zero orphan after deletion (workspace %s)", protocol, crName)
			return
		}
		if time.Now().After(sweepDeadline) {
			t.Fatalf("resources survived the deletion of workspace %s:\n  %s",
				crName, strings.Join(leftovers, "\n  "))
		}
		time.Sleep(5 * time.Second)
	}
}

// clusterLeftovers sweeps every managed namespaced type (single inventory
// in the operator api package) for objects still labeled with the deleted
// workspace's name.
func clusterLeftovers(ctx context.Context, t *testing.T, kube k8sclient.Client, crName string) []string {
	t.Helper()
	var out []string
	for _, gvk := range waasv1alpha1.ManagedNamespacedGVKs() {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gvk)
		err := kube.List(ctx, list, k8sclient.MatchingLabels{waasv1alpha1.LabelWorkspace: crName})
		if err != nil {
			// Optional kinds (KubeVirt) may not be served by this cluster.
			if strings.Contains(err.Error(), "no matches for kind") {
				continue
			}
			t.Fatalf("listing %s: %v", gvk.Kind, err)
		}
		for i := range list.Items {
			obj := &list.Items[i]
			out = append(out, fmt.Sprintf("%s %s/%s", gvk.Kind, obj.GetNamespace(), obj.GetName()))
		}
	}
	return out
}

// aloneInNamespace reports whether no OTHER workspace targets the same
// namespace (admin view: every workspace is visible).
func (c *client_) aloneInNamespace(_ context.Context, _ k8sclient.Client, namespace, crName string) bool {
	var all []struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	c.do("GET", "/api/v1/workspaces", nil, &all)
	for _, w := range all {
		if w.Namespace == namespace && w.Name != crName {
			return false
		}
	}
	return true
}

// openSessions counts still-open session rows for one workspace through
// the admin sessions endpoint.
func (c *client_) openSessions(workspaceID string) int {
	req, err := http.NewRequest("GET", c.base+"/api/v1/sessions?page_size=100", nil)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("listing sessions: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var envelope struct {
		Data []struct {
			WorkspaceID string  `json:"workspaceId"`
			EndedAt     *string `json:"endedAt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		c.t.Fatalf("decoding sessions: %v (%.200s)", err, raw)
	}
	open := 0
	for _, s := range envelope.Data {
		if s.WorkspaceID == workspaceID && s.EndedAt == nil {
			open++
		}
	}
	return open
}

// kubeClientOrSkip builds a controller-runtime client from the ambient
// kubeconfig; the orphan gate is skipped where the cluster is not
// reachable (the connection smoke test still runs).
func kubeClientOrSkip(t *testing.T) k8sclient.Client {
	t.Helper()
	cfg, err := config.GetConfig()
	if err != nil {
		t.Skipf("no cluster access (kubeconfig): %v", err)
	}
	kube, err := k8sclient.New(cfg, k8sclient.Options{})
	if err != nil {
		t.Skipf("building kubernetes client: %v", err)
	}
	// Touch the API once so a dead kubeconfig skips instead of failing.
	ns := &unstructured.UnstructuredList{}
	ns.SetGroupVersionKind(namespaceGVK)
	if err := kube.List(context.Background(), ns, k8sclient.Limit(1)); err != nil {
		t.Skipf("cluster unreachable: %v", err)
	}
	return kube
}
