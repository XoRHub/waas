package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
	"github.com/xorhub/waas/operator/pkg/metakeys"
	"github.com/xorhub/waas/operator/pkg/policy"
)

// buildPodTemplate assembles the desktop pod spec from the template's
// workload passthrough and the workspace's admitted overrides. The home PVC
// mount and the protocol ports are platform-managed and always present.
func (r *WorkspaceReconciler) buildPodTemplate(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, pvcName string) corev1.PodTemplateSpec {
	wl := tpl.Spec.Workload
	if wl == nil {
		wl = &waasv1alpha1.WorkspaceWorkload{}
	}
	ov := ws.Spec.Overrides
	if ov == nil {
		ov = &waasv1alpha1.WorkspaceOverrides{}
	}

	resources := tpl.Spec.Resources
	if ws.Spec.Resources != nil {
		resources = *ws.Spec.Resources
	}

	// Multi-arch scheduling: the catalog entry knows which architectures
	// the image is published for; missing catalog entry means no
	// constraint (pre-governance).
	var affinity *corev1.Affinity
	var pullSecrets []corev1.LocalObjectReference
	catalog := &waasv1alpha1.WorkspaceImageList{}
	if err := r.List(ctx, catalog, client.InNamespace(ws.Namespace)); err == nil {
		if img := policy.FindImage(catalog.Items, tpl.Spec.Image); img != nil {
			affinity = archAffinity(img.Spec.Architectures)
			// Private registry: the namespace-local copy (or the source
			// itself when unplaced) ensured by ensurePullSecret.
			if img.Spec.ImagePullSecretRef != "" {
				pullSecrets = append(pullSecrets,
					corev1.LocalObjectReference{Name: pullSecretPodName(ws, img.Spec.ImagePullSecretRef)})
			}
		}
	}

	protocols := tpl.Spec.EffectiveProtocols()
	ports := make([]corev1.ContainerPort, 0, len(protocols))
	for _, p := range protocols {
		ports = append(ports, corev1.ContainerPort{Name: p.Name, ContainerPort: p.Port})
	}
	probePort := effectiveProtocol(ws, tpl).Port

	volumes := append([]corev1.Volume{{
		Name: "home",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName},
		},
	}}, mergeVolumes(wl.Volumes, ov.Volumes)...)
	mounts := append([]corev1.VolumeMount{{
		Name:      "home",
		MountPath: tpl.Spec.EffectiveHomeMountPath(),
	}}, mergeVolumeMounts(wl.VolumeMounts, ov.VolumeMounts)...)

	// Opaque user-level KasmVNC config: single-file subPath mount ON TOP
	// of the home volume — the .vnc directory itself stays writable for
	// the runtime artifacts KasmVNC drops there (self.pem).
	if tpl.Spec.KasmVNCConfig != "" {
		volumes = append(volumes, corev1.Volume{
			Name: kasmConfigVolume,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: computeName(ws)},
					Items:                []corev1.KeyToPath{{Key: kasmConfigKey, Path: kasmConfigKey}},
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      kasmConfigVolume,
			MountPath: tpl.Spec.EffectiveHomeMountPath() + "/.vnc/" + kasmConfigKey,
			SubPath:   kasmConfigKey,
			ReadOnly:  true,
		})
	}

	securityContext := wl.SecurityContext
	if ov.SecurityContext != nil {
		securityContext = ov.SecurityContext
	}
	podSecurityContext := wl.PodSecurityContext
	if ov.PodSecurityContext != nil {
		podSecurityContext = ov.PodSecurityContext
	}

	labels, annotations := workloadMeta(ws, tpl)
	// subPath mounts never refresh in place: hashing the config into the
	// pod template makes a content change roll the workload.
	if tpl.Spec.KasmVNCConfig != "" {
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[annotationKasmConfigHash] = kasmConfigHash(tpl.Spec.KasmVNCConfig)
	}
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:      corev1.RestartPolicyAlways,
			Affinity:           affinity,
			ImagePullSecrets:   pullSecrets,
			NodeSelector:       mergeStringMap(wl.NodeSelector, ov.NodeSelector),
			Tolerations:        append(append([]corev1.Toleration{}, wl.Tolerations...), ov.Tolerations...),
			ServiceAccountName: wl.ServiceAccountName,
			SecurityContext:    podSecurityContext,
			Containers: []corev1.Container{{
				Name:            "desktop",
				Image:           tpl.Spec.Image,
				Env:             desktopEnv(ws, tpl, ov),
				Resources:       resources,
				Ports:           ports,
				SecurityContext: securityContext,
				VolumeMounts:    mounts,
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(probePort)},
					},
					InitialDelaySeconds: 5,
					PeriodSeconds:       5,
				},
				LivenessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(probePort)},
					},
					InitialDelaySeconds: 30,
					PeriodSeconds:       10,
				},
			}},
			Volumes: volumes,
		},
	}
}

