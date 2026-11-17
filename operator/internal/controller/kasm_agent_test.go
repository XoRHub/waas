package controller

import (
	"context"
	"slices"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/shared/kasmagent"
)

var testKasmAgent = KasmAgentConfig{Image: "ghcr.io/xorhub/waas/kasm-agent:test", MaxUploadBytes: 42}

// kasmAgentFixture reconciles the workspace with the sidecar configured
// and returns the workload and Service.
func kasmAgentFixture(t *testing.T, tpl *waasv1alpha1.WorkspaceTemplate, ws *waasv1alpha1.Workspace, cfg KasmAgentConfig) (*appsv1.Deployment, *corev1.Service) {
	t.Helper()
	r, c := newFixture(t, tpl, ws)
	r.KasmAgent = cfg
	reconcile(t, r, ws)
	dep := &appsv1.Deployment{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "ws-marc"}, dep); err != nil {
		t.Fatal(err)
	}
	svc := &corev1.Service{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "ws-marc"}, svc); err != nil {
		t.Fatal(err)
	}
	return dep, svc
}

func sidecarOf(t *testing.T, dep *appsv1.Deployment) corev1.Container {
	t.Helper()
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name == kasmAgentContainerName {
			return c
		}
	}
	t.Fatalf("expected the %s sidecar, got %+v", kasmAgentContainerName, dep.Spec.Template.Spec.Containers)
	return corev1.Container{}
}

func envByName(env []corev1.EnvVar) map[string]corev1.EnvVar {
	out := map[string]corev1.EnvVar{}
	for _, e := range env {
		out[e.Name] = e
	}
	return out
}

func hasServicePort(svc *corev1.Service, name string) bool {
	for _, p := range svc.Spec.Ports {
		if p.Name == name {
			return true
		}
	}
	return false
}

