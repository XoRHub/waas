// Package envtest runs the operator's integration tests against a REAL
// kube-apiserver + etcd (controller-runtime envtest): everything only a
// real apiserver evaluates — CRD schema (CEL rules, enums, defaults,
// required fields), the admission webhooks served end-to-end, and the
// finalizer lifecycle driven by the running reconciler.
//
// Scope note: envtest has NO kube-controller-manager, so ownerReference
// garbage collection never runs here. These tests assert that ownerRefs
// are SET; the actual cascade is only observable in the kind smoke test.
//
// Run with `make test-envtest` (downloads the apiserver binaries via
// setup-envtest). Without KUBEBUILDER_ASSETS every test skips, so a bare
// `go test ./...` stays fast and toolchain-free.
package envtest

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/internal/controller"
	webhookv1alpha1 "github.com/xorhub/waas/operator/internal/webhook/v1alpha1"
)

// trustedWriter is the username the webhook treats as the platform
// api-server (may stamp identity annotations on workspaces).
const trustedWriter = "platform-api"

var (
	envStarted bool
	adminCli   client.Client // full-privilege client from envtest
	aliceCli   client.Client // regular authenticated user "alice"
	trustedCli client.Client // authenticated as trustedWriter
)

// requireEnv skips the test when the envtest control plane is not
// available (no KUBEBUILDER_ASSETS — see the package comment).
func requireEnv(t *testing.T) {
	t.Helper()
	if !envStarted {
		t.Skip("envtest control plane not available; run via `make test-envtest`")
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		// No control plane binaries: run (and skip) the tests.
		os.Exit(m.Run())
	}
	logf.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{"../../config/crd/bases"},
		ErrorIfCRDPathMissing: true,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{"../../config/webhook"},
		},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "starting envtest: %v\n", err)
		os.Exit(1)
	}

	code := func() int {
		defer func() {
			if err := testEnv.Stop(); err != nil {
				fmt.Fprintf(os.Stderr, "stopping envtest: %v\n", err)
			}
		}()

		scheme := runtime.NewScheme()
		if err := clientgoscheme.AddToScheme(scheme); err != nil {
			fmt.Fprintf(os.Stderr, "core scheme: %v\n", err)
			return 1
		}
		if err := waasv1alpha1.AddToScheme(scheme); err != nil {
			fmt.Fprintf(os.Stderr, "waas scheme: %v\n", err)
			return 1
		}

		adminCli, err = client.New(cfg, client.Options{Scheme: scheme})
		if err != nil {
			fmt.Fprintf(os.Stderr, "admin client: %v\n", err)
			return 1
		}

		// The exact wiring of cmd/main.go: both validating webhooks plus
		// the workspace reconciler, on one manager bound to the local
		// serving host/port envtest rewrote the webhook manifests to.
		whOpts := &testEnv.WebhookInstallOptions
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:  scheme,
			Metrics: metricsserver.Options{BindAddress: "0"},
			WebhookServer: webhook.NewServer(webhook.Options{
				Host:    whOpts.LocalServingHost,
				Port:    whOpts.LocalServingPort,
				CertDir: whOpts.LocalServingCertDir,
			}),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "manager: %v\n", err)
			return 1
		}
		if err := webhookv1alpha1.SetupWorkspaceWebhookWithManager(mgr, false, []string{trustedWriter}, nil, ""); err != nil {
			fmt.Fprintf(os.Stderr, "workspace webhook: %v\n", err)
			return 1
		}
		if err := webhookv1alpha1.SetupWorkspaceTemplateWebhookWithManager(mgr); err != nil {
			fmt.Fprintf(os.Stderr, "template webhook: %v\n", err)
			return 1
		}
		if err := (&controller.WorkspaceReconciler{
			Client:   mgr.GetClient(),
			Recorder: mgr.GetEventRecorderFor("waas-operator"), //nolint:staticcheck // SA1019: same as cmd/main.go
			Probe:    func(string) error { return nil },
		}).SetupWithManager(mgr); err != nil {
			fmt.Fprintf(os.Stderr, "reconciler: %v\n", err)
			return 1
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		mgrDone := make(chan error, 1)
		go func() { mgrDone <- mgr.Start(ctx) }()

		if err := waitForWebhookServer(whOpts); err != nil {
			fmt.Fprintf(os.Stderr, "webhook server never came up: %v\n", err)
			return 1
		}

		// Authenticated non-admin users: envtest runs the apiserver with
		// RBAC, so grant them broad rights — these tests exercise
		// ADMISSION, not authorization.
		aliceCli, err = userClient(testEnv, cfg, scheme, "alice", []string{"devs"})
		if err != nil {
			fmt.Fprintf(os.Stderr, "alice client: %v\n", err)
			return 1
		}
		trustedCli, err = userClient(testEnv, cfg, scheme, trustedWriter, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "trusted client: %v\n", err)
			return 1
		}

		envStarted = true
		return m.Run()
	}()
	os.Exit(code)
}

func userClient(testEnv *envtest.Environment, cfg *rest.Config, scheme *runtime.Scheme, name string, groups []string) (client.Client, error) {
	user, err := testEnv.AddUser(envtest.User{Name: name, Groups: groups}, cfg)
	if err != nil {
		return nil, fmt.Errorf("adding user %s: %w", name, err)
	}
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "envtest-" + name},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, APIGroup: rbacv1.GroupName, Name: name}},
		RoleRef:    rbacv1.RoleRef{Kind: "ClusterRole", APIGroup: rbacv1.GroupName, Name: "cluster-admin"},
	}
	if err := adminCli.Create(context.Background(), crb); err != nil {
		return nil, fmt.Errorf("binding %s: %w", name, err)
	}
	return client.New(user.Config(), client.Options{Scheme: scheme})
}

func waitForWebhookServer(whOpts *envtest.WebhookInstallOptions) error {
	addr := net.JoinHostPort(whOpts.LocalServingHost, fmt.Sprintf("%d", whOpts.LocalServingPort))
	dialer := &net.Dialer{Timeout: time.Second}
	var lastErr error
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // readiness probe
		if err == nil {
			return conn.Close()
		}
		lastErr = err
	}
	return lastErr
}

// waitFor polls until check returns nil or the timeout elapses; the last
// error becomes the failure message.
func waitFor(t *testing.T, timeout time.Duration, what string, check func() error) {
	t.Helper()
	var lastErr error
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if lastErr = check(); lastErr == nil {
			return
		}
	}
	t.Fatalf("timed out waiting for %s: %v", what, lastErr)
}
