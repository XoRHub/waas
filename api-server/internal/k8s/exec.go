package k8s

import (
	"bytes"
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	ctrl "sigs.k8s.io/controller-runtime"
)

// PodExec runs commands in pods over the exec subresource. The
// controller-runtime client cannot: exec is a streaming (SPDY)
// subresource — so this is the ONE place the api-server holds a plain
// client-go clientset, built from the same kubeconfig resolution as the
// main client (ctrl.GetConfig — in-cluster or local kubeconfig).
type PodExec struct {
	client kubernetes.Interface
	config *rest.Config
}

// NewPodExec builds the exec client. Not used in dev mode (no cluster).
func NewPodExec() (*PodExec, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("building kubernetes clientset: %w", err)
	}
	return &PodExec{client: cs, config: cfg}, nil
}

// Exec runs command — a fixed argv, never a shell — in the container and
// surfaces stderr in the returned error when the command fails.
func (e *PodExec) Exec(ctx context.Context, namespace, pod, container string, command []string) error {
	req := e.client.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(namespace).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)
	exec, err := remotecommand.NewSPDYExecutor(e.config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("building SPDY executor: %w", err)
	}
	var stdout, stderr bytes.Buffer
	if err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &stdout, Stderr: &stderr}); err != nil {
		return fmt.Errorf("exec %v in %s/%s: %w (stderr: %.200s)", command, namespace, pod, err, stderr.String())
	}
	return nil
}