// workloadMeta merges the template's workload metadata and the
// workspace's metadata overrides under the platform labels. The webhook
// already rejected reserved keys; MergeAllowed re-filters here (second
// line) and guarantees operator labels always win.
func workloadMeta(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) (labels, annotations map[string]string) {
	var userLabels, userAnnotations map[string]string
	if wl := tpl.Spec.Workload; wl != nil {
		userLabels, userAnnotations = wl.Labels, wl.Annotations
	}
	if ov := ws.Spec.Overrides; ov != nil {
		userLabels = mergeStringMap(userLabels, ov.Labels)
		userAnnotations = mergeStringMap(userAnnotations, ov.Annotations)
	}
	return metakeys.MergeAllowed(userLabels, workspaceLabels(ws)), metakeys.MergeAllowed(userAnnotations, nil)
}

// effectiveProtocol resolves the workspace's default protocol: the
// creator's choice when it names a declared protocol, else the template
// default.
func effectiveProtocol(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate) waasv1alpha1.WorkspaceProtocol {
	if ws.Spec.Overrides != nil && ws.Spec.Overrides.Protocol != "" {
		if p := tpl.Spec.ProtocolNamed(ws.Spec.Overrides.Protocol); p != nil {
			return *p
		}
	}
	return tpl.Spec.DefaultProtocol()
}

// ensureWorkload creates the desktop workload of the kind the template
// asks for and reports readiness.
// ensureWorkload reconciles the desktop workload towards the desired
// replica count (0 when paused, 1 otherwise). It returns whether the
// desktop is ready to serve — always false while paused.
func (r *WorkspaceReconciler) ensureWorkload(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, pvcName string, paused bool) (bool, error) {
	switch tpl.Spec.WorkloadKindOrDefault() {
	case waasv1alpha1.WorkloadPod:
		return r.ensurePod(ctx, ws, tpl, pvcName, paused)
	case waasv1alpha1.WorkloadStatefulSet:
		return r.ensureStatefulSet(ctx, ws, tpl, pvcName, paused)
	default:
		return r.ensureDeployment(ctx, ws, tpl, pvcName, paused)
	}
}

// desiredReplicas is 0 while paused (scale-to-0), 1 otherwise.
func desiredReplicas(paused bool) int32 {
	if paused {
		return 0
	}
	return 1
}

func (r *WorkspaceReconciler) ensureDeployment(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, pvcName string, paused bool) (bool, error) {
	name := computeName(ws)
	want := desiredReplicas(paused)
	existing := &appsv1.Deployment{}
	err := r.Get(ctx, computeKey(ws), existing)
	if err == nil {
		// Reconcile the replica count in place (pause/resume): keep the
		// object, only scale it. The pod template is otherwise create-only
		// — EXCEPT the kasmvnc config hash: subPath mounts go silently
		// stale, so a config change is the one template edit that rolls
		// the workload (same desired-state stance as the ingress netpol).
		changed := false
		if existing.Spec.Replicas == nil || *existing.Spec.Replicas != want {
			existing.Spec.Replicas = &want
			changed = true
		}
		if kasmConfigDrifted(existing.Spec.Template.Annotations, tpl) {
			existing.Spec.Template = r.buildPodTemplate(ctx, ws, tpl, pvcName)
			changed = true
		}
		if changed {
			if err := r.Update(ctx, existing); err != nil {
				return false, fmt.Errorf("updating deployment %s: %w", name, err)
			}
		}
		return existing.Status.ReadyReplicas > 0 && !paused, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("fetching deployment %s: %w", name, err)
	}

	template := r.buildPodTemplate(ctx, ws, tpl, pvcName)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   computeNamespace(ws),
			Labels:      template.Labels,
			Annotations: template.Annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &want,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{labelWorkspace: ws.Name}},
			// Recreate: the home PVC is RWO, two desktop pods must never
			// overlap during a rollout.
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: template,
		},
	}
	if err := r.setOwnerIfLocal(ws, dep); err != nil {
		return false, fmt.Errorf("setting owner on deployment %s: %w", name, err)
	}
	if err := r.Create(ctx, dep); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, fmt.Errorf("creating deployment %s: %w", name, err)
	}
	return false, nil
}

