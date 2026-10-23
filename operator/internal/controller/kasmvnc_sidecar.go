package controller

// The kasmvnc-sidecar of kasmvnc workloads: policy-gated file transfer
// (list / download / upload of the session home) and audio (playback and
// microphone, through the sidecar's own PulseAudio) served to wwt, which
// gates every request on the connection token's grants. Everything
// running INSIDE the desktop container is soft against its own occupant
// (docs/kasmvnc.md); the sidecar is a separate container the occupant
// cannot modify — the governed path for normal use. Its configuration
// surface is WAAS_KASMVNC_SIDECAR_* (operator.kasmvncSidecar.* Helm values); an
// empty image turns the feature off.

import (
	"fmt"
	"os"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/policy"
	"github.com/xorhub/waas/shared/kasmvncsidecar"
)

// KasmVNCSidecarConfig is the operator-wide sidecar configuration.
type KasmVNCSidecarConfig struct {
	// Image is the kasmvnc-sidecar image; empty = no sidecar, no Service port
	// (wwt then answers 503 on the file routes).
	Image string
	// MaxUploadBytes caps one upload request's body.
	MaxUploadBytes int64
}

// KasmVNCSidecarFromEnv reads WAAS_KASMVNC_SIDECAR_IMAGE and
// WAAS_KASMVNC_SIDECAR_MAX_UPLOAD_SIZE (a Kubernetes quantity, e.g. 1Gi;
// missing = the sidecar's default). An unparseable quantity is an error:
// the caller refuses to start rather than silently ship another cap than
// what GitOps declares (same doctrine as the egress config).
func KasmVNCSidecarFromEnv() (KasmVNCSidecarConfig, error) {
	cfg := KasmVNCSidecarConfig{Image: os.Getenv("WAAS_KASMVNC_SIDECAR_IMAGE"), MaxUploadBytes: kasmvncsidecar.DefaultMaxUploadBytes}
	if v := os.Getenv("WAAS_KASMVNC_SIDECAR_MAX_UPLOAD_SIZE"); v != "" {
		q, err := resource.ParseQuantity(v)
		if err != nil {
			return cfg, fmt.Errorf("WAAS_KASMVNC_SIDECAR_MAX_UPLOAD_SIZE: invalid quantity %q: %w", v, err)
		}
		if q.Sign() <= 0 {
			return cfg, fmt.Errorf("WAAS_KASMVNC_SIDECAR_MAX_UPLOAD_SIZE: %q must be positive", v)
		}
		cfg.MaxUploadBytes = q.Value()
	}
	return cfg, nil
}

const kasmvncSidecarContainerName = "kasmvnc-sidecar"

// The emptyDir the sidecar's PulseAudio shares with the desktop (socket
// and microphone FIFO, kasmvncsidecar.PulseDir): Memory-backed and tiny — it
// only ever holds the daemon's runtime files.
const (
	kasmPulseVolume = "waas-pulse"
	kasmPulseSize   = "8Mi"
)

var kasmPulseMount = corev1.VolumeMount{Name: kasmPulseVolume, MountPath: kasmvncsidecar.PulseDir}

// kasmvncSidecarResources is the sidecar's resource shape. shared/kasmvncsidecar
// keeps the numbers (also used by operator/pkg/policy's quota math) so
// the pod spec and the aggregate cpu/memory accounting can't diverge.
var kasmvncSidecarResources = corev1.ResourceRequirements{
	Requests: corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(kasmvncsidecar.SidecarCPURequest),
		corev1.ResourceMemory: resource.MustParse(kasmvncsidecar.SidecarMemoryRequest),
	},
	Limits: corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(kasmvncsidecar.SidecarCPULimit),
		corev1.ResourceMemory: resource.MustParse(kasmvncsidecar.SidecarMemoryLimit),
	},
}

