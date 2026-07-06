package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	waasv1alpha1 "github.com/xorhub/waas/operator/api/v1alpha1"
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
	catalog := &waasv1alpha1.WorkspaceImageList{}
	if err := r.List(ctx, catalog, client.InNamespace(ws.Namespace)); err == nil {
		if img := policy.FindImage(catalog.Items, tpl.Spec.Image); img != nil {
			affinity = archAffinity(img.Spec.Architectures)
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
		MountPath: homeMountPath,
	}}, mergeVolumeMounts(wl.VolumeMounts, ov.VolumeMounts)...)

	securityContext := wl.SecurityContext
	if ov.SecurityContext != nil {
		securityContext = ov.SecurityContext
	}
	podSecurityContext := wl.PodSecurityContext
	if ov.PodSecurityContext != nil {
		podSecurityContext = ov.PodSecurityContext
	}

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: workspaceLabels(ws),
		},
		Spec: corev1.PodSpec{
			RestartPolicy:      corev1.RestartPolicyAlways,
			Affinity:           affinity,
			NodeSelector:       mergeStringMap(wl.NodeSelector, ov.NodeSelector),
			Tolerations:        append(append([]corev1.Toleration{}, wl.Tolerations...), ov.Tolerations...),
			ServiceAccountName: wl.ServiceAccountName,
			SecurityContext:    podSecurityContext,
			Containers: []corev1.Container{{
				Name:            "desktop",
				Image:           tpl.Spec.Image,
				Env:             mergeEnv(tpl.Spec.Env, ov.Env),
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
	err := r.Get(ctx, types.NamespacedName{Namespace: ws.Namespace, Name: name}, existing)
	if err == nil {
		// Reconcile the replica count in place (pause/resume): keep the
		// object, only scale it.
		if existing.Spec.Replicas == nil || *existing.Spec.Replicas != want {
			existing.Spec.Replicas = &want
			if err := r.Update(ctx, existing); err != nil {
				return false, fmt.Errorf("scaling deployment %s to %d: %w", name, want, err)
			}
		}
		return existing.Status.ReadyReplicas > 0 && !paused, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("fetching deployment %s: %w", name, err)
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ws.Namespace,
			Labels:    workspaceLabels(ws),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &want,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{labelWorkspace: ws.Name}},
			// Recreate: the home PVC is RWO, two desktop pods must never
			// overlap during a rollout.
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: r.buildPodTemplate(ctx, ws, tpl, pvcName),
		},
	}
	if err := controllerutil.SetControllerReference(ws, dep, r.Scheme()); err != nil {
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
	err := r.Get(ctx, types.NamespacedName{Namespace: ws.Namespace, Name: name}, existing)
	if err == nil {
		if existing.Spec.Replicas == nil || *existing.Spec.Replicas != want {
			existing.Spec.Replicas = &want
			if err := r.Update(ctx, existing); err != nil {
				return false, fmt.Errorf("scaling statefulset %s to %d: %w", name, want, err)
			}
		}
		return existing.Status.ReadyReplicas > 0 && !paused, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("fetching statefulset %s: %w", name, err)
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ws.Namespace,
			Labels:    workspaceLabels(ws),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &want,
			ServiceName: name,
			Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{labelWorkspace: ws.Name}},
			Template:    r.buildPodTemplate(ctx, ws, tpl, pvcName),
		},
	}
	if err := controllerutil.SetControllerReference(ws, sts, r.Scheme()); err != nil {
		return false, fmt.Errorf("setting owner on statefulset %s: %w", name, err)
	}
	if err := r.Create(ctx, sts); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, fmt.Errorf("creating statefulset %s: %w", name, err)
	}
	return false, nil
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