// The sidecar shares the desktop's home volume, the Pulse emptyDir, the
// credential and identity, is hardened, and gets its port on the
// workspace Service; the desktop's audio clients are pointed at it.
func TestKasmAgentInjectedOnKasmvncWorkloads(t *testing.T) {
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"
	// A template PULSE_SERVER (legacy data, the webhook refuses it now) is
	// overridden like the side-service toggles.
	tpl := kasmTemplate()
	tpl.Spec.Env = []corev1.EnvVar{{Name: kasmagent.EnvPulseServer, Value: "tcp:elsewhere"}}
	dep, svc := kasmAgentFixture(t, tpl, ws, testKasmAgent)
	spec := dep.Spec.Template.Spec
	if len(spec.Containers) != 2 {
		t.Fatalf("expected desktop + sidecar, got %d containers", len(spec.Containers))
	}
	desktop, agent := spec.Containers[0], sidecarOf(t, dep)
	if agent.Image != testKasmAgent.Image {
		t.Fatalf("unexpected sidecar image %q", agent.Image)
	}

	var pulse *corev1.Volume
	for i := range spec.Volumes {
		if spec.Volumes[i].Name == kasmPulseVolume {
			pulse = &spec.Volumes[i]
		}
	}
	if pulse == nil || pulse.EmptyDir == nil || pulse.EmptyDir.Medium != corev1.StorageMediumMemory {
		t.Fatalf("expected a Memory emptyDir %q for the Pulse socket, got %+v", kasmPulseVolume, spec.Volumes)
	}
	mountsPulse := func(m corev1.VolumeMount) bool { return m.Name == kasmPulseVolume && m.MountPath == kasmagent.PulseDir }
	if !slices.ContainsFunc(desktop.VolumeMounts, mountsPulse) || !slices.ContainsFunc(agent.VolumeMounts, mountsPulse) {
		t.Fatalf("both containers must mount the Pulse emptyDir at %s, got desktop %+v, agent %+v", kasmagent.PulseDir, desktop.VolumeMounts, agent.VolumeMounts)
	}
	var pulseServer []string
	for _, e := range desktop.Env {
		if e.Name == kasmagent.EnvPulseServer {
			pulseServer = append(pulseServer, e.Value)
		}
	}
	if !slices.Equal(pulseServer, []string{kasmagent.PulseServer}) {
		t.Fatalf("the desktop must get exactly one PULSE_SERVER pointing at the sidecar, got %v", pulseServer)
	}

	env := envByName(agent.Env)
	if env["HOME"].Value != "/home/kasm-user" {
		t.Fatalf("HOME must be the desktop's home mount, got %+v", env["HOME"])
	}
	pw := env[kasmagent.EnvPassword]
	if pw.ValueFrom == nil || pw.ValueFrom.SecretKeyRef == nil ||
		pw.ValueFrom.SecretKeyRef.Name != envByName(desktop.Env)[kasmagent.EnvPassword].ValueFrom.SecretKeyRef.Name {
		t.Fatalf("the sidecar must read the same VNC_PW secret as the desktop, got %+v", pw)
	}
	if env[kasmagent.EnvMaxUploadBytes].Value != "42" {
		t.Fatalf("expected the configured upload cap, got %+v", env[kasmagent.EnvMaxUploadBytes])
	}

	if len(agent.Ports) != 1 || agent.Ports[0].Name != kasmagent.PortName || agent.Ports[0].ContainerPort != kasmagent.Port {
		t.Fatalf("unexpected sidecar ports %+v", agent.Ports)
	}
	if len(agent.VolumeMounts) != 2 || agent.VolumeMounts[0].Name != "home" ||
		agent.VolumeMounts[0].MountPath != "/home/kasm-user" || agent.VolumeMounts[0].ReadOnly {
		t.Fatalf("the sidecar must mount the home volume read-write at the same path, got %+v", agent.VolumeMounts)
	}
	if agent.ReadinessProbe == nil || agent.ReadinessProbe.TCPSocket == nil || agent.ReadinessProbe.TCPSocket.Port.IntValue() != int(kasmagent.Port) {
		t.Fatalf("expected a TCP readiness probe on the agent port, got %+v", agent.ReadinessProbe)
	}

	sc := agent.SecurityContext
	if sc == nil || *sc.RunAsUser != 1000 || *sc.RunAsGroup != 1000 || !*sc.RunAsNonRoot ||
		!*sc.ReadOnlyRootFilesystem || *sc.AllowPrivilegeEscalation ||
		sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" ||
		sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Fatalf("sidecar must run as the kasm uid/gid, hardened; got %+v", sc)
	}

	if !hasServicePort(svc, kasmagent.PortName) {
		t.Fatalf("expected the %s port on the Service, got %+v", kasmagent.PortName, svc.Spec.Ports)
	}
}

// Feature off (no image) or not a kasmvnc workload: no sidecar, no port,
// no Pulse volume, no PULSE_SERVER.
func TestKasmAgentAbsentWhenOffOrNotKasmvnc(t *testing.T) {
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"
	dep, svc := kasmAgentFixture(t, kasmTemplate(), ws, KasmAgentConfig{})
	if len(dep.Spec.Template.Spec.Containers) != 1 || hasServicePort(svc, kasmagent.PortName) {
		t.Fatalf("no image configured: expected no sidecar and no Service port, got %d containers, %+v",
			len(dep.Spec.Template.Spec.Containers), svc.Spec.Ports)
	}
	desktop := dep.Spec.Template.Spec.Containers[0]
	if slices.ContainsFunc(dep.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == kasmPulseVolume }) ||
		slices.ContainsFunc(desktop.VolumeMounts, func(m corev1.VolumeMount) bool { return m.Name == kasmPulseVolume }) ||
		envByName(desktop.Env)[kasmagent.EnvPulseServer].Name != "" {
		t.Fatal("without the sidecar there is no Pulse volume and no PULSE_SERVER to stamp")
	}

	dep, svc = kasmAgentFixture(t, linuxTemplate(), workspace(), testKasmAgent)
	if len(dep.Spec.Template.Spec.Containers) != 1 || hasServicePort(svc, kasmagent.PortName) {
		t.Fatalf("guacd workload: expected no sidecar and no Service port, got %d containers, %+v",
			len(dep.Spec.Template.Spec.Containers), svc.Spec.Ports)
	}
}

