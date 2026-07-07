package main

import (
	"flag"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/internal/controller"
	"github.com/xorhub/waas/operator/internal/kubevirt"
	webhookv1alpha1 "github.com/xorhub/waas/operator/internal/webhook/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/naming"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(waasv1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr, probeAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	restConfig := ctrl.GetConfigOrDie()

	kubeVirtAvailable, err := kubevirt.Detect(restConfig)
	if err != nil {
		setupLog.Error(err, "unable to probe for KubeVirt; assuming unavailable")
		kubeVirtAvailable = false
	}
	setupLog.Info("KubeVirt detection complete", "available", kubeVirtAvailable)

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		WebhookServer:          webhook.NewServer(webhook.Options{Port: 9443}),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "waas-operator.waas.xorhub.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Operator-wide placement pattern (precedence: template pattern >
	// this > built-in "waas-workspace"). An invalid pattern is a refusal
	// to start, NOT a silent fallback: an operator placing workloads
	// differently from what GitOps declares would be an invisible drift.
	// Changing it only affects NEW workspaces (spec.targetNamespace is
	// frozen at creation).
	defaultNamespacePattern := os.Getenv("WAAS_DEFAULT_NAMESPACE_PATTERN")
	if defaultNamespacePattern != "" {
		if err := naming.ValidatePattern(defaultNamespacePattern); err != nil {
			setupLog.Error(err, "invalid WAAS_DEFAULT_NAMESPACE_PATTERN — refusing to start",
				"pattern", defaultNamespacePattern)
			os.Exit(1)
		}
	}
	setupLog.Info("workspace placement configured",
		"defaultNamespacePattern", naming.EffectivePattern("", defaultNamespacePattern))

	if err := (&controller.WorkspaceReconciler{
		Client:            mgr.GetClient(),
		KubeVirtAvailable: kubeVirtAvailable,
		Recorder:          mgr.GetEventRecorderFor("waas-operator"),
		// Where guacd/wwt run (the release namespace, injected by the
		// chart via the downward API): the default-deny ingress of placed
		// workload namespaces must let it in, or placed desktops become
		// unreachable through the proxy.
		PlatformNamespace:       os.Getenv("WAAS_PLATFORM_NAMESPACE"),
		DefaultNamespacePattern: defaultNamespacePattern,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Workspace")
		os.Exit(1)
	}

	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		// Governance wiring (see internal/webhook): WAAS_TRUSTED_WRITERS
		// lists the SAs whose spec.owner/identity annotations are believed
		// (the api-server); WAAS_POLICY_BYPASS lists users/groups exempt
		// from policy (GitOps applier, break-glass admins). Both are set
		// by the Helm chart; empty trusted-writers means even the
		// api-server is treated as an untrusted caller — fail closed.
		trustedWriters := splitEnvList(os.Getenv("WAAS_TRUSTED_WRITERS"))
		bypassSubjects := splitEnvList(os.Getenv("WAAS_POLICY_BYPASS"))
		if len(bypassSubjects) == 0 {
			bypassSubjects = []string{"system:masters"}
		}
		setupLog.Info("workspace governance configured",
			"trustedWriters", trustedWriters, "bypassSubjects", bypassSubjects)
		if err := webhookv1alpha1.SetupWorkspaceWebhookWithManager(mgr, kubeVirtAvailable, trustedWriters, bypassSubjects, defaultNamespacePattern); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "Workspace")
			os.Exit(1)
		}
		if err := webhookv1alpha1.SetupWorkspaceTemplateWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "WorkspaceTemplate")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// splitEnvList parses a comma-separated env value, trimming blanks.
func splitEnvList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