// kasmvncSidecarEnabled says whether THIS workspace gets the sidecar: the image
// is configured, the template serves kasmvnc, and the desktop container
// carries the VNC_PW the sidecar authenticates with (generated, or set by
// the template/override env). A kasm template whose password reaches the
// image some other way gets no sidecar rather than one that cannot start
// and keeps the pod from ever turning Ready.
func (r *WorkspaceReconciler) kasmvncSidecarEnabled(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) bool {
	return r.KasmVNCSidecar.Image != "" && policy.TemplateHasKasmVNC(tpl) && kasmPasswordEnv(desktopEnvOf(ws, tpl)) != nil
}

// desktopEnvOf is desktopEnv without the policy decision — only the
// VNC_PW presence matters here, which the printer toggle cannot change.
func desktopEnvOf(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) []corev1.EnvVar {
	ov := ws.Spec.Overrides
	if ov == nil {
		ov = &waasv1alpha1.WorkspaceOverrides{}
	}
	return desktopEnv(ws, tpl, ov, false, false)
}

func kasmPasswordEnv(env []corev1.EnvVar) *corev1.EnvVar {
	for i := range env {
		if env[i].Name == kasmvncsidecar.EnvPassword {
			return &env[i]
		}
	}
	return nil
}

// kasmvncSidecarContainer builds the sidecar next to the desktop container:
// same home volume at the same path (read-write), the shared Pulse
// emptyDir, the desktop's own VNC_PW entry (whatever its source), and
// the desktop's uid/gid so uploads land owned by the session user and
// the Pulse socket is reachable. Hardened like the platform pods; the
// readiness probe is a TCP connect like the desktop's.
func kasmvncSidecarContainer(tpl *waasv1alpha1.WorkspaceTemplate, desktop corev1.Container, podSC *corev1.PodSecurityContext, cfg KasmVNCSidecarConfig) corev1.Container {
	home := tpl.Spec.EffectiveHomeMountPath()
	uid, gid := kasmvncSidecarIdentity(desktop.SecurityContext, podSC)
	return corev1.Container{
		Name:  kasmvncSidecarContainerName,
		Image: cfg.Image,
		Env: []corev1.EnvVar{
			// The sidecar serves $HOME; the desktop's home mount is the
			// same volume, so the paths agree.
			{Name: "HOME", Value: home},
			*kasmPasswordEnv(desktop.Env),
			{Name: kasmvncsidecar.EnvMaxUploadBytes, Value: strconv.FormatInt(cfg.MaxUploadBytes, 10)},
		},
		Ports: []corev1.ContainerPort{{Name: kasmvncsidecar.PortName, ContainerPort: kasmvncsidecar.Port}},
		// Streaming uploads never spool; PulseAudio and one parec per audio
		// stream are the footprint. A ResourceQuota with limits.aggregate
		// renders limits.cpu/limits.memory (placement.go) — Kubernetes
		// rejects any container missing a limit once that scope exists, so
		// the CPU limit here is load-bearing, not just hardening.
		Resources:    kasmvncSidecarResources,
		VolumeMounts: []corev1.VolumeMount{{Name: "home", MountPath: home}, kasmPulseMount},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(kasmvncsidecar.Port)}},
			InitialDelaySeconds: 2,
			PeriodSeconds:       5,
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:  ptr.To(uid),
			RunAsGroup: ptr.To(gid),
			// A root desktop (admin's choice) would otherwise make the
			// kubelet refuse the sidecar.
			RunAsNonRoot:             ptr.To(uid != 0),
			ReadOnlyRootFilesystem:   ptr.To(true),
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
}

// kasmvncSidecarIdentity is the desktop's effective uid/gid: the container
// securityContext wins over the pod's; the kasmweb images run as
// 1000/1000 when the template says nothing.
func kasmvncSidecarIdentity(sc *corev1.SecurityContext, podSC *corev1.PodSecurityContext) (uid, gid int64) {
	uid, gid = 1000, 1000
	switch {
	case sc != nil && sc.RunAsUser != nil:
		uid = *sc.RunAsUser
	case podSC != nil && podSC.RunAsUser != nil:
		uid = *podSC.RunAsUser
	}
	switch {
	case sc != nil && sc.RunAsGroup != nil:
		gid = *sc.RunAsGroup
	case podSC != nil && podSC.RunAsGroup != nil:
		gid = *podSC.RunAsGroup
	}
	return uid, gid
}