func (r *WorkspaceReconciler) ensureStatefulSet(ctx context.Context, ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, pvcName string, paused bool) (bool, error) {
	name := computeName(ws)
	want := desiredReplicas(paused)
	existing := &appsv1.StatefulSet{}
	err := r.Get(ctx, computeKey(ws), existing)
	if err == nil {
		changed := false
		if existing.Spec.Replicas == nil || *existing.Spec.Replicas != want {
			existing.Spec.Replicas = &want
			changed = true
		}
		// See ensureDeployment: the kasmvnc config hash is the one
		// template edit converged in place.
		if kasmConfigDrifted(existing.Spec.Template.Annotations, tpl) {
			existing.Spec.Template = r.buildPodTemplate(ctx, ws, tpl, pvcName)
			changed = true
		}
		if changed {
			if err := r.Update(ctx, existing); err != nil {
				return false, fmt.Errorf("updating statefulset %s: %w", name, err)
			}
		}
		return existing.Status.ReadyReplicas > 0 && !paused, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("fetching statefulset %s: %w", name, err)
	}

	template := r.buildPodTemplate(ctx, ws, tpl, pvcName)
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   computeNamespace(ws),
			Labels:      template.Labels,
			Annotations: template.Annotations,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &want,
			ServiceName: name,
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{labelWorkspace: ws.Name}},
			Template:    template,
		},
	}
	if err := r.setOwnerIfLocal(ws, sts); err != nil {
		return false, fmt.Errorf("setting owner on statefulset %s: %w", name, err)
	}
	if err := r.Create(ctx, sts); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, fmt.Errorf("creating statefulset %s: %w", name, err)
	}
	return false, nil
}

// desktopEnv is the container environment: template env with the
// workspace's admitted overrides on top, plus the generated KasmVNC
// password (VNC_PW from the pod-namespace Secret) when no explicit
// source provides one.
func desktopEnv(ws *waasv1alpha1.Workspace, tpl *waasv1alpha1.WorkspaceTemplate, ov *waasv1alpha1.WorkspaceOverrides) []corev1.EnvVar {
	env := mergeEnv(tpl.Spec.Env, ov.Env)
	if kasmPasswordGenerated(ws, tpl) {
		env = append(env, kasmEnv(ws))
	}
	return env
}

// mergeEnv lays override entries over the base list; an override with the
// same name replaces the base entry, new names are appended in order.
func mergeEnv(base, over []corev1.EnvVar) []corev1.EnvVar {
	if len(over) == 0 {
		return base
	}
	out := append([]corev1.EnvVar{}, base...)
	for _, o := range over {
		replaced := false
		for i := range out {
			if out[i].Name == o.Name {
				out[i] = o
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, o)
		}
	}
	return out
}

func mergeVolumes(base, over []corev1.Volume) []corev1.Volume {
	if len(over) == 0 {
		return base
	}
	out := append([]corev1.Volume{}, base...)
	for _, o := range over {
		replaced := false
		for i := range out {
			if out[i].Name == o.Name {
				out[i] = o
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, o)
		}
	}
	return out
}

func mergeVolumeMounts(base, over []corev1.VolumeMount) []corev1.VolumeMount {
	if len(over) == 0 {
		return base
	}
	out := append([]corev1.VolumeMount{}, base...)
	for _, o := range over {
		replaced := false
		for i := range out {
			if out[i].Name == o.Name {
				out[i] = o
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, o)
		}
	}
	return out
}

func mergeStringMap(base, over map[string]string) map[string]string {
	if len(base) == 0 && len(over) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}