// The credential is whatever VNC_PW the desktop has: a template-set entry
// is copied verbatim; a template with an explicit credentials Secret and
// no VNC_PW env gets no sidecar at all (it could never authenticate).
func TestKasmAgentFollowsTheDesktopPassword(t *testing.T) {
	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"

	tpl := kasmTemplate()
	tpl.Spec.Env = []corev1.EnvVar{{Name: kasmagent.EnvPassword, Value: "static-pw"}}
	dep, _ := kasmAgentFixture(t, tpl, ws, testKasmAgent)
	if got := envByName(sidecarOf(t, dep).Env)[kasmagent.EnvPassword]; got.Value != "static-pw" || got.ValueFrom != nil {
		t.Fatalf("expected the template's VNC_PW entry on the sidecar, got %+v", got)
	}

	tpl = kasmTemplate()
	tpl.Spec.Protocols[0].CredentialsSecretRef = "my-kasm-creds"
	dep, svc := kasmAgentFixture(t, tpl, ws, testKasmAgent)
	if len(dep.Spec.Template.Spec.Containers) != 1 || hasServicePort(svc, kasmagent.PortName) {
		t.Fatalf("without a VNC_PW env there is nothing to authenticate with: expected no sidecar, got %d containers, %+v",
			len(dep.Spec.Template.Spec.Containers), svc.Spec.Ports)
	}
}

// Uploads must land owned by the session user: the sidecar runs as the
// desktop's uid/gid (container securityContext over pod securityContext
// over the kasmweb 1000/1000), and a root desktop does not trip
// runAsNonRoot.
func TestKasmAgentIdentity(t *testing.T) {
	cases := []struct {
		name     string
		sc       *corev1.SecurityContext
		podSC    *corev1.PodSecurityContext
		uid, gid int64
	}{
		{"kasmweb default", nil, nil, 1000, 1000},
		{"pod-level", nil, &corev1.PodSecurityContext{RunAsUser: ptr.To[int64](2000), RunAsGroup: ptr.To[int64](3000)}, 2000, 3000},
		{"container wins", &corev1.SecurityContext{RunAsUser: ptr.To[int64](1234)},
			&corev1.PodSecurityContext{RunAsUser: ptr.To[int64](2000), RunAsGroup: ptr.To[int64](3000)}, 1234, 3000},
	}
	for _, tc := range cases {
		uid, gid := kasmAgentIdentity(tc.sc, tc.podSC)
		if uid != tc.uid || gid != tc.gid {
			t.Errorf("%s: got %d/%d, want %d/%d", tc.name, uid, gid, tc.uid, tc.gid)
		}
	}

	ws := workspace()
	ws.Spec.TemplateRef = "kasm-firefox"
	tpl := kasmTemplate()
	tpl.Spec.Workload = &waasv1alpha1.WorkspaceWorkload{SecurityContext: &corev1.SecurityContext{RunAsUser: ptr.To[int64](0)}}
	dep, _ := kasmAgentFixture(t, tpl, ws, testKasmAgent)
	sc := sidecarOf(t, dep).SecurityContext
	if *sc.RunAsUser != 0 || *sc.RunAsNonRoot {
		t.Fatalf("a root desktop must not make the kubelet refuse the sidecar, got %+v", sc)
	}
}

func TestKasmAgentFromEnv(t *testing.T) {
	t.Setenv("WAAS_KASM_AGENT_IMAGE", "img:1")
	t.Setenv("WAAS_KASM_AGENT_MAX_UPLOAD_SIZE", "")
	cfg, err := KasmAgentFromEnv()
	if err != nil || cfg.Image != "img:1" || cfg.MaxUploadBytes != kasmagent.DefaultMaxUploadBytes {
		t.Fatalf("missing cap must fall back to the default, got %+v %v", cfg, err)
	}

	t.Setenv("WAAS_KASM_AGENT_MAX_UPLOAD_SIZE", "2Gi")
	if cfg, err = KasmAgentFromEnv(); err != nil || cfg.MaxUploadBytes != 2<<30 {
		t.Fatalf("expected 2Gi parsed, got %+v %v", cfg, err)
	}

	for _, bad := range []string{"lots", "0", "-1Mi"} {
		t.Setenv("WAAS_KASM_AGENT_MAX_UPLOAD_SIZE", bad)
		if _, err := KasmAgentFromEnv(); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
